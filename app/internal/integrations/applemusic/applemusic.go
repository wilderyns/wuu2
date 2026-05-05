package applemusic

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html/template"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"wuu2/internal/config"
	"wuu2/internal/lib/persistence"
	"wuu2/internal/model"
)

const (
	musicKitScriptURL         = "https://js-cdn.music.apple.com/musickit/v3/musickit.js"
	developerTokenLifetime    = 12 * time.Hour
	developerTokenRefreshSkew = 5 * time.Minute
	developerTokenMaxLifetime = 15777000 * time.Second
)

var (
	errUnauthorized       = errors.New("applemusic unauthorized")
	httpClient            = http.DefaultClient
	recentPlayedTracksURL = "https://api.music.apple.com/v1/me/recent/played/tracks"
	authKeyIDPattern      = regexp.MustCompile(`^AuthKey_([A-Za-z0-9]+)\.p8$`)
)

type recentPlayedTrackAttributes struct {
	Name       string `json:"name"`
	AlbumName  string `json:"albumName"`
	ArtistName string `json:"artistName"`
	URL        string `json:"url"`
}

type recentPlayedTrack struct {
	ID         string                      `json:"id"`
	Type       string                      `json:"type"`
	Attributes recentPlayedTrackAttributes `json:"attributes"`
}

type includedResource struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"attributes"`
}

type recentPlayedTracksResponse struct {
	Data     []recentPlayedTrack `json:"data"`
	Included []includedResource  `json:"included"`
}

type authState struct {
	mu           sync.Mutex
	userToken    string
	state        string
	startEnabled bool
}

type Client struct {
	config        config.Config
	auth          authState
	updateMu      sync.Mutex
	tokenFilePath string
	devTokenMu    sync.Mutex
	devToken      string
	devTokenExp   time.Time
}

type authCallbackRequest struct {
	MusicUserToken string `json:"musicUserToken"`
	State          string `json:"state"`
}

type startViewData struct {
	CallbackURL    string
	DeveloperToken string
	State          string
	ScriptURL      string
}

var startTemplate = template.Must(template.New("applemusic_start").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Apple Music Authorization</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; margin: 2rem; max-width: 38rem; }
    button { font-size: 1rem; padding: 0.8rem 1rem; }
    p { line-height: 1.5; }
    .note { color: #555; }
    .status { margin-top: 1rem; font-weight: 600; }
  </style>
  <script src="{{.ScriptURL}}"></script>
</head>
<body>
  <h1>Authorize Apple Music</h1>
  <p>This connects your Apple Music account so wuu2 can fetch your current or most recently played song.</p>
  <p class="note">You need an active Apple Music subscription for this flow to succeed.</p>
  <button id="authorize" data-callback-url="{{.CallbackURL}}" data-developer-token="{{.DeveloperToken}}" data-state="{{.State}}" disabled>Authorize Apple Music</button>
  <p id="status" class="status">Loading MusicKit…</p>
  <script>
    (function () {
      const button = document.getElementById("authorize");
      const status = document.getElementById("status");

      async function authorize() {
        button.disabled = true;
        status.textContent = "Waiting for Apple Music authorization…";

        try {
          let music;
          try {
            music = window.MusicKit.getInstance();
          } catch (_) {}

          if (!music) {
            music = window.MusicKit.configure({
              developerToken: button.dataset.developerToken,
              app: {
                name: "wuu2",
                build: "1.0.0"
              }
            });
          }

          const result = await music.authorize();
          const musicUserToken = music.musicUserToken || result;
          if (!musicUserToken) {
            throw new Error("Apple Music did not return a user token.");
          }

          status.textContent = "Saving Apple Music authorization…";
          const response = await fetch(button.dataset.callbackUrl, {
            method: "POST",
            headers: {
              "Content-Type": "application/json"
            },
            body: JSON.stringify({
              musicUserToken,
              state: button.dataset.state
            })
          });

          const message = await response.text();
          if (!response.ok) {
            throw new Error(message || "Failed saving Apple Music authorization.");
          }

          status.textContent = message || "Apple Music authorization complete.";
        } catch (error) {
          status.textContent = error && error.message ? error.message : "Apple Music authorization failed.";
          button.disabled = false;
        }
      }

      function ready() {
        button.disabled = false;
        status.textContent = "Ready to authorize.";
        button.addEventListener("click", authorize, { once: false });
      }

      if (window.MusicKit) {
        ready();
      } else {
        document.addEventListener("musickitloaded", ready, { once: true });
      }
    }());
  </script>
</body>
</html>`))

