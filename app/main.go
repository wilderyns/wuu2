package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, using environment variables")
	}

	var config = loadConfig()

	http.HandleFunc("/", handler)
	if config.BattleNetEnabled {
		http.HandleFunc("/auth/battlenet/start", withAuthSecurityGate(config, "Battle.net OAuth", battleNetAuthStartHandler(config)))
		http.HandleFunc("/auth/battlenet/callback", battleNetAuthCallbackHandler(config))
	}
	go timedUpdater(config)
	log.Printf("Listening on %s", config.Address)
	log.Fatal(http.ListenAndServe(config.Address, nil))
}
