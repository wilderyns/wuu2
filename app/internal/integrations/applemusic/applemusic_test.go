package applemusic

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wuu2/internal/config"
	"wuu2/internal/model"
)

func TestUpdateSetsMostRecentlyPlayedSongDetails(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer developer-token" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("Music-User-Token"); got != "music-user-token" {
			t.Fatalf("unexpected music-user-token header: %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "1" {
			t.Fatalf("unexpected limit query: %q", got)
		}
		if got := r.URL.Query().Get("types"); got != "songs,library-songs" {
			t.Fatalf("unexpected types query: %q", got)
		}
		if got := r.URL.Query().Get("include"); got != "albums,artists" {
			t.Fatalf("unexpected include query: %q", got)
		}

		_, _ = w.Write([]byte(`{
			"data": [{
				"id": "123",
				"type": "songs",
				"attributes": {
					"name": "Everything In Its Right Place",
					"artistName": "Radiohead",
					"albumName": "Kid A",
					"url": "https://music.apple.com/us/album/everything-in-its-right-place/123"
				}
			}],
			"included": [
				{"id":"album-1","type":"albums","attributes":{"name":"Kid A","url":"https://music.apple.com/us/album/kid-a/album-1"}},
				{"id":"artist-1","type":"artists","attributes":{"name":"Radiohead","url":"https://music.apple.com/us/artist/radiohead/artist-1"}}
			]
		}`))
	}))
	defer server.Close()

	restore := stubAppleMusicAPI(server.URL)
	defer restore()

	client := NewClient(config.Config{
		AppleMusicEnabled:        true,
		AppleMusicDeveloperToken: "developer-token",
	})
	client.auth.userToken = "music-user-token"

	snapshot := model.Wuu2{}
	client.Update(&snapshot)

	if len(snapshot.AppleMusic) != 1 {
		t.Fatalf("expected 1 Apple Music entry, got %d", len(snapshot.AppleMusic))
	}

	entry := snapshot.AppleMusic[0]
	if entry.Song != "Everything In Its Right Place" {
		t.Fatalf("unexpected song: %q", entry.Song)
	}
	if entry.SongLink != "https://music.apple.com/us/album/everything-in-its-right-place/123" {
		t.Fatalf("unexpected song link: %q", entry.SongLink)
	}
	if entry.Artist != "Radiohead" {
		t.Fatalf("unexpected artist: %q", entry.Artist)
	}
	if entry.ArtistLink != "https://music.apple.com/us/artist/radiohead/artist-1" {
		t.Fatalf("unexpected artist link: %q", entry.ArtistLink)
	}
	if entry.Album != "Kid A" {
		t.Fatalf("unexpected album: %q", entry.Album)
	}
	if entry.AlbumLink != "https://music.apple.com/us/album/kid-a/album-1" {
		t.Fatalf("unexpected album link: %q", entry.AlbumLink)
	}
	if entry.LastChange == "" {
		t.Fatal("expected last change to be set")
	}
}

