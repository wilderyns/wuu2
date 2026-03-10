package battle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/endpoints"

	"wuu2/internal/config"
	"wuu2/internal/lib/oauthflow"
	"wuu2/internal/lib/persistence"
	"wuu2/internal/lib/timeutil"
	"wuu2/internal/model"
)

var errUnauthorized = errors.New("battlenet unauthorized")

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

type characterMedia struct {
	Assets []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"assets"`
}

type Client struct {
	config   config.Config
	oauth    *oauthflow.Manager
	updateMu sync.Mutex
}

// NewClient creates a Battle.net client and wires it to the shared OAuth flow manager.
func NewClient(cfg config.Config) *Client {
	tokenFilePath := ""
	if cfg.TokenPersistenceEnabled {
		tokenFilePath = persistence.TokenFilePathForDirectory(cfg.PersistenceDirectory, "battlenet")
	}

	oauthConfig := &oauth2.Config{
		ClientID:     cfg.BattleNetClientID,
		ClientSecret: cfg.BattleNetClientSecret,
		RedirectURL:  cfg.BattleNetRedirectURI,
		Scopes:       oauthflow.ParseScopes(cfg.BattleNetScope),
		Endpoint:     endpoints.Battlenet,
	}
	onPersistError := func(err error) {
		fmt.Println("Failed writing Battle.net token file:", err)
	}

	oauthManager := oauthflow.New(
		oauthConfig,
		16,
		onPersistError,
	)
	if tokenFilePath != "" {
		oauthManager = oauthflow.New(
			oauthConfig,
			16,
			onPersistError,
			tokenFilePath,
		)
	}

	client := &Client{
		config: cfg,
		oauth:  oauthManager,
	}

	return client
}

// LoadPersistedTokenState loads the persisted auth token state from disk and sets the client's auth state accordingly
func (c *Client) LoadPersistedTokenState() error {
	return c.oauth.LoadPersistedTokenState()
}

// AuthStartHandler starts the Battle.net OAuth flow.
// First uses hasConfig to ensure the user applied battle.net config is present.
// Then
func (c *Client) AuthStartHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !c.hasConfig() {
			http.Error(w, "Battle.net config missing", http.StatusBadRequest)
			return
		}

		authorizeURL, err := c.oauth.StartAuthorizationURL()
		if errors.Is(err, oauthflow.ErrStartDisabled) {
			http.Error(w, "Battle.net auth start is disabled until re-authentication is required", http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, "failed preparing OAuth start", http.StatusInternalServerError)
			fmt.Println("Battle.net OAuth start failed:", err)
			return
		}

		http.Redirect(w, r, authorizeURL, http.StatusFound)
	}
}

func (c *Client) AuthCallbackHandler(onAuthorizedRefresh func() error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code parameter", http.StatusBadRequest)
			return
		}

		err := c.oauth.ExchangeCode(r.Context(), code, r.URL.Query().Get("state"))
		if errors.Is(err, oauthflow.ErrInvalidState) {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}
		if errors.Is(err, oauthflow.ErrMissingCode) {
			http.Error(w, "missing code parameter", http.StatusBadRequest)
			return
		}
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

func (c *Client) enableStart() {
	c.oauth.EnableStart()
}

func (c *Client) clearAccessToken() {
	c.oauth.ClearAccessToken()
}

func (c *Client) ensureAccessToken() (string, error) {
	return c.oauth.EnsureAccessToken(context.Background())
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
	entry.AvatarURL = assetValue(media, "avatar")
	entry.InsetURL = assetValue(media, "inset")
	entry.MainrawURL = assetValue(media, "main-raw")
	entry.ArmoryURL = c.buildWowArmoryURL(summary)
	entry.Online = false
	entry.LastOnline = ""

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

func assetValue(media characterMedia, key string) string {
	for _, asset := range media.Assets {
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

func nonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
