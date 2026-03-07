package main

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
)

const (
	battleNetOAuthHost       = "https://oauth.battle.net"
	battleNetMovementEpsilon = 0.001
)

var errBattleNetUnauthorized = errors.New("battlenet unauthorized")

type battleNetTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type battleNetProtectedCharacterSummary struct {
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

type battleNetCharacterMediaAsset struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type battleNetCharacterMedia struct {
	Assets []battleNetCharacterMediaAsset `json:"assets"`
}

type battleNetAuthState struct {
	mu           sync.Mutex
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	authCode     string
	state        string
	startEnabled bool
}

var battleAuth = battleNetAuthState{
	startEnabled: true,
}
var wowMovementHistory []Wow

func battleNetAuthStartHandler(config Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !hasBattleNetConfig(config) {
			http.Error(w, "Battle.net config missing", http.StatusBadRequest)
			return
		}

		if !battleAuth.isStartEnabled() {
			http.Error(w, "Battle.net auth start is disabled until re-authentication is required", http.StatusConflict)
			return
		}

		state := randomHex(16)
		battleAuth.mu.Lock()
		battleAuth.state = state
		battleAuth.mu.Unlock()

		http.Redirect(w, r, buildBattleNetAuthorizeURL(config, state), http.StatusFound)
	}
}

func battleNetAuthCallbackHandler(config Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code parameter", http.StatusBadRequest)
			return
		}

		state := r.URL.Query().Get("state")
		if !battleAuth.validateState(state) {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			return
		}

		battleAuth.mu.Lock()
		battleAuth.authCode = code
		battleAuth.mu.Unlock()

		_, err := battleAuth.ensureAccessToken(config)
		if err != nil {
			http.Error(w, "failed exchanging OAuth code", http.StatusInternalServerError)
			fmt.Println("Battle.net OAuth callback exchange failed:", err)
			return
		}

		// Refresh WoW snapshot immediately after successful OAuth.
		getBattle(config, &WUU2)

		_, _ = w.Write([]byte("Battle.net auth complete. You can close this tab."))
	}
}

func (s *battleNetAuthState) validateState(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.state == "" {
		return true
	}

	if state == "" || state != s.state {
		return false
	}

	s.state = ""
	return true
}

func (s *battleNetAuthState) clearAccessToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accessToken = ""
	s.expiresAt = time.Time{}
}

func (s *battleNetAuthState) isStartEnabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startEnabled
}

func (s *battleNetAuthState) enableStart() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.startEnabled = true
}

func (s *battleNetAuthState) ensureAccessToken(config Config) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	if s.accessToken != "" && now.Before(s.expiresAt.Add(-1*time.Minute)) {
		return s.accessToken, nil
	}

	if s.refreshToken != "" {
		token, err := exchangeBattleNetToken(config, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {s.refreshToken},
		})
		if err == nil {
			s.applyToken(token, now)
			return s.accessToken, nil
		}
		fmt.Println("Battle.net refresh failed, falling back to auth code:", err)
	}

	if s.authCode == "" {
		s.startEnabled = true
		return "", fmt.Errorf("battlenet auth required: open %s", buildBattleNetAuthorizeURL(config, ""))
	}

	token, err := exchangeBattleNetToken(config, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {s.authCode},
		"redirect_uri": {config.BattleNetRedirectURI},
	})
	if err != nil {
		s.startEnabled = true
		return "", err
	}

	// Authorization codes are single-use.
	s.authCode = ""
	s.applyToken(token, now)
	return s.accessToken, nil
}

func (s *battleNetAuthState) applyToken(token battleNetTokenResponse, now time.Time) {
	s.accessToken = token.AccessToken
	if token.RefreshToken != "" {
		s.refreshToken = token.RefreshToken
	}
	if token.ExpiresIn > 0 {
		s.expiresAt = now.Add(time.Duration(token.ExpiresIn) * time.Second)
	} else {
		s.expiresAt = now.Add(12 * time.Hour)
	}
	s.startEnabled = false
}

