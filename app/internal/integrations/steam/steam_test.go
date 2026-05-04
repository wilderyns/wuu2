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
			_, _ = w.Write([]byte(`{"response":{"game_count":1,"games":[{"appid":620,"name":"Portal 2","playtime_forever":570}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubSteamAPI(server.URL+"/summary", server.URL+"/owned")
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
	if len(snapshot.Steam) != 1 {
		t.Fatalf("expected 1 steam entry, got %d", len(snapshot.Steam))
	}

	entry := snapshot.Steam[0]
	if entry.GameName != "Portal 2" {
		t.Fatalf("unexpected game name: %q", entry.GameName)
	}
	if entry.GameURL != "https://store.steampowered.com/app/620/" {
		t.Fatalf("unexpected game url: %q", entry.GameURL)
	}
	if entry.ProfileAvatarURL != "https://cdn.example/avatar.jpg" {
		t.Fatalf("unexpected avatar url: %q", entry.ProfileAvatarURL)
	}
	if entry.HoursPlayed != 9 {
		t.Fatalf("unexpected hours played: %d", entry.HoursPlayed)
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
			_, _ = w.Write([]byte(`{"response":{"game_count":1,"games":[{"appid":620,"name":"Portal 2","playtime_forever":630}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubSteamAPI(server.URL+"/summary", server.URL+"/owned")
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
	if entry.LastChange != "2026-05-01T12:00:00Z" {
		t.Fatalf("expected last change to be preserved, got %q", entry.LastChange)
	}
	if entry.HoursPlayed != 10 {
		t.Fatalf("expected hours played to refresh, got %d", entry.HoursPlayed)
	}
	if entry.ProfileAvatarURL != "https://cdn.example/avatar.jpg" {
		t.Fatalf("expected avatar url to refresh, got %q", entry.ProfileAvatarURL)
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
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubSteamAPI(server.URL+"/summary", server.URL+"/owned")
	defer restore()

	snapshot := model.Wuu2{}
	Update(config.Config{
		SteamEnabled:   true,
		SteamWebAPIKey: "key",
		SteamID:        "76561198000000000",
	}, &snapshot)

	if ownedGamesRequested {
		t.Fatal("did not expect owned games request while offline")
	}
	if len(snapshot.Steam) != 1 {
		t.Fatalf("expected 1 steam entry, got %d", len(snapshot.Steam))
	}

	entry := snapshot.Steam[0]
	if entry.GameName != "" {
		t.Fatalf("expected empty game name, got %q", entry.GameName)
	}
	if entry.GameURL != "" {
		t.Fatalf("expected empty game url, got %q", entry.GameURL)
	}
	if entry.HoursPlayed != 0 {
		t.Fatalf("expected zero hours played, got %d", entry.HoursPlayed)
	}
	if entry.LastChange != "2024-03-09T16:00:00Z" {
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
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubSteamAPI(server.URL+"/summary", server.URL+"/owned")
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
	if entry.LastChange != "2024-03-09T17:00:00Z" {
		t.Fatalf("expected updated last logoff, got %q", entry.LastChange)
	}
}

func stubSteamAPI(summaryURL string, gamesURL string) func() {
	previousSummaryURL := playerSummariesURL
	previousOwnedGamesURL := ownedGamesURL
	previousHTTPClient := httpClient

	playerSummariesURL = summaryURL
	ownedGamesURL = gamesURL
	httpClient = http.DefaultClient

	return func() {
		playerSummariesURL = previousSummaryURL
		ownedGamesURL = previousOwnedGamesURL
		httpClient = previousHTTPClient
	}
}
