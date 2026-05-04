package steam

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"wuu2/internal/config"
	"wuu2/internal/model"
)

var (
	httpClient         = http.DefaultClient
	playerSummariesURL = "https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v0002/"
	ownedGamesURL      = "https://api.steampowered.com/IPlayerService/GetOwnedGames/v0001/"
)

type playerSummariesResponse struct {
	Response struct {
		Players []playerSummary `json:"players"`
	} `json:"response"`
}

type playerSummary struct {
	SteamID       string `json:"steamid"`
	ProfileURL    string `json:"profileurl"`
	AvatarFull    string `json:"avatarfull"`
	GameID        string `json:"gameid"`
	GameExtraInfo string `json:"gameextrainfo"`
	LastLogoff    int64  `json:"lastlogoff"`
}

type ownedGamesResponse struct {
	Response struct {
		GameCount int         `json:"game_count"`
		Games     []ownedGame `json:"games"`
	} `json:"response"`
}

type ownedGame struct {
	AppID           int    `json:"appid"`
	Name            string `json:"name"`
	PlaytimeForever int    `json:"playtime_forever"`
}

func Update(cfg config.Config, snapshot *model.Wuu2) {
	if snapshot == nil {
		return
	}
	if !cfg.SteamEnabled {
		snapshot.Steam = nil
		return
	}

	summary, err := fetchPlayerSummary(cfg)
	if err != nil {
		fmt.Println("Steam player summary request failed:", err)
		return
	}
	if summary == nil {
		snapshot.Steam = nil
		return
	}

	entry := model.Steam{
		GameName:         strings.TrimSpace(summary.GameExtraInfo),
		GameURL:          buildGameURL(summary.GameID),
		ProfileAvatarURL: strings.TrimSpace(summary.AvatarFull),
	}

	if summary.GameID != "" {
		hoursPlayed, err := fetchHoursPlayed(cfg, summary.GameID)
		if err != nil {
			fmt.Println("Steam owned games request failed:", err)
		} else {
			entry.HoursPlayed = hoursPlayed
		}
	}

	entry.LastChange = resolveLastChange(firstSteamEntry(snapshot.Steam), entry, *summary, time.Now().UTC())
	snapshot.Steam = []model.Steam{entry}
}

func fetchPlayerSummary(cfg config.Config) (*playerSummary, error) {
	query := url.Values{}
	query.Set("key", strings.TrimSpace(cfg.SteamWebAPIKey))
	query.Set("steamids", strings.TrimSpace(cfg.SteamID))

	requestURL := playerSummariesURL
	if strings.Contains(requestURL, "?") {
		requestURL += "&" + query.Encode()
	} else {
		requestURL += "?" + query.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var parsed playerSummariesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Response.Players) == 0 {
		return nil, nil
	}

	player := parsed.Response.Players[0]
	return &player, nil
}

func fetchHoursPlayed(cfg config.Config, gameID string) (int, error) {
	query := url.Values{}
	query.Set("key", strings.TrimSpace(cfg.SteamWebAPIKey))
	query.Set("steamid", strings.TrimSpace(cfg.SteamID))
	query.Set("include_appinfo", "1")
	query.Set("include_played_free_games", "1")
	query.Set("appids_filter[0]", strings.TrimSpace(gameID))

	requestURL := ownedGamesURL
	if strings.Contains(requestURL, "?") {
		requestURL += "&" + query.Encode()
	} else {
		requestURL += "?" + query.Encode()
	}

	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return 0, err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	var parsed ownedGamesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	for _, game := range parsed.Response.Games {
		if strconv.Itoa(game.AppID) == strings.TrimSpace(gameID) {
			return game.PlaytimeForever / 60, nil
		}
	}

	return 0, nil
}

func buildGameURL(gameID string) string {
	gameID = strings.TrimSpace(gameID)
	if gameID == "" {
		return ""
	}

	return "https://store.steampowered.com/app/" + url.PathEscape(gameID) + "/"
}

func resolveLastChange(existing *model.Steam, current model.Steam, summary playerSummary, now time.Time) string {
	if current.GameName == "" && summary.LastLogoff > 0 {
		return time.Unix(summary.LastLogoff, 0).UTC().Format(time.RFC3339)
	}

	if existing != nil && samePresence(*existing, current) {
		lastChange := strings.TrimSpace(existing.LastChange)
		if lastChange != "" {
			return lastChange
		}
	}

	return now.Format(time.RFC3339)
}

func samePresence(existing model.Steam, current model.Steam) bool {
	return strings.EqualFold(strings.TrimSpace(existing.GameName), strings.TrimSpace(current.GameName)) &&
		strings.EqualFold(strings.TrimSpace(existing.GameURL), strings.TrimSpace(current.GameURL))
}

func firstSteamEntry(entries []model.Steam) *model.Steam {
	if len(entries) == 0 {
		return nil
	}

	entry := entries[0]
	return &entry
}