func exchangeBattleNetToken(config Config, values url.Values) (battleNetTokenResponse, error) {
	var token battleNetTokenResponse

	req, err := http.NewRequest("POST", battleNetOAuthHost+"/token", strings.NewReader(values.Encode()))
	if err != nil {
		return token, err
	}

	req.SetBasicAuth(config.BattleNetClientID, config.BattleNetClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return token, err
	}
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
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

func getBattle(config Config, wuu2 *Wuu2) {
	if !hasBattleNetConfig(config) {
		fmt.Println("Battle.net config missing. Skipping WoW update.")
		return
	}

	accessToken, err := battleAuth.ensureAccessToken(config)
	if err != nil {
		fmt.Println("Battle.net OAuth not ready. Head to " + config.Address + "/auth/battlenet/start to authorize.")
		return
	}

	summary, lastModified, err := fetchBattleNetProtectedCharacter(config, accessToken)
	if errors.Is(err, errBattleNetUnauthorized) {
		battleAuth.enableStart()
		battleAuth.clearAccessToken()
		var refreshErr error
		accessToken, refreshErr = battleAuth.ensureAccessToken(config)
		if refreshErr != nil {
			fmt.Println("Battle.net token refresh after 401 failed:", refreshErr)
			return
		}

		summary, lastModified, err = fetchBattleNetProtectedCharacter(config, accessToken)
	}
	if err != nil {
		fmt.Println("Battle.net protected character request failed:", err)
		return
	}

	media, mediaErr := fetchBattleNetCharacterMedia(config, accessToken, summary)
	if errors.Is(mediaErr, errBattleNetUnauthorized) {
		battleAuth.enableStart()
		battleAuth.clearAccessToken()
		var refreshErr error
		accessToken, refreshErr = battleAuth.ensureAccessToken(config)
		if refreshErr != nil {
			fmt.Println("Battle.net token refresh after 401 failed on character-media:", refreshErr)
		} else {
			media, mediaErr = fetchBattleNetCharacterMedia(config, accessToken, summary)
		}
	}
	if mediaErr != nil {
		fmt.Println("Battle.net character media request failed:", mediaErr)
	}

	now := time.Now().UTC()
	entry := Wow{
		LastCheck:    now.Format(time.RFC3339),
		LastModified: lastModified,
		Character:    nonEmpty(summary.Character.Name, config.BattleNetCharacter),
		Realm:        summary.Character.Realm.Name,
		Location:     formatWowLocation(summary),
		X:            summary.Position.X,
		Y:            summary.Position.Y,
		Z:            summary.Position.Z,
		Facing:       summary.Position.Facing,
	}
	entry.AvatarURL = battleNetAssetValue(media.Assets, "avatar")
	entry.InsetURL = battleNetAssetValue(media.Assets, "inset")
	entry.MainrawURL = battleNetAssetValue(media.Assets, "main-raw")
	entry.ArmoryURL = buildWowArmoryURL(config, summary)
	entry.Online = hasWowCharacterMoved(wowMovementHistory, entry, now, config.UpdateIntervalMinutes)
	entry.LastOnline = resolveWowLastOnline(wowMovementHistory, entry)

	wowMovementHistory = append(wowMovementHistory, entry)
	wowMovementHistory = trimWowMovementHistory(wowMovementHistory, 512)

	// Public payload should contain only the latest WoW snapshot.
	wuu2.Wow = []Wow{entry}
}

func fetchBattleNetProtectedCharacter(config Config, accessToken string) (battleNetProtectedCharacterSummary, string, error) {
	var summary battleNetProtectedCharacterSummary

	baseURI := strings.TrimRight(config.BattleNetRequestURI, "/")
	requestURI := fmt.Sprintf(
		"%s/profile/user/wow/protected-character/%s-%s",
		baseURI,
		config.BattleNetRealm,
		config.BattleNetCharacterID,
	)

	query := url.Values{}
	query.Set("namespace", fmt.Sprintf("profile-%s", strings.ToLower(config.BattleNetRegion)))
	if config.BattleNetLocale != "" {
		query.Set("locale", config.BattleNetLocale)
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
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	lastModified := parseBattleNetLastModified(resp.Header.Get("Last-Modified"))

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return summary, lastModified, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return summary, lastModified, errBattleNetUnauthorized
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return summary, lastModified, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, &summary); err != nil {
		return summary, lastModified, err
	}

	return summary, lastModified, nil
}

func parseBattleNetLastModified(raw string) string {
	lastModified := strings.TrimSpace(raw)
	if parsed, err := parseDateTimeString(lastModified); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return lastModified
}

func fetchBattleNetCharacterMedia(config Config, accessToken string, summary battleNetProtectedCharacterSummary) (battleNetCharacterMedia, error) {
	var media battleNetCharacterMedia

	realmSlug := strings.TrimSpace(summary.Character.Realm.Slug)
	if realmSlug == "" {
		return media, errors.New("missing realm slug in protected character response")
	}

	characterName := strings.ToLower(nonEmpty(summary.Character.Name, config.BattleNetCharacter))
	characterName = strings.TrimSpace(characterName)
	if characterName == "" {
		return media, errors.New("missing character name for character-media request")
	}

	baseURI := strings.TrimRight(config.BattleNetRequestURI, "/")
	requestURI := fmt.Sprintf(
		"%s/profile/wow/character/%s/%s/character-media",
		baseURI,
		url.PathEscape(realmSlug),
		url.PathEscape(characterName),
	)

	query := url.Values{}
	query.Set("namespace", fmt.Sprintf("profile-%s", strings.ToLower(config.BattleNetRegion)))
	if config.BattleNetLocale != "" {
		query.Set("locale", config.BattleNetLocale)
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
	defer func(Body io.ReadCloser) {
		_ = Body.Close()
	}(resp.Body)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return media, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return media, errBattleNetUnauthorized
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return media, fmt.Errorf("status %d: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, &media); err != nil {
		return media, err
	}

	return media, nil
}

func battleNetAssetValue(assets []battleNetCharacterMediaAsset, key string) string {
	for _, asset := range assets {
		if strings.EqualFold(strings.TrimSpace(asset.Key), key) {
			return strings.TrimSpace(asset.Value)
		}
	}
	return ""
}

func buildWowArmoryURL(config Config, summary battleNetProtectedCharacterSummary) string {
	realmSlug := strings.TrimSpace(summary.Character.Realm.Slug)
	characterName := strings.TrimSpace(summary.Character.Name)
	if realmSlug == "" || characterName == "" {
		return ""
	}

	region := strings.ToLower(strings.TrimSpace(config.BattleNetRegion))
	if region == "" {
		region = "eu"
	}

	locale := normalizeWowArmoryLocale(config.BattleNetLocale, region)

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

func buildBattleNetAuthorizeURL(config Config, state string) string {
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", config.BattleNetClientID)
	query.Set("scope", config.BattleNetScope)
	query.Set("redirect_uri", config.BattleNetRedirectURI)
	if state != "" {
		query.Set("state", state)
	}

	return battleNetOAuthHost + "/authorize?" + query.Encode()
}

func hasWowCharacterMoved(history []Wow, current Wow, now time.Time, lookback time.Duration) bool {
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
	return math.Abs(float64(current.X-ref.X)) > battleNetMovementEpsilon ||
		math.Abs(float64(current.Y-ref.Y)) > battleNetMovementEpsilon ||
		math.Abs(float64(current.Z-ref.Z)) > battleNetMovementEpsilon
}

func sameWowCharacter(a Wow, b Wow) bool {
	return strings.EqualFold(strings.TrimSpace(a.Character), strings.TrimSpace(b.Character)) &&
		strings.EqualFold(strings.TrimSpace(a.Realm), strings.TrimSpace(b.Realm))
}

func resolveWowLastOnline(history []Wow, current Wow) string {
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

func formatWowLocation(summary battleNetProtectedCharacterSummary) string {
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

func hasBattleNetConfig(config Config) bool {
	return config.BattleNetClientID != "" &&
		config.BattleNetClientSecret != "" &&
		config.BattleNetRequestURI != "" &&
		config.BattleNetRegion != "" &&
		config.BattleNetRealm != "" &&
		config.BattleNetCharacterID != "" &&
		config.BattleNetRedirectURI != "" &&
		config.BattleNetScope != ""
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

func trimWowMovementHistory(history []Wow, maxItems int) []Wow {
	if maxItems <= 0 || len(history) <= maxItems {
		return history
	}
	return history[len(history)-maxItems:]
}
