package retroachievements

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"wuu2/internal/config"
	"wuu2/internal/model"
)

func TestUpdateSetsRetroAchievementsProfileAndSummaryFields(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("unexpected accept header: %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Fatalf("unexpected user agent: %q", got)
		}

		switch r.URL.Path {
		case "/profile":
			if got := r.URL.Query().Get("u"); got != "wilderyns" {
				t.Fatalf("unexpected user query: %q", got)
			}
			if got := r.URL.Query().Get("y"); got != "api-key" {
				t.Fatalf("unexpected api key query: %q", got)
			}
			_, _ = w.Write([]byte(`{"User":"wilderyns","ULID":"01","UserPic":"/UserPic/wilderyns.png","RichPresenceMsg":"Playing Super Metroid","LastGameID":42,"TotalPoints":1200,"TotalSoftcorePoints":1400,"TotalTruePoints":3600}`))
		case "/summary":
			if got := r.URL.Query().Get("g"); got != "1" {
				t.Fatalf("unexpected recent games query: %q", got)
			}
			if got := r.URL.Query().Get("a"); got != "0" {
				t.Fatalf("unexpected recent achievements query: %q", got)
			}
			_, _ = w.Write([]byte(`{"Rank":512,"Status":"Online","RichPresenceMsg":"Playing Super Metroid","RecentlyPlayed":[{"GameID":42,"Title":"Super Metroid","LastPlayed":"2026-05-04 21:54:54","ImageIcon":"/Images/000042.png"}],"LastGame":{"ID":42,"Title":"Super Metroid","ImageIcon":"/Images/lastgame.png"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubRetroAchievementsAPI(server.URL+"/profile", server.URL+"/summary")
	defer restore()

	snapshot := model.Wuu2{}
	Update(config.Config{
		RetroAchievementsEnabled: true,
		RetroAchievementsKey:     "api-key",
		RetroAchievementsUser:    "wilderyns",
	}, &snapshot)

	if len(snapshot.RetroAchievements) != 1 {
		t.Fatalf("expected 1 RetroAchievements entry, got %d", len(snapshot.RetroAchievements))
	}

	entry := snapshot.RetroAchievements[0]
	if entry.HardcorePoints != 1200 {
		t.Fatalf("unexpected hardcore points: %d", entry.HardcorePoints)
	}
	if entry.SoftcorePoints != 1400 {
		t.Fatalf("unexpected softcore points: %d", entry.SoftcorePoints)
	}
	if entry.RetroPoints != 3600 {
		t.Fatalf("unexpected retro points: %d", entry.RetroPoints)
	}
	if entry.LastGameID != 42 {
		t.Fatalf("unexpected last game id: %d", entry.LastGameID)
	}
	if entry.LastGameTitle != "Super Metroid" {
		t.Fatalf("unexpected last game title: %q", entry.LastGameTitle)
	}
	if entry.GameIconURL != "https://retroachievements.org/Images/000042.png" {
		t.Fatalf("unexpected game icon url: %q", entry.GameIconURL)
	}
	if entry.LastChange != "2026-05-04T21:54:54Z" {
		t.Fatalf("unexpected last change: %q", entry.LastChange)
	}
	if !entry.CurrentlyInGame {
		t.Fatal("expected user to be marked in game")
	}
	if entry.RichPresence != "Playing Super Metroid" {
		t.Fatalf("unexpected rich presence: %q", entry.RichPresence)
	}
	if entry.ProfileAvatarURL != "https://retroachievements.org/UserPic/wilderyns.png" {
		t.Fatalf("unexpected avatar url: %q", entry.ProfileAvatarURL)
	}
	if entry.SiteRank != 512 {
		t.Fatalf("unexpected site rank: %d", entry.SiteRank)
	}
}

func TestUpdatePreservesRankAndTitleWhenSummaryFails(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Fatalf("unexpected accept header: %q", got)
		}
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Fatalf("unexpected user agent: %q", got)
		}

		switch r.URL.Path {
		case "/profile":
			_, _ = w.Write([]byte(`{"User":"wilderyns","ULID":"01","UserPic":"/UserPic/wilderyns.png","RichPresenceMsg":"","LastGameID":84,"TotalPoints":1500,"TotalSoftcorePoints":1700,"TotalTruePoints":4200}`))
		case "/summary":
			http.Error(w, "unavailable", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	restore := stubRetroAchievementsAPI(server.URL+"/profile", server.URL+"/summary")
	defer restore()

	snapshot := model.Wuu2{
		RetroAchievements: []model.RetroAchievements{{
			LastChange:       "2026-05-01T12:00:00Z",
			LastGameID:       84,
			LastGameTitle:    "Chrono Trigger",
			SiteRank:         321,
			CurrentlyInGame:  true,
			ProfileAvatarURL: "https://retroachievements.org/UserPic/old.png",
		}},
	}

	Update(config.Config{
		RetroAchievementsEnabled: true,
		RetroAchievementsKey:     "api-key",
		RetroAchievementsUser:    "wilderyns",
	}, &snapshot)

	entry := snapshot.RetroAchievements[0]
	if entry.LastGameTitle != "Chrono Trigger" {
		t.Fatalf("expected preserved last game title, got %q", entry.LastGameTitle)
	}
	if entry.SiteRank != 321 {
		t.Fatalf("expected preserved site rank, got %d", entry.SiteRank)
	}
	if entry.LastChange != "2026-05-01T12:00:00Z" {
		t.Fatalf("expected preserved last change, got %q", entry.LastChange)
	}
	if entry.CurrentlyInGame {
		t.Fatal("expected empty presence to mark user out of game")
	}
}

func stubRetroAchievementsAPI(profileURL string, summaryURL string) func() {
	previousProfileURL := userProfileURL
	previousSummaryURL := userSummaryURL
	previousHTTPClient := httpClient

	userProfileURL = profileURL
	userSummaryURL = summaryURL
	httpClient = http.DefaultClient

	return func() {
		userProfileURL = previousProfileURL
		userSummaryURL = previousSummaryURL
		httpClient = previousHTTPClient
	}
}