func NewClient(cfg config.Config) *Client {
	client := &Client{
		config: cfg,
		auth: authState{
			startEnabled: true,
		},
	}

	if cfg.TokenPersistenceEnabled {
		client.tokenFilePath = persistence.TokenFilePathForDirectory(cfg.PersistenceDirectory, "applemusic")
	}

	return client
}

func (c *Client) LoadPersistedTokenState() error {
	persisted, err := persistence.LoadAuthTokenState(c.tokenFilePath)
	if err != nil {
		return err
	}

	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()

	c.auth.userToken = strings.TrimSpace(persisted.AccessToken)
	c.auth.startEnabled = persisted.StartEnabled

	if c.auth.userToken == "" {
		c.auth.startEnabled = true
	}

	return nil
}

func (c *Client) AuthStartHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !c.hasConfig() {
			http.Error(w, "Apple Music config missing", http.StatusBadRequest)
			return
		}

		if !c.isStartEnabled() {
			http.Error(w, "Apple Music auth start is disabled until re-authorization is required", http.StatusConflict)
			return
		}

		developerToken, err := c.developerToken()
		if err != nil {
			http.Error(w, "failed generating Apple Music developer token", http.StatusInternalServerError)
			fmt.Println("Apple Music developer token generation failed:", err)
			return
		}

		state := randomHex(16)
		c.auth.mu.Lock()
		c.auth.state = state
		c.auth.mu.Unlock()

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = startTemplate.Execute(w, startViewData{
			CallbackURL:    "/auth/applemusic/callback",
			DeveloperToken: developerToken,
			State:          state,
			ScriptURL:      musicKitScriptURL,
		})
	}
}

func (c *Client) AuthCallbackHandler(onAuthorizedRefresh func() error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		defer func(body io.ReadCloser) {
			_ = body.Close()
		}(r.Body)

		var payload authCallbackRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		userToken := strings.TrimSpace(payload.MusicUserToken)
		if userToken == "" {
			http.Error(w, "missing musicUserToken field", http.StatusBadRequest)
			return
		}

		if !c.validateState(payload.State) {
			http.Error(w, "invalid authorization state", http.StatusBadRequest)
			return
		}

		if err := c.storeUserToken(userToken); err != nil {
			http.Error(w, "failed storing Apple Music token", http.StatusInternalServerError)
			fmt.Println("Apple Music token persistence failed:", err)
			return
		}

		if onAuthorizedRefresh != nil {
			if err := onAuthorizedRefresh(); err != nil {
				fmt.Println("Apple Music snapshot refresh failed:", err)
			}
		}

		_, _ = w.Write([]byte("Apple Music auth complete. You can close this tab."))
	}
}

func (c *Client) Update(snapshot *model.Wuu2) {
	if snapshot == nil {
		return
	}
	if !c.config.AppleMusicEnabled {
		snapshot.AppleMusic = nil
		return
	}

	c.updateMu.Lock()
	defer c.updateMu.Unlock()

	if !c.hasConfig() {
		fmt.Println("Apple Music config missing. Skipping Apple Music update.")
		return
	}

	userToken, err := c.ensureUserToken()
	if err != nil {
		fmt.Println("Apple Music auth not ready. Head to " + c.config.Address + "/auth/applemusic/start to authorize.")
		return
	}

	developerToken, err := c.developerToken()
	if err != nil {
		fmt.Println("Apple Music developer token generation failed:", err)
		return
	}

	track, artist, album, err := c.fetchRecentTrack(developerToken, userToken)
	if errors.Is(err, errUnauthorized) {
		if invalidateErr := c.invalidateUserToken(); invalidateErr != nil {
			fmt.Println("Failed writing Apple Music token file:", invalidateErr)
		}
		fmt.Println("Apple Music authorization expired. Head to " + c.config.Address + "/auth/applemusic/start to authorize again.")
		return
	}
	if err != nil {
		fmt.Println("Apple Music recent tracks request failed:", err)
		return
	}
	if track == nil {
		snapshot.AppleMusic = nil
		return
	}

	entry := model.AppleMusic{
		Song:       strings.TrimSpace(track.Attributes.Name),
		SongLink:   strings.TrimSpace(track.Attributes.URL),
		Artist:     strings.TrimSpace(track.Attributes.ArtistName),
		ArtistLink: artistURL(artist),
		Album:      strings.TrimSpace(track.Attributes.AlbumName),
		AlbumLink:  albumURL(album),
	}
	if entry.Artist == "" {
		entry.Artist = includedName(artist)
	}
	if entry.Album == "" {
		entry.Album = includedName(album)
	}

	entry.LastChange = resolveLastChange(firstAppleMusicEntry(snapshot.AppleMusic), entry, time.Now().UTC())
	snapshot.AppleMusic = []model.AppleMusic{entry}
}