func TestUpdatePreservesLastChangeWhenTrackIsUnchanged(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"data": [{
				"id": "123",
				"type": "songs",
				"attributes": {
					"name": "Everything In Its Right Place",
					"artistName": "Radiohead",
					"albumName": "Kid A",
					"url": "https://music.apple.com/us/album/everything-in-its-right-place/123"
				}
			}]
		}`))
	}))
	defer server.Close()

	restore := stubAppleMusicAPI(server.URL)
	defer restore()

	client := NewClient(config.Config{
		AppleMusicEnabled:        true,
		AppleMusicDeveloperToken: "developer-token",
	})
	client.auth.userToken = "music-user-token"

	snapshot := model.Wuu2{
		AppleMusic: []model.AppleMusic{{
			LastChange: "2026-05-01T12:00:00Z",
			Song:       "Everything In Its Right Place",
			SongLink:   "https://music.apple.com/us/album/everything-in-its-right-place/123",
			Artist:     "Radiohead",
			Album:      "Kid A",
		}},
	}

	client.Update(&snapshot)

	entry := snapshot.AppleMusic[0]
	if entry.LastChange != "2026-05-01T12:00:00Z" {
		t.Fatalf("expected last change to be preserved, got %q", entry.LastChange)
	}
}

func TestAuthCallbackPersistsUserTokenAndRefreshesSnapshot(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	client := NewClient(config.Config{
		AppleMusicEnabled:        true,
		AppleMusicDeveloperToken: "developer-token",
		TokenPersistenceEnabled:  true,
		PersistenceDirectory:     dir,
	})
	client.auth.state = "abc123"

	var refreshed bool
	handler := client.AuthCallbackHandler(func() error {
		refreshed = true
		return nil
	})

	req := httptest.NewRequest(http.MethodPost, "/auth/applemusic/callback", strings.NewReader(`{"musicUserToken":"music-user-token","state":"abc123"}`))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", recorder.Code)
	}
	if !refreshed {
		t.Fatal("expected snapshot refresh callback to run")
	}
	if client.isStartEnabled() {
		t.Fatal("expected auth start to be disabled after successful authorization")
	}

	tokenPath := filepath.Join(dir, "tokens", "applemusic.json")
	payload, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("failed reading persisted token file: %v", err)
	}

	var persisted struct {
		AccessToken  string `json:"accessToken"`
		StartEnabled bool   `json:"startEnabled"`
	}
	if err := json.Unmarshal(payload, &persisted); err != nil {
		t.Fatalf("failed unmarshalling persisted token file: %v", err)
	}
	if persisted.AccessToken != "music-user-token" {
		t.Fatalf("unexpected persisted access token: %q", persisted.AccessToken)
	}
	if persisted.StartEnabled {
		t.Fatal("expected persisted startEnabled=false after successful authorization")
	}
}

func TestUpdateReEnablesAuthorizationWhenTokenIsRejected(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	restore := stubAppleMusicAPI(server.URL)
	defer restore()

	dir := t.TempDir()
	client := NewClient(config.Config{
		AppleMusicEnabled:        true,
		AppleMusicDeveloperToken: "developer-token",
		TokenPersistenceEnabled:  true,
		PersistenceDirectory:     dir,
		Address:                  ":8080",
	})
	client.auth.userToken = "stale-token"
	client.auth.startEnabled = false

	snapshot := model.Wuu2{}
	client.Update(&snapshot)

	if !client.isStartEnabled() {
		t.Fatal("expected auth start to be re-enabled after unauthorized response")
	}

	tokenPath := filepath.Join(dir, "tokens", "applemusic.json")
	payload, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("failed reading persisted token file: %v", err)
	}

	var persisted struct {
		AccessToken  string `json:"accessToken"`
		StartEnabled bool   `json:"startEnabled"`
	}
	if err := json.Unmarshal(payload, &persisted); err != nil {
		t.Fatalf("failed unmarshalling persisted token file: %v", err)
	}
	if persisted.AccessToken != "" {
		t.Fatalf("expected cleared persisted token, got %q", persisted.AccessToken)
	}
	if !persisted.StartEnabled {
		t.Fatal("expected persisted startEnabled=true after unauthorized response")
	}
}

func TestDeveloperTokenGeneratedFromLocalP8Key(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "AuthKey_TESTKEY123.p8")
	writeTestPrivateKey(t, keyPath)

	client := NewClient(config.Config{
		AppleMusicEnabled:        true,
		AppleMusicTeamID:         "TEAM123456",
		AppleMusicPrivateKeyPath: dir,
	})

	token, err := client.developerToken()
	if err != nil {
		t.Fatalf("expected developer token, got error: %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected JWT with 3 parts, got %d", len(parts))
	}

	headerPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("failed decoding JWT header: %v", err)
	}
	claimsPayload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("failed decoding JWT claims: %v", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("failed decoding JWT signature: %v", err)
	}

	var header map[string]string
	if err := json.Unmarshal(headerPayload, &header); err != nil {
		t.Fatalf("failed unmarshalling JWT header: %v", err)
	}
	if header["alg"] != "ES256" {
		t.Fatalf("unexpected alg: %q", header["alg"])
	}
	if header["kid"] != "TESTKEY123" {
		t.Fatalf("unexpected kid: %q", header["kid"])
	}

	var claims struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if err := json.Unmarshal(claimsPayload, &claims); err != nil {
		t.Fatalf("failed unmarshalling JWT claims: %v", err)
	}
	if claims.Iss != "TEAM123456" {
		t.Fatalf("unexpected iss: %q", claims.Iss)
	}
	if claims.Exp <= claims.Iat {
		t.Fatalf("expected exp > iat, got iat=%d exp=%d", claims.Iat, claims.Exp)
	}
	if lifetime := time.Unix(claims.Exp, 0).Sub(time.Unix(claims.Iat, 0)); lifetime != developerTokenLifetime {
		t.Fatalf("unexpected token lifetime: %s", lifetime)
	}

	if len(signature) != 64 {
		t.Fatalf("expected 64-byte ES256 signature, got %d", len(signature))
	}
}

func TestResolvePrivateKeyPathRequiresSingleP8File(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	writeTestPrivateKey(t, filepath.Join(dir, "AuthKey_ONE1234567.p8"))
	writeTestPrivateKey(t, filepath.Join(dir, "AuthKey_TWO1234567.p8"))

	if _, err := resolvePrivateKeyPath(dir); err == nil {
		t.Fatal("expected error when multiple .p8 files exist")
	}
}

func stubAppleMusicAPI(baseURL string) func() {
	previousRecentPlayedTracksURL := recentPlayedTracksURL
	previousHTTPClient := httpClient

	recentPlayedTracksURL = baseURL
	httpClient = http.DefaultClient

	return func() {
		recentPlayedTracksURL = previousRecentPlayedTracksURL
		httpClient = previousHTTPClient
	}
}

func writeTestPrivateKey(t *testing.T, path string) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed generating test private key: %v", err)
	}

	payload, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("failed marshaling test private key: %v", err)
	}

	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: payload,
	})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("failed writing test private key: %v", err)
	}
}
