package retroachievements

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"wuu2/internal/config"
	"wuu2/internal/model"
)

const siteURL = "https://retroachievements.org"
const userAgent = "wuu2/1.0 (+https://github.com/wilderyns/wuu2)"

var (
	httpClient          = http.DefaultClient
	userProfileURL      = siteURL + "/API/API_GetUserProfile.php"
	userSummaryURL      = siteURL + "/API/API_GetUserSummary.php"
	userGameProgressURL = siteURL + "/API/API_GetGameInfoAndUserProgress.php"
)

type userProfileResponse struct {
	User                string `json:"User"`
	ULID                string `json:"ULID"`
	UserPic             string `json:"UserPic"`
	RichPresenceMsg     string `json:"RichPresenceMsg"`
	LastGameID          int    `json:"LastGameID"`
	TotalPoints         int    `json:"TotalPoints"`
	TotalSoftcorePoints int    `json:"TotalSoftcorePoints"`
	TotalTruePoints     int    `json:"TotalTruePoints"`
}

type userSummaryResponse struct {
	Rank            int    `json:"Rank"`
	Status          string `json:"Status"`
	RichPresenceMsg string `json:"RichPresenceMsg"`
	RecentlyPlayed  []struct {
		GameID     int    `json:"GameID"`
		Title      string `json:"Title"`
		LastPlayed string `json:"LastPlayed"`
		ImageIcon  string `json:"ImageIcon"`
	} `json:"RecentlyPlayed"`
	LastGame struct {
		ID        int    `json:"ID"`
		Title     string `json:"Title"`
		ImageIcon string `json:"ImageIcon"`
	} `json:"LastGame"`
}

type userGameProgressResponse struct {
	ID                int    `json:"ID"`
	Title             string `json:"Title"`
	NumAchievements   int    `json:"NumAchievements"`
	NumAwardedToUser  int    `json:"NumAwardedToUser"`
	UserTotalPlaytime int    `json:"UserTotalPlaytime"`
	HighestAwardKind  string `json:"HighestAwardKind"`
}

func Update(cfg config.Config, snapshot *model.Wuu2) {
	if snapshot == nil {
		return
	}
	if !cfg.RetroAchievementsEnabled {
		snapshot.RetroAchievements = nil
		return
	}

	profile, err := fetchUserProfile(cfg)
	if err != nil {
		fmt.Println("RetroAchievements user profile request failed:", err)
		return
	}
	if profile == nil {
		snapshot.RetroAchievements = nil
		return
	}

	existing := firstRetroAchievementsEntry(snapshot.RetroAchievements)
	entry := model.RetroAchievements{
		HardcorePoints:   profile.TotalPoints,
		SoftcorePoints:   profile.TotalSoftcorePoints,
		RetroPoints:      profile.TotalTruePoints,
		LastGameID:       profile.LastGameID,
		RichPresence:     strings.TrimSpace(profile.RichPresenceMsg),
		CurrentlyInGame:  isCurrentlyInGame("", profile.RichPresenceMsg, profile.LastGameID),
		ProfileAvatarURL: buildAssetURL(profile.UserPic),
	}

	if existing != nil {
		if existing.SiteRank > 0 {
			entry.SiteRank = existing.SiteRank
		}
		if existing.LastGameID == entry.LastGameID {
			entry.LastGameTitle = existing.LastGameTitle
		}
		if existing.LastChange != "" {
			entry.LastChange = existing.LastChange
		}
		if existing.LastGameID == entry.LastGameID {
			entry.EarnedAchievements = existing.EarnedAchievements
			entry.TotalAchievements = existing.TotalAchievements
			entry.Beaten = existing.Beaten
			entry.Mastered = existing.Mastered
			entry.PlaytimeSeconds = existing.PlaytimeSeconds
		}
	}

	summary, err := fetchUserSummary(cfg)
	if err != nil {
		fmt.Println("RetroAchievements user summary request failed:", err)
	} else if summary != nil {
		if summary.Rank > 0 {
			entry.SiteRank = summary.Rank
		}
		if summary.LastGame.ID > 0 {
			entry.LastGameID = summary.LastGame.ID
		}
		if title := strings.TrimSpace(summary.LastGame.Title); title != "" {
			entry.LastGameTitle = title
		}
		if icon := buildAssetURL(summary.LastGame.ImageIcon); icon != "" {
			entry.GameIconURL = icon
		}
		if len(summary.RecentlyPlayed) > 0 {
			if title := strings.TrimSpace(summary.RecentlyPlayed[0].Title); title != "" {
				entry.LastGameTitle = title
			}
			if summary.RecentlyPlayed[0].GameID > 0 {
				entry.LastGameID = summary.RecentlyPlayed[0].GameID
			}
			if icon := buildAssetURL(summary.RecentlyPlayed[0].ImageIcon); icon != "" {
				entry.GameIconURL = icon
			}
			if lastChange := normalizeRetroAchievementsTime(summary.RecentlyPlayed[0].LastPlayed); lastChange != "" {
				entry.LastChange = lastChange
			}
		}
		if richPresence := strings.TrimSpace(summary.RichPresenceMsg); richPresence != "" {
			entry.RichPresence = richPresence
		}
		entry.CurrentlyInGame = isCurrentlyInGame(summary.Status, entry.RichPresence, entry.LastGameID)
	}

	if entry.LastGameID > 0 {
		progress, err := fetchUserGameProgress(cfg, entry.LastGameID)
		if err != nil {
			fmt.Println("RetroAchievements user game progress request failed:", err)
		} else if progress != nil {
			entry.EarnedAchievements = progress.NumAwardedToUser
			entry.TotalAchievements = progress.NumAchievements
			entry.PlaytimeSeconds = progress.UserTotalPlaytime
			entry.Mastered = isMastered(progress.HighestAwardKind)
			entry.Beaten = isBeaten(progress.HighestAwardKind)
			if title := strings.TrimSpace(progress.Title); title != "" {
				entry.LastGameTitle = title
			}
		}
	}

	snapshot.RetroAchievements = []model.RetroAchievements{entry}
}

