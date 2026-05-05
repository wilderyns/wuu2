package steam

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wuu2/internal/config"
	"wuu2/internal/model"
)

func TestUpdateSetsCurrentGameDetails(t *testing.T) {
	t.Helper()

	var ownedGamesRequested bool
	var achievementsRequested bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/summary":
			if got := r.URL.Query().Get("steamids"); got != "76561198000000000" {
				t.Fatalf("unexpected steamids query: %q", got)
			}
			_, _ = w.Write([]byte(`{"response":{"players":[{"steamid":"76561198000000000","avatarfull":"https://cdn.example/avatar.jpg","gameid":"620","gameextrainfo":"Portal 2","lastlogoff":1710000000}]}}`))
		case "/owned":
			ownedGamesRequested = true
			if got := r.URL.Query().Get("appids_filter[0]"); got != "620" {
				t.Fatalf("unexpected appids_filter query: %q", got)
			}
			_, _ = w.Write([]byte(`{"response":{"game_count":1,"games":[{"appid":620,"name":"Portal 2","img_icon_url":"iconhash620","playtime_forever":570}]}}`))
		case "/achievements":
			achievementsRequested = true
			if got := r.URL.Query().Get("appid"); got != "620" {
				t.Fatalf("unexpected achievements appid query: %q", got)
			}
			_, _ = w.Write([]byte(`{"playerstats":{"gameName":"Portal 2","success":true,"achievements":[{"achieved":1},{"achieved":0},{"achieved":1}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubSteamAPI(server.URL+"/summary", server.URL+"/owned", server.URL+"/achievements")
	defer restore()

	snapshot := model.Wuu2{}
	Update(config.Config{
		SteamEnabled:   true,
		SteamWebAPIKey: "key",
		SteamID:        "76561198000000000",
	}, &snapshot)

	if !ownedGamesRequested {
		t.Fatal("expected owned games request for active game")
	}
	if !achievementsRequested {
		t.Fatal("expected achievements request for active game")
	}
	if len(snapshot.Steam) != 1 {
		t.Fatalf("expected 1 steam entry, got %d", len(snapshot.Steam))
	}

	entry := snapshot.Steam[0]
	if !entry.CurrentlyInGame {
		t.Fatal("expected user to be marked in game")
	}
	if entry.GameName != "Portal 2" {
		t.Fatalf("unexpected game name: %q", entry.GameName)
	}
	if entry.GameURL != "https://store.steampowered.com/app/620/" {
		t.Fatalf("unexpected game url: %q", entry.GameURL)
	}
	if entry.GameIconURL != "https://media.steampowered.com/steamcommunity/public/images/apps/620/iconhash620.jpg" {
		t.Fatalf("unexpected game icon url: %q", entry.GameIconURL)
	}
	if entry.ProfileAvatarURL != "https://cdn.example/avatar.jpg" {
		t.Fatalf("unexpected avatar url: %q", entry.ProfileAvatarURL)
	}
	if entry.HoursPlayed != 9 {
		t.Fatalf("unexpected hours played: %d", entry.HoursPlayed)
	}
	if entry.EarnedAchievements != 2 || entry.TotalAchievements != 3 {
		t.Fatalf("unexpected achievement counts: %d/%d", entry.EarnedAchievements, entry.TotalAchievements)
	}
	if _, err := time.Parse(time.RFC3339, entry.LastChange); err != nil {
		t.Fatalf("unexpected last change format: %v", err)
	}
}

func TestUpdatePreservesLastChangeWhenGameIsUnchanged(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/summary":
			_, _ = w.Write([]byte(`{"response":{"players":[{"steamid":"76561198000000000","avatarfull":"https://cdn.example/avatar.jpg","gameid":"620","gameextrainfo":"Portal 2","lastlogoff":1710000000}]}}`))
		case "/owned":
			_, _ = w.Write([]byte(`{"response":{"game_count":1,"games":[{"appid":620,"name":"Portal 2","img_icon_url":"iconhash620","playtime_forever":630}]}}`))
		case "/achievements":
			_, _ = w.Write([]byte(`{"playerstats":{"gameName":"Portal 2","success":true,"achievements":[{"achieved":1},{"achieved":1},{"achieved":0},{"achieved":0}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubSteamAPI(server.URL+"/summary", server.URL+"/owned", server.URL+"/achievements")
	defer restore()

	snapshot := model.Wuu2{
		Steam: []model.Steam{{
			LastChange:       "2026-05-01T12:00:00Z",
			GameName:         "Portal 2",
			GameURL:          "https://store.steampowered.com/app/620/",
			ProfileAvatarURL: "https://cdn.example/old-avatar.jpg",
			HoursPlayed:      8,
		}},
	}

	Update(config.Config{
		SteamEnabled:   true,
		SteamWebAPIKey: "key",
		SteamID:        "76561198000000000",
	}, &snapshot)

	entry := snapshot.Steam[0]
	if !entry.CurrentlyInGame {
		t.Fatal("expected user to be marked in game")
	}
	if entry.LastChange != "2026-05-01T12:00:00Z" {
		t.Fatalf("expected last change to be preserved, got %q", entry.LastChange)
	}
	if entry.HoursPlayed != 10 {
		t.Fatalf("expected hours played to refresh, got %d", entry.HoursPlayed)
	}
	if entry.EarnedAchievements != 2 || entry.TotalAchievements != 4 {
		t.Fatalf("unexpected achievement counts: %d/%d", entry.EarnedAchievements, entry.TotalAchievements)
	}
	if entry.ProfileAvatarURL != "https://cdn.example/avatar.jpg" {
		t.Fatalf("expected avatar url to refresh, got %q", entry.ProfileAvatarURL)
	}
	if entry.GameIconURL != "https://media.steampowered.com/steamcommunity/public/images/apps/620/iconhash620.jpg" {
		t.Fatalf("unexpected game icon url: %q", entry.GameIconURL)
	}
}

func TestUpdateUsesLastLogoffWhenOffline(t *testing.T) {
	t.Helper()

	var ownedGamesRequested bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/summary":
			_, _ = w.Write([]byte(`{"response":{"players":[{"steamid":"76561198000000000","avatarfull":"https://cdn.example/avatar.jpg","lastlogoff":1710000000}]}}`))
		case "/owned":
			ownedGamesRequested = true
			if got := r.URL.Query().Get("appids_filter[0]"); got != "" {
				t.Fatalf("did not expect filtered owned games query, got %q", got)
			}
			_, _ = w.Write([]byte(`{"response":{"game_count":3,"games":[{"appid":620,"name":"Portal 2","img_icon_url":"iconhash620","playtime_forever":570,"rtime_last_played":1710000000},{"appid":400,"name":"Portal","img_icon_url":"iconhash400","playtime_forever":180,"rtime_last_played":1710100000},{"appid":500,"name":"Left 4 Dead","img_icon_url":"iconhash500","playtime_forever":60,"rtime_last_played":1700000000}]}}`))
		case "/achievements":
			if got := r.URL.Query().Get("appid"); got != "400" {
				t.Fatalf("unexpected achievements appid query: %q", got)
			}
			_, _ = w.Write([]byte(`{"playerstats":{"gameName":"Portal","success":true,"achievements":[{"achieved":1},{"achieved":0}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubSteamAPI(server.URL+"/summary", server.URL+"/owned", server.URL+"/achievements")
	defer restore()

	snapshot := model.Wuu2{}
	Update(config.Config{
		SteamEnabled:   true,
		SteamWebAPIKey: "key",
		SteamID:        "76561198000000000",
	}, &snapshot)

	if !ownedGamesRequested {
		t.Fatal("expected owned games request for most recently played game")
	}
	if len(snapshot.Steam) != 1 {
		t.Fatalf("expected 1 steam entry, got %d", len(snapshot.Steam))
	}

	entry := snapshot.Steam[0]
	if entry.CurrentlyInGame {
		t.Fatal("expected user to be marked out of game")
	}
	if entry.GameName != "Portal" {
		t.Fatalf("unexpected game name: %q", entry.GameName)
	}
	if entry.GameURL != "https://store.steampowered.com/app/400/" {
		t.Fatalf("unexpected game url: %q", entry.GameURL)
	}
	if entry.GameIconURL != "https://media.steampowered.com/steamcommunity/public/images/apps/400/iconhash400.jpg" {
		t.Fatalf("unexpected game icon url: %q", entry.GameIconURL)
	}
	if entry.HoursPlayed != 3 {
		t.Fatalf("expected updated hours played, got %d", entry.HoursPlayed)
	}
	if entry.EarnedAchievements != 1 || entry.TotalAchievements != 2 {
		t.Fatalf("unexpected achievement counts: %d/%d", entry.EarnedAchievements, entry.TotalAchievements)
	}
	if entry.LastChange != "2024-03-10T19:46:40Z" {
		t.Fatalf("unexpected last change: %q", entry.LastChange)
	}
}

func TestUpdateRefreshesLastLogoffAcrossOfflineSnapshots(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/summary":
			_, _ = w.Write([]byte(`{"response":{"players":[{"steamid":"76561198000000000","avatarfull":"https://cdn.example/avatar.jpg","lastlogoff":1710003600}]}}`))
		case "/owned":
			_, _ = w.Write([]byte(`{"response":{"game_count":1,"games":[{"appid":400,"name":"","playtime_forever":180,"rtime_last_played":0}]}}`))
		case "/achievements":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubSteamAPI(server.URL+"/summary", server.URL+"/owned", server.URL+"/achievements")
	defer restore()

	snapshot := model.Wuu2{
		Steam: []model.Steam{{
			LastChange:       "2024-03-09T16:00:00Z",
			ProfileAvatarURL: "https://cdn.example/old-avatar.jpg",
		}},
	}

	Update(config.Config{
		SteamEnabled:   true,
		SteamWebAPIKey: "key",
		SteamID:        "76561198000000000",
	}, &snapshot)

	entry := snapshot.Steam[0]
	if entry.CurrentlyInGame {
		t.Fatal("expected user to be marked out of game")
	}
	if entry.LastChange != "2024-03-09T17:00:00Z" {
		t.Fatalf("expected updated last logoff, got %q", entry.LastChange)
	}
}

func stubSteamAPI(summaryURL string, gamesURL string, achievementsURL string) func() {
	previousSummaryURL := playerSummariesURL
	previousOwnedGamesURL := ownedGamesURL
	previousAchievementsURL := playerAchievementsURL
	previousHTTPClient := httpClient

	playerSummariesURL = summaryURL
	ownedGamesURL = gamesURL
	playerAchievementsURL = achievementsURL
	httpClient = http.DefaultClient

	return func() {
		playerSummariesURL = previousSummaryURL
		ownedGamesURL = previousOwnedGamesURL
		playerAchievementsURL = previousAchievementsURL
		httpClient = previousHTTPClient
	}
}
