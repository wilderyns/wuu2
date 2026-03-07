package battle

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"wuu2/internal/config"
	"wuu2/internal/lib/persistence"
	"wuu2/internal/lib/timeutil"
	"wuu2/internal/model"
)

const (
	oauthHost       = "https://oauth.battle.net"
	movementEpsilon = 0.001
)

var errUnauthorized = errors.New("battlenet unauthorized")

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type protectedCharacterSummary struct {
	Character struct {
		Name  string `json:"name"`
		Realm struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"realm"`
	} `json:"character"`
	Position struct {
		Zone struct {
			Name string `json:"name"`
		} `json:"zone"`
		Map struct {
			Name string `json:"name"`
		} `json:"map"`
		X      float32 `json:"x"`
		Y      float32 `json:"y"`
		Z      float32 `json:"z"`
		Facing float32 `json:"facing"`
	} `json:"position"`
}

type characterMediaAsset struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type characterMedia struct {
	Assets []characterMediaAsset `json:"assets"`
}

type authState struct {
	mu           sync.Mutex
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	authCode     string
	state        string
	startEnabled bool
}

type Client struct {
	config             config.Config
	auth               authState
	updateMu           sync.Mutex
	tokenFilePath      string
	wowMovementHistory []model.Wow
}

func NewClient(cfg config.Config) *Client {
	client := &Client{
		config: cfg,
		auth: authState{
			startEnabled: true,
		},
	}

	if cfg.TokenPersistenceEnabled {
		client.tokenFilePath = persistence.TokenFilePathForDirectory(cfg.PersistenceDirectory, "battlenet")
	}

	return client
}

func (c *Client) LoadPersistedTokenState() error {
	persisted, err := persistence.LoadAuthTokenState(c.tokenFilePath)
	if err != nil {
		return err
	}

	var parsedExpiry time.Time
	expiresAtRaw := strings.TrimSpace(persisted.ExpiresAt)
	if expiresAtRaw != "" {
		parsedExpiry, err = time.Parse(time.RFC3339, expiresAtRaw)
		if err != nil {
			return err
		}
	}

	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()

	c.auth.accessToken = strings.TrimSpace(persisted.AccessToken)
	c.auth.refreshToken = strings.TrimSpace(persisted.RefreshToken)
	c.auth.expiresAt = parsedExpiry
	c.auth.startEnabled = persisted.StartEnabled

	if c.auth.accessToken == "" && c.auth.refreshToken == "" {
		c.auth.startEnabled = true
	}

	return nil
}

func (c *Client) AuthStartHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !c.hasConfig() {
			http.Error(w, "Battle.net config missing", http.StatusBadRequest)
			return
		}

		if !c.isStartEnabled() {
			http.Error(w, "Battle.net auth start is disabled until re-authentication is required", http.StatusConflict)
			return
		}

		state := randomHex(16)
		c.auth.mu.Lock()
		c.auth.state = state
		c.auth.mu.Unlock()

		http.Redirect(w, r, c.buildAuthorizeURL(state), http.StatusFound)
	}
}

func (c *Client) AuthCallbackHandler(onAuthorizedRefresh func() error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code parameter", http.StatusBadRequest)
			return
		}

		state := r.URL.Query().Get("state")
		if !c.validateState(state) {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}

		c.auth.mu.Lock()
		c.auth.authCode = code
		c.auth.mu.Unlock()

		_, err := c.ensureAccessToken()
		if err != nil {
			http.Error(w, "failed exchanging OAuth code", http.StatusInternalServerError)
			fmt.Println("Battle.net OAuth callback exchange failed:", err)
			return
		}

		if onAuthorizedRefresh != nil {
			if err := onAuthorizedRefresh(); err != nil {
				fmt.Println("Battle.net snapshot refresh failed:", err)
			}
		}

		_, _ = w.Write([]byte("Battle.net auth complete. You can close this tab."))
	}
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

func (c *Client) isStartEnabled() bool {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()
	return c.auth.startEnabled
}

func (c *Client) enableStart() {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()
	c.auth.startEnabled = true
	if err := c.persistTokenStateLocked(); err != nil {
		fmt.Println("Failed writing Battle.net token file:", err)
	}
}

func (c *Client) clearAccessToken() {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()
	c.auth.accessToken = ""
	c.auth.expiresAt = time.Time{}
	if err := c.persistTokenStateLocked(); err != nil {
		fmt.Println("Failed writing Battle.net token file:", err)
	}
}

func (c *Client) ensureAccessToken() (string, error) {
	c.auth.mu.Lock()
	defer c.auth.mu.Unlock()

	now := time.Now()

	if c.auth.accessToken != "" && now.Before(c.auth.expiresAt.Add(-1*time.Minute)) {
		return c.auth.accessToken, nil
	}

	if c.auth.refreshToken != "" {
		token, err := c.exchangeToken(url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {c.auth.refreshToken},
		})
		if err == nil {
			c.applyTokenLocked(token, now)
			return c.auth.accessToken, nil
		}
		fmt.Println("Battle.net refresh failed, falling back to auth code:", err)
	}

	if c.auth.authCode == "" {
		c.auth.startEnabled = true
		if err := c.persistTokenStateLocked(); err != nil {
			fmt.Println("Failed writing Battle.net token file:", err)
		}
		return "", fmt.Errorf("battlenet auth required: open %s", c.buildAuthorizeURL(""))
	}

	token, err := c.exchangeToken(url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {c.auth.authCode},
		"redirect_uri": {c.config.BattleNetRedirectURI},
	})
	if err != nil {
		c.auth.startEnabled = true
		return "", err
	}

	c.auth.authCode = ""
	c.applyTokenLocked(token, now)
	return c.auth.accessToken, nil
}

func (c *Client) applyTokenLocked(token tokenResponse, now time.Time) {
	c.auth.accessToken = token.AccessToken
	if token.RefreshToken != "" {
		c.auth.refreshToken = token.RefreshToken
	}
	if token.ExpiresIn > 0 {
		c.auth.expiresAt = now.Add(time.Duration(token.ExpiresIn) * time.Second)
	} else {
		c.auth.expiresAt = now.Add(12 * time.Hour)
	}
	c.auth.startEnabled = false
	if err := c.persistTokenStateLocked(); err != nil {
		fmt.Println("Failed writing Battle.net token file:", err)
	}
}

func (c *Client) persistTokenStateLocked() error {
	persisted := persistence.AuthTokenState{
		AccessToken:  c.auth.accessToken,
		RefreshToken: c.auth.refreshToken,
		StartEnabled: c.auth.startEnabled,
	}
	if !c.auth.expiresAt.IsZero() {
		persisted.ExpiresAt = c.auth.expiresAt.UTC().Format(time.RFC3339)
	}

	return persistence.SaveAuthTokenState(c.tokenFilePath, persisted)
}

func (c *Client) exchangeToken(values url.Values) (tokenResponse, error) {
	var token tokenResponse

	req, err := http.NewRequest("POST", oauthHost+"/token", strings.NewReader(values.Encode()))
	if err != nil {
		return token, err
	}

	req.SetBasicAuth(c.config.BattleNetClientID, c.config.BattleNetClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return token, err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return token, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return token, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, &token); err != nil {
		return token, err
	}

	if token.AccessToken == "" {
		return token, errors.New("empty access_token in battle.net response")
	}

	return token, nil
}

func (c *Client) Update(snapshot *model.Wuu2) {
	c.updateMu.Lock()
	defer c.updateMu.Unlock()

	if !c.hasConfig() {
		fmt.Println("Battle.net config missing. Skipping WoW update.")
		return
	}

	accessToken, err := c.ensureAccessToken()
	if err != nil {
		fmt.Println("Battle.net OAuth not ready. Head to " + c.config.Address + "/auth/battlenet/start to authorize.")
		return
	}

	summary, lastModified, err := c.fetchProtectedCharacter(accessToken)
	if errors.Is(err, errUnauthorized) {
		c.enableStart()
		c.clearAccessToken()
		var refreshErr error
		accessToken, refreshErr = c.ensureAccessToken()
		if refreshErr != nil {
			fmt.Println("Battle.net token refresh after 401 failed:", refreshErr)
			return
		}

		summary, lastModified, err = c.fetchProtectedCharacter(accessToken)
	}
	if err != nil {
		fmt.Println("Battle.net protected character request failed:", err)
		return
	}

	media, mediaErr := c.fetchCharacterMedia(accessToken, summary)
	if errors.Is(mediaErr, errUnauthorized) {
		c.enableStart()
		c.clearAccessToken()
		var refreshErr error
		accessToken, refreshErr = c.ensureAccessToken()
		if refreshErr != nil {
			fmt.Println("Battle.net token refresh after 401 failed on character-media:", refreshErr)
		} else {
			media, mediaErr = c.fetchCharacterMedia(accessToken, summary)
		}
	}
	if mediaErr != nil {
		fmt.Println("Battle.net character media request failed:", mediaErr)
	}

	now := time.Now().UTC()
	entry := model.Wow{
		LastCheck:    now.Format(time.RFC3339),
		LastModified: lastModified,
		Character:    nonEmpty(summary.Character.Name, c.config.BattleNetCharacter),
		Realm:        summary.Character.Realm.Name,
		Location:     formatWowLocation(summary),
		X:            summary.Position.X,
		Y:            summary.Position.Y,
		Z:            summary.Position.Z,
		Facing:       summary.Position.Facing,
	}
	entry.AvatarURL = assetValue(media.Assets, "avatar")
	entry.InsetURL = assetValue(media.Assets, "inset")
	entry.MainrawURL = assetValue(media.Assets, "main-raw")
	entry.ArmoryURL = c.buildWowArmoryURL(summary)
	entry.Online = hasWowCharacterMoved(c.wowMovementHistory, entry, now, c.config.UpdateIntervalMinutes)
	entry.LastOnline = resolveWowLastOnline(c.wowMovementHistory, entry)

	c.wowMovementHistory = append(c.wowMovementHistory, entry)
	c.wowMovementHistory = trimWowMovementHistory(c.wowMovementHistory, 512)

	snapshot.Wow = []model.Wow{entry}
}

func (c *Client) fetchProtectedCharacter(accessToken string) (protectedCharacterSummary, string, error) {
	var summary protectedCharacterSummary

	baseURI := strings.TrimRight(c.config.BattleNetRequestURI, "/")
	requestURI := fmt.Sprintf(
		"%s/profile/user/wow/protected-character/%s-%s",
		baseURI,
		c.config.BattleNetRealm,
		c.config.BattleNetCharacterID,
	)

	query := url.Values{}
	query.Set("namespace", fmt.Sprintf("profile-%s", strings.ToLower(c.config.BattleNetRegion)))
	if c.config.BattleNetLocale != "" {
		query.Set("locale", c.config.BattleNetLocale)
	}

	req, err := http.NewRequest("GET", requestURI+"?"+query.Encode(), nil)
	if err != nil {
		return summary, "", err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return summary, "", err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	lastModified := parseLastModified(resp.Header.Get("Last-Modified"))

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return summary, lastModified, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return summary, lastModified, errUnauthorized
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return summary, lastModified, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, &summary); err != nil {
		return summary, lastModified, err
	}

	return summary, lastModified, nil
}

func parseLastModified(raw string) string {
	lastModified := strings.TrimSpace(raw)
	if parsed, err := timeutil.ParseDateTimeString(lastModified); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return lastModified
}

func (c *Client) fetchCharacterMedia(accessToken string, summary protectedCharacterSummary) (characterMedia, error) {
	var media characterMedia

	realmSlug := strings.TrimSpace(summary.Character.Realm.Slug)
	if realmSlug == "" {
		return media, errors.New("missing realm slug in protected character response")
	}

	characterName := strings.ToLower(nonEmpty(summary.Character.Name, c.config.BattleNetCharacter))
	characterName = strings.TrimSpace(characterName)
	if characterName == "" {
		return media, errors.New("missing character name for character-media request")
	}

	baseURI := strings.TrimRight(c.config.BattleNetRequestURI, "/")
	requestURI := fmt.Sprintf(
		"%s/profile/wow/character/%s/%s/character-media",
		baseURI,
		url.PathEscape(realmSlug),
		url.PathEscape(characterName),
	)

	query := url.Values{}
	query.Set("namespace", fmt.Sprintf("profile-%s", strings.ToLower(c.config.BattleNetRegion)))
	if c.config.BattleNetLocale != "" {
		query.Set("locale", c.config.BattleNetLocale)
	}

	req, err := http.NewRequest("GET", requestURI+"?"+query.Encode(), nil)
	if err != nil {
		return media, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return media, err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return media, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return media, errUnauthorized
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return media, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, &media); err != nil {
		return media, err
	}

	return media, nil
}

func assetValue(assets []characterMediaAsset, key string) string {
	for _, asset := range assets {
		if strings.EqualFold(strings.TrimSpace(asset.Key), key) {
			return strings.TrimSpace(asset.Value)
		}
	}
	return ""
}

func (c *Client) buildWowArmoryURL(summary protectedCharacterSummary) string {
	realmSlug := strings.TrimSpace(summary.Character.Realm.Slug)
	characterName := strings.TrimSpace(summary.Character.Name)
	if realmSlug == "" || characterName == "" {
		return ""
	}

	region := strings.ToLower(strings.TrimSpace(c.config.BattleNetRegion))
	if region == "" {
		region = "eu"
	}

	locale := normalizeWowArmoryLocale(c.config.BattleNetLocale, region)

	return fmt.Sprintf(
		"https://worldofwarcraft.blizzard.com/%s/character/%s/%s/%s/",
		locale,
		url.PathEscape(region),
		url.PathEscape(strings.ToLower(realmSlug)),
		url.PathEscape(strings.ToLower(characterName)),
	)
}

func normalizeWowArmoryLocale(rawLocale string, region string) string {
	locale := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(rawLocale), "_", "-"))
	if locale != "" {
		return locale
	}

	switch strings.ToLower(strings.TrimSpace(region)) {
	case "eu":
		return "en-gb"
	case "kr":
		return "ko-kr"
	case "tw":
		return "zh-tw"
	default:
		return "en-us"
	}
}

func (c *Client) buildAuthorizeURL(state string) string {
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", c.config.BattleNetClientID)
	query.Set("scope", c.config.BattleNetScope)
	query.Set("redirect_uri", c.config.BattleNetRedirectURI)
	if state != "" {
		query.Set("state", state)
	}

	return oauthHost + "/authorize?" + query.Encode()
}

func hasWowCharacterMoved(history []model.Wow, current model.Wow, now time.Time, lookback time.Duration) bool {
	if len(history) == 0 {
		return false
	}

	referenceIndex := -1
	fallbackIndex := -1
	cutoff := now.Add(-lookback)

	for i := len(history) - 1; i >= 0; i-- {
		existing := history[i]
		if !sameWowCharacter(existing, current) {
			continue
		}

		if fallbackIndex == -1 {
			fallbackIndex = i
		}

		lastCheck, err := time.Parse(time.RFC3339, existing.LastCheck)
		if err != nil {
			continue
		}

		if !lastCheck.After(cutoff) {
			referenceIndex = i
			break
		}
	}

	if referenceIndex == -1 {
		referenceIndex = fallbackIndex
	}
	if referenceIndex == -1 {
		return false
	}

	ref := history[referenceIndex]
	return math.Abs(float64(current.X-ref.X)) > movementEpsilon ||
		math.Abs(float64(current.Y-ref.Y)) > movementEpsilon ||
		math.Abs(float64(current.Z-ref.Z)) > movementEpsilon
}

func sameWowCharacter(a model.Wow, b model.Wow) bool {
	return strings.EqualFold(strings.TrimSpace(a.Character), strings.TrimSpace(b.Character)) &&
		strings.EqualFold(strings.TrimSpace(a.Realm), strings.TrimSpace(b.Realm))
}

func resolveWowLastOnline(history []model.Wow, current model.Wow) string {
	if current.Online {
		return current.LastCheck
	}

	for i := len(history) - 1; i >= 0; i-- {
		existing := history[i]
		if !sameWowCharacter(existing, current) {
			continue
		}

		lastOnline := strings.TrimSpace(existing.LastOnline)
		if lastOnline != "" {
			return lastOnline
		}
		if existing.Online {
			return existing.LastCheck
		}
	}

	return ""
}

func formatWowLocation(summary protectedCharacterSummary) string {
	zone := strings.TrimSpace(summary.Position.Zone.Name)
	mapName := strings.TrimSpace(summary.Position.Map.Name)

	if zone != "" && mapName != "" {
		return zone + ", " + mapName
	}
	if zone != "" {
		return zone
	}
	return mapName
}

func (c *Client) hasConfig() bool {
	return c.config.BattleNetClientID != "" &&
		c.config.BattleNetClientSecret != "" &&
		c.config.BattleNetRequestURI != "" &&
		c.config.BattleNetRegion != "" &&
		c.config.BattleNetRealm != "" &&
		c.config.BattleNetCharacterID != "" &&
		c.config.BattleNetRedirectURI != "" &&
		c.config.BattleNetScope != ""
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func trimWowMovementHistory(history []model.Wow, maxItems int) []model.Wow {
	if maxItems <= 0 || len(history) <= maxItems {
		return history
	}
	return history[len(history)-maxItems:]
}
