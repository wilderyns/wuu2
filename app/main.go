package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"

	"wuu2/internal/config"
	"wuu2/internal/integrations/applemusic"
	"wuu2/internal/integrations/battle"
	"wuu2/internal/integrations/steam"
	"wuu2/internal/integrations/trakt"
	"wuu2/internal/lib/authgate"
	"wuu2/internal/lib/persistence"
	"wuu2/internal/model"
)

var serverStartTime = time.Now().UTC()
var totalRequests uint64
var snapshotUpdateMu sync.Mutex

func getUpdates(cfg config.Config, store *persistence.SnapshotStore, battleClient *battle.Client) {
	snapshotUpdateMu.Lock()
	defer snapshotUpdateMu.Unlock()

	snapshot := store.Get()

	if cfg.TraktEnabled {
		trakt.Update(cfg, &snapshot)
	}

	if cfg.BattleNetEnabled {
		battleClient.Update(&snapshot)
	}

	steam.Update(cfg, &snapshot)
	applemusic.Update(cfg, &snapshot)

	store.Set(snapshot)
	if err := store.Persist(snapshot); err != nil {
		log.Printf("Failed persisting snapshot file: %v", err)
	}
}

func timedUpdater(cfg config.Config, store *persistence.SnapshotStore, battleClient *battle.Client) {
	getUpdates(cfg, store, battleClient)
	for range time.Tick(cfg.UpdateIntervalMinutes) {
		getUpdates(cfg, store, battleClient)
	}
}

func handler(store *persistence.SnapshotStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot := store.Get()
		if !persistence.HasWuu2Data(snapshot) {
			if err := store.EnsureLoadedFromDisk(); err != nil {
				log.Printf("Snapshot fallback load failed: %v", err)
			}
			snapshot = store.Get()
		}

		responseGeneratedAt := time.Now().UTC()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Last-Modified", responseGeneratedAt.Format(http.TimeFormat))

		response := struct {
			model.Wuu2
			Information model.Information `json:"Information"`
		}{
			Wuu2: snapshot,
			Information: model.Information{
				TotalRequests:   atomic.LoadUint64(&totalRequests),
				ServerStartTime: serverStartTime.Format(time.RFC3339),
			},
		}

		b, err := json.Marshal(response)
		if err != nil {
			http.Error(w, "failed to marshal response", http.StatusInternalServerError)
			return
		}

		_, _ = w.Write(b)
	}
}

func withRequestMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&totalRequests, 1)
		next.ServeHTTP(w, r)
	})
}

func withCORS(cfg config.Config, next http.Handler) http.Handler {
	allowedOrigins := parseAllowedOrigins(cfg.CORSAllowOrigin)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		applyCORSHeaders(w, r, allowedOrigins)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func applyCORSHeaders(w http.ResponseWriter, r *http.Request, allowedOrigins map[string]struct{}) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	allowOrigin := ""

	if len(allowedOrigins) == 0 {
		allowOrigin = "*"
	} else if _, ok := allowedOrigins["*"]; ok {
		allowOrigin = "*"
	} else if origin != "" {
		if _, ok := allowedOrigins[origin]; ok {
			allowOrigin = origin
			w.Header().Set("Vary", "Origin")
		}
	}

	if allowOrigin != "" {
		w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

func parseAllowedOrigins(raw string) map[string]struct{} {
	origins := map[string]struct{}{}
	for _, part := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(part)
		if origin == "" {
			continue
		}
		origins[origin] = struct{}{}
	}
	return origins
}

func main() {
	// Load config
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}
	cfg := config.Load()

	// Decide if we're using on disk persistence
	if cfg.PersistenceDirectory == "" {
		log.Println("No persistence directory specified, using in-memory persistence")
	} else {
		log.Printf("Using persistence directory: %s", cfg.PersistenceDirectory)
	}
	store := persistence.NewSnapshotStore(persistence.SnapshotFilePathForDirectory(cfg.PersistenceDirectory))
	if err := store.EnsureLoadedFromDisk(); err != nil {
		log.Printf("Failed loading snapshot file: %v", err)
	}

	// Handle auth token persistence loading
	// Battle.net: determine existing auth token
	battleClient := battle.NewClient(cfg)
	if cfg.BattleNetEnabled && cfg.PersistenceDirectory != "" {
		if err := battleClient.LoadPersistedTokenState(); err != nil {
			//TODO: Specify only to show an error if we've previously set a persistence token maybe
			log.Printf("Failed loading Battle.net token file: %v", err)
		}
	}

	// Load pers
	refreshBattleAndPersist := func() error {
		snapshotUpdateMu.Lock()
		defer snapshotUpdateMu.Unlock()

		snapshot := store.Get()
		battleClient.Update(&snapshot)
		store.Set(snapshot)
		return store.Persist(snapshot)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler(store))
	if cfg.BattleNetEnabled {
		mux.HandleFunc("/auth/battlenet/start", authgate.WithSecurityGate(cfg.AuthSecurityCode, "Battle.net OAuth", battleClient.AuthStartHandler()))
		mux.HandleFunc("/auth/battlenet/callback", battleClient.AuthCallbackHandler(refreshBattleAndPersist))
	}

	serverHandler := withRequestMetrics(withCORS(cfg, mux))
	go timedUpdater(cfg, store, battleClient)
	log.Printf("Listening on %s", cfg.Address)
	log.Fatal(http.ListenAndServe(cfg.Address, serverHandler))
}