func (c *Client) fetchRecentTrack(developerToken string, userToken string) (*recentPlayedTrack, *includedResource, *includedResource, error) {
	req, err := http.NewRequest(http.MethodGet, buildRecentPlayedTracksURL(recentPlayedTracksURL), nil)
	if err != nil {
		return nil, nil, nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(developerToken))
	req.Header.Set("Music-User-Token", strings.TrimSpace(userToken))

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, nil, nil, errUnauthorized
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, nil, nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed recentPlayedTracksResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, nil, nil, err
	}
	if len(parsed.Data) == 0 {
		return nil, nil, nil, nil
	}

	artist := firstIncludedResource(parsed.Included, "artists")
	album := firstIncludedResource(parsed.Included, "albums")
	track := parsed.Data[0]
	return &track, artist, album, nil
}

func buildRecentPlayedTracksURL(base string) string {
	query := url.Values{}
	query.Set("include", "albums,artists")
	query.Set("limit", "1")
	query.Set("types", "songs,library-songs")

	if strings.Contains(base, "?") {
		return base + "&" + query.Encode()
	}
	return base + "?" + query.Encode()
}

func resolveLastChange(existing *model.AppleMusic, current model.AppleMusic, now time.Time) string {
	if existing != nil && sameTrack(*existing, current) {
		lastChange := strings.TrimSpace(existing.LastChange)
		if lastChange != "" {
			return lastChange
		}
	}
	return now.Format(time.RFC3339)
}

func sameTrack(existing model.AppleMusic, current model.AppleMusic) bool {
	return strings.EqualFold(strings.TrimSpace(existing.Song), strings.TrimSpace(current.Song)) &&
		strings.EqualFold(strings.TrimSpace(existing.SongLink), strings.TrimSpace(current.SongLink)) &&
		strings.EqualFold(strings.TrimSpace(existing.Artist), strings.TrimSpace(current.Artist)) &&
		strings.EqualFold(strings.TrimSpace(existing.Album), strings.TrimSpace(current.Album))
}

func firstAppleMusicEntry(entries []model.AppleMusic) *model.AppleMusic {
	if len(entries) == 0 {
		return nil
	}

	entry := entries[0]
	return &entry
}

func firstIncludedResource(resources []includedResource, resourceType string) *includedResource {
	resourceType = strings.TrimSpace(resourceType)
	for i := range resources {
		if strings.EqualFold(strings.TrimSpace(resources[i].Type), resourceType) {
			return &resources[i]
		}
	}
	return nil
}

func includedName(resource *includedResource) string {
	if resource == nil {
		return ""
	}
	return strings.TrimSpace(resource.Attributes.Name)
}

func artistURL(resource *includedResource) string {
	if resource == nil {
		return ""
	}
	return strings.TrimSpace(resource.Attributes.URL)
}

func albumURL(resource *includedResource) string {
	if resource == nil {
		return ""
	}
	return strings.TrimSpace(resource.Attributes.URL)
}

func (c *Client) hasConfig() bool {
	if strings.TrimSpace(c.config.AppleMusicDeveloperToken) != "" {
		return true
	}

	return strings.TrimSpace(c.config.AppleMusicTeamID) != ""
}

func (c *Client) isStartEnabled() bool {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()
	return c.auth.startEnabled
}

func (c *Client) ensureUserToken() (string, error) {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()

	if c.auth.userToken != "" {
		return c.auth.userToken, nil
	}

	c.auth.startEnabled = true
	if err := c.persistTokenStateLocked(); err != nil {
		fmt.Println("Failed writing Apple Music token file:", err)
	}
	return "", fmt.Errorf("apple music auth required")
}

func (c *Client) validateState(state string) bool {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()

	if c.auth.state == "" {
		return true
	}
	if state == "" || state != c.auth.state {
		return false
	}

	c.auth.state = ""
	return true
}

func (c *Client) storeUserToken(userToken string) error {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()

	c.auth.userToken = strings.TrimSpace(userToken)
	c.auth.startEnabled = false
	return c.persistTokenStateLocked()
}

func (c *Client) invalidateUserToken() error {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()

	c.auth.userToken = ""
	c.auth.startEnabled = true
	return c.persistTokenStateLocked()
}

func (c *Client) persistTokenStateLocked() error {
	persisted := persistence.AuthTokenState{
		AccessToken:  strings.TrimSpace(c.auth.userToken),
		StartEnabled: c.auth.startEnabled,
	}
	return persistence.SaveAuthTokenState(c.tokenFilePath, persisted)
}

