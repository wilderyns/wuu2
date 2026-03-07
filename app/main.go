package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/joho/godotenv"
)

var WUU2 Wuu2
var serverStartTime = time.Now().UTC()
var totalRequests uint64

func getUpdates(config Config) {
	if config.TraktEnabled {
		getTrakt(config, &WUU2)
	}

	if config.BattleNetEnabled {
		getBattle(config, &WUU2)
	}

	// TODO: Update Steam

	// TODO: Update Apple Music
}

func timedUpdater(config Config) {
	// Run as go routine to run updates on schedule
	getUpdates(config)
	for range time.Tick(config.UpdateIntervalMinutes) {
		getUpdates(config)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	responseGeneratedAt := time.Now().UTC()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Last-Modified", responseGeneratedAt.Format(http.TimeFormat))

	response := struct {
		Wuu2
		Information Information `json:"Information"`
	}{
		Wuu2: WUU2,
		Information: Information{
			TotalRequests:   atomic.LoadUint64(&totalRequests),
			ServerStartTime: serverStartTime.Format(time.RFC3339),
		},
	}

	b, err := json.Marshal(response)
	if err != nil {
		fmt.Println(err)
		return
	}

	write, err := w.Write(b)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}

	fmt.Println(write)
}

func withRequestMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint64(&totalRequests, 1)
		next.ServeHTTP(w, r)
	})
}

func withCORS(config Config, next http.Handler) http.Handler {
	allowedOrigins := parseAllowedOrigins(config.CORSAllowOrigin)

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
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using environment variables")
	}

	var config = loadConfig()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handler)
	if config.BattleNetEnabled {
		mux.HandleFunc("/auth/battlenet/start", withAuthSecurityGate(config, "Battle.net OAuth", battleNetAuthStartHandler(config)))
		mux.HandleFunc("/auth/battlenet/callback", battleNetAuthCallbackHandler(config))
	}

	serverHandler := withRequestMetrics(withCORS(config, mux))
	go timedUpdater(config)
	log.Printf("Listening on %s", config.Address)
	log.Fatal(http.ListenAndServe(config.Address, serverHandler))
}