func fetchUserProfile(cfg config.Config) (*userProfileResponse, error) {
	params := url.Values{}
	params.Set("y", strings.TrimSpace(cfg.RetroAchievementsKey))
	params.Set("u", strings.TrimSpace(cfg.RetroAchievementsUser))

	var profile userProfileResponse
	if err := doRequest(buildRequestURL(userProfileURL, params), &profile); err != nil {
		return nil, err
	}

	return &profile, nil
}

func fetchUserSummary(cfg config.Config) (*userSummaryResponse, error) {
	params := url.Values{}
	params.Set("y", strings.TrimSpace(cfg.RetroAchievementsKey))
	params.Set("u", strings.TrimSpace(cfg.RetroAchievementsUser))
	params.Set("g", "1")
	params.Set("a", "0")

	var summary userSummaryResponse
	if err := doRequest(buildRequestURL(userSummaryURL, params), &summary); err != nil {
		return nil, err
	}

	return &summary, nil
}

func fetchUserGameProgress(cfg config.Config, gameID int) (*userGameProgressResponse, error) {
	params := url.Values{}
	params.Set("y", strings.TrimSpace(cfg.RetroAchievementsKey))
	params.Set("u", strings.TrimSpace(cfg.RetroAchievementsUser))
	params.Set("g", fmt.Sprintf("%d", gameID))
	params.Set("a", "1")

	var progress userGameProgressResponse
	if err := doRequest(buildRequestURL(userGameProgressURL, params), &progress); err != nil {
		return nil, err
	}

	return &progress, nil
}

func doRequest(requestURL string, target any) error {
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func(body io.ReadCloser) {
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, target); err != nil {
		return err
	}

	return nil
}

func buildRequestURL(base string, params url.Values) string {
	if strings.Contains(base, "?") {
		return base + "&" + params.Encode()
	}

	return base + "?" + params.Encode()
}

func buildAssetURL(assetPath string) string {
	assetPath = strings.TrimSpace(assetPath)
	if assetPath == "" {
		return ""
	}
	if strings.HasPrefix(assetPath, "http://") || strings.HasPrefix(assetPath, "https://") {
		return assetPath
	}

	return siteURL + "/" + strings.TrimPrefix(assetPath, "/")
}

func isCurrentlyInGame(status string, richPresence string, lastGameID int) bool {
	if strings.TrimSpace(richPresence) == "" || lastGameID == 0 {
		return false
	}

	if strings.EqualFold(strings.TrimSpace(status), "offline") {
		return false
	}

	return true
}

func isBeaten(highestAwardKind string) bool {
	switch strings.ToLower(strings.TrimSpace(highestAwardKind)) {
	case "beaten-softcore", "beaten-hardcore", "completed", "mastered":
		return true
	default:
		return false
	}
}

func isMastered(highestAwardKind string) bool {
	return strings.EqualFold(strings.TrimSpace(highestAwardKind), "mastered")
}

func firstRetroAchievementsEntry(entries []model.RetroAchievements) *model.RetroAchievements {
	if len(entries) == 0 {
		return nil
	}

	entry := entries[0]
	return &entry
}

func normalizeRetroAchievementsTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	parsed, err := time.Parse("2006-01-02 15:04:05", value)
	if err != nil {
		return value
	}

	return parsed.UTC().Format(time.RFC3339)
}
