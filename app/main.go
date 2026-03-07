package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/joho/godotenv"
	_ "github.com/joho/godotenv"
)

var WUU2 Wuu2

func getUpdates(config Config) {
	if config.TraktEnabled {
		getTrakt(config, &WUU2)
	}

	if config.BattleNetEnabled {
		getBattle(config, &WUU2)
	}

	// Update Steam

	// TODO: Update Apple Music
}

func timedUpdater(config Config) {
	getUpdates(config)
	for range time.Tick(config.UpdateIntervalMinutes) {
		getUpdates(config)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if len(WUU2.Wow) > 0 {
		if lastModified := strings.TrimSpace(WUU2.Wow[0].LastModified); lastModified != "" {
			w.Header().Set("Last-Modified", lastModified)
		}
	}

	b, err := json.Marshal(WUU2)
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

	serverHandler := withCORS(config, mux)
	go timedUpdater(config)
	log.Printf("Listening on %s", config.Address)
	log.Fatal(http.ListenAndServe(config.Address, serverHandler))
}
