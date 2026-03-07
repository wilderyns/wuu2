package main

import (
	"log"
	"net"
	"strings"
	"time"

	"github.com/caarlos0/env/v6"
	_ "github.com/joho/godotenv"
)

type Config struct {
	UpdateIntervalMinutes   time.Duration `env:"UPDATE_INTERVAL_MINUTES,required"`
	Address                 string        `env:"ADDRESS"`
	Port                    string        `env:"PORT"`
	CORSAllowOrigin         string        `env:"CORS_ALLOW_ORIGIN" envDefault:"*"`
	PersistenceDirectory    string        `env:"PERSISTENCE_DIRECTORY" envDefault:"/tmp/wuu2"`
	TokenPersistenceEnabled bool          `env:"TOKEN_PERSISTENCE_ENABLED" envDefault:"false"`

	TraktEnabled bool   `env:"TRAKT_ENABLED"`
	TraktID      string `env:"TRAKT_ID"`

	BattleNetEnabled bool `env:"BATTLENET_ENABLED"`

	AuthSecurityCode string `env:"AUTH_SECURITY_CODE"`

	BattleNetRequestURI   string `env:"BATTLENET_REQUEST_URI"`
	BattleNetClientID     string `env:"BATTLENET_CLIENT_ID"`
	BattleNetClientSecret string `env:"BATTLENET_CLIENT_SECRET"`
	BattleNetRealm        string `env:"BATTLENET_REALM"`
	BattleNetCharacter    string `env:"BATTLENET_CHARACTER"`
	BattleNetCharacterID  string `env:"BATTLENET_CHARACTER_ID"`
	BattleNetLocale       string `env:"BATTLENET_LOCALE"`
	BattleNetRegion       string `env:"BATTLENET_REGION"`
	BattleNetRedirectURI  string `env:"BATTLENET_REDIRECT_URI"`
	BattleNetScope        string `env:"BATTLENET_SCOPE"`
}

func loadConfig() Config {
	var conf Config
	if err := env.Parse(&conf); err != nil {
		log.Fatalf("Failed to parse environment variables: %v", err)
	}

	conf.Address = resolveListenAddress(conf.Address, conf.Port)
	conf.PersistenceDirectory = strings.TrimSpace(conf.PersistenceDirectory)
	if conf.PersistenceDirectory == "" {
		conf.PersistenceDirectory = "/tmp/wuu2"
	}

	if conf.TraktEnabled && strings.TrimSpace(conf.TraktID) == "" {
		log.Fatal("TRAKT_ENABLED=true requires TRAKT_ID")
	}

	if conf.BattleNetEnabled {
		required := map[string]string{
			"BATTLENET_REQUEST_URI":   conf.BattleNetRequestURI,
			"BATTLENET_CLIENT_ID":     conf.BattleNetClientID,
			"BATTLENET_CLIENT_SECRET": conf.BattleNetClientSecret,
			"BATTLENET_REALM":         conf.BattleNetRealm,
			"BATTLENET_CHARACTER_ID":  conf.BattleNetCharacterID,
			"BATTLENET_REGION":        conf.BattleNetRegion,
			"BATTLENET_REDIRECT_URI":  conf.BattleNetRedirectURI,
			"BATTLENET_SCOPE":         conf.BattleNetScope,
		}

		var missing []string
		for key, value := range required {
			if strings.TrimSpace(value) == "" {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			log.Fatalf("BATTLENET_ENABLED=true requires: %s", strings.Join(missing, ", "))
		}
	}

	return conf
}

func resolveListenAddress(address string, port string) string {
	address = strings.TrimSpace(address)
	port = strings.TrimSpace(port)

	// Cloud Run sets PORT; default to binding all interfaces on that port.
	if address == "" {
		if port != "" {
			return ":" + port
		}
		return ":8080"
	}

	// If an localhost bind is configured, convert to all interfaces so
	// container platforms (Cloud Run) can reach the process.
	host, p, err := net.SplitHostPort(address)
	if err == nil {
		if host == "localhost" || host == "127.0.0.1" {
			return ":" + p
		}
		return address
	}

	return address
}