func randomHex(length int) string {
	if length <= 0 {
		return ""
	}

	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}

	return hex.EncodeToString(buf)
}

func (c *Client) developerToken() (string, error) {
	if direct := strings.TrimSpace(c.config.AppleMusicDeveloperToken); direct != "" {
		return direct, nil
	}

	c.devTokenMu.Lock()
	defer c.devTokenMu.Unlock()

	now := time.Now().UTC()
	if c.devToken != "" && now.Before(c.devTokenExp.Add(-developerTokenRefreshSkew)) {
		return c.devToken, nil
	}

	keyID, privateKey, err := loadSigningKey(c.config.AppleMusicPrivateKeyPath, c.config.AppleMusicKeyID)
	if err != nil {
		return "", err
	}

	token, expiresAt, err := generateDeveloperToken(strings.TrimSpace(c.config.AppleMusicTeamID), keyID, privateKey, now, developerTokenLifetime)
	if err != nil {
		return "", err
	}

	c.devToken = token
	c.devTokenExp = expiresAt
	return c.devToken, nil
}

func loadSigningKey(path string, keyID string) (string, *ecdsa.PrivateKey, error) {
	resolvedPath, err := resolvePrivateKeyPath(path)
	if err != nil {
		return "", nil, err
	}

	resolvedKeyID := strings.TrimSpace(keyID)
	if resolvedKeyID == "" {
		resolvedKeyID = inferKeyIDFromPath(resolvedPath)
	}
	if resolvedKeyID == "" {
		return "", nil, fmt.Errorf("apple music key id missing and could not be inferred from %s", resolvedPath)
	}

	payload, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", nil, err
	}

	block, _ := pem.Decode(payload)
	if block == nil {
		return "", nil, errors.New("apple music private key is not valid PEM")
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", nil, err
	}

	privateKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return "", nil, fmt.Errorf("apple music private key must be ECDSA, got %T", parsed)
	}

	return resolvedKeyID, privateKey, nil
}

func resolvePrivateKeyPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "tokens"
	}

	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	if !info.IsDir() {
		return path, nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}

	var matches []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if strings.HasSuffix(strings.ToLower(name), ".p8") {
			matches = append(matches, filepath.Join(path, name))
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no .p8 files found in %s", path)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("multiple .p8 files found in %s; set APPLEMUSIC_PRIVATE_KEY_PATH explicitly", path)
	}
}

func inferKeyIDFromPath(path string) string {
	base := filepath.Base(strings.TrimSpace(path))
	matches := authKeyIDPattern.FindStringSubmatch(base)
	if len(matches) != 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func generateDeveloperToken(teamID string, keyID string, privateKey *ecdsa.PrivateKey, now time.Time, lifetime time.Duration) (string, time.Time, error) {
	teamID = strings.TrimSpace(teamID)
	keyID = strings.TrimSpace(keyID)
	if teamID == "" {
		return "", time.Time{}, errors.New("apple music team id is required")
	}
	if keyID == "" {
		return "", time.Time{}, errors.New("apple music key id is required")
	}
	if privateKey == nil {
		return "", time.Time{}, errors.New("apple music private key is required")
	}

	if lifetime <= 0 {
		lifetime = developerTokenLifetime
	}
	if lifetime > developerTokenMaxLifetime {
		return "", time.Time{}, fmt.Errorf("apple music developer token lifetime exceeds Apple maximum of %s", developerTokenMaxLifetime)
	}

	now = now.UTC()
	expiresAt := now.Add(lifetime)
	headerJSON, err := json.Marshal(map[string]string{
		"alg": "ES256",
		"kid": keyID,
	})
	if err != nil {
		return "", time.Time{}, err
	}

	claimsJSON, err := json.Marshal(map[string]any{
		"iss": teamID,
		"iat": now.Unix(),
		"exp": expiresAt.Unix(),
	})
	if err != nil {
		return "", time.Time{}, err
	}

	unsignedToken := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(unsignedToken))

	r, s, err := ecdsa.Sign(rand.Reader, privateKey, digest[:])
	if err != nil {
		return "", time.Time{}, err
	}

	signature := encodeECDSASignature(r, s, 32)
	token := unsignedToken + "." + base64.RawURLEncoding.EncodeToString(signature)
	return token, expiresAt, nil
}

func encodeECDSASignature(r *big.Int, s *big.Int, size int) []byte {
	signature := make([]byte, size*2)
	r.FillBytes(signature[:size])
	s.FillBytes(signature[size:])
	return signature
}
