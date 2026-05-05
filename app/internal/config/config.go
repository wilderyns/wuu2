package config

import (
	"log"
	"net"
	"strings"
	"time"

	"github.com/caarlos0/env/v6"
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

	BattleNetEnabled         bool `env:"BATTLENET_ENABLED"`
	AppleMusicEnabled        bool `env:"APPLEMUSIC_ENABLED"`
	SteamEnabled             bool `env:"STEAM_ENABLED"`
	RetroAchievementsEnabled bool `env:"RETROACHIEVEMENTS_ENABLED"`

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

	AppleMusicDeveloperToken string `env:"APPLEMUSIC_DEVELOPER_TOKEN"`
	AppleMusicTeamID         string `env:"APPLEMUSIC_TEAM_ID"`
	AppleMusicKeyID          string `env:"APPLEMUSIC_KEY_ID"`
	AppleMusicPrivateKeyPath string `env:"APPLEMUSIC_PRIVATE_KEY_PATH" envDefault:"tokens"`

	SteamWebAPIKey string `env:"STEAM_WEBAPI_KEY"`
	SteamID        string `env:"STEAM_ID"`

	RetroAchievementsKey  string `env:"RETROACHIEVEMENTS_KEY"`
	RetroAchievementsUser string `env:"RETROACHIEVEMENTS_USER"`
}

func Load() Config {
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

	if conf.AppleMusicEnabled &&
		strings.TrimSpace(conf.AppleMusicDeveloperToken) == "" &&
		strings.TrimSpace(conf.AppleMusicTeamID) == "" {
		log.Fatal("APPLEMUSIC_ENABLED=true requires APPLEMUSIC_DEVELOPER_TOKEN or APPLEMUSIC_TEAM_ID with a local .p8 key")
	}

	if conf.SteamEnabled {
		required := map[string]string{
			"STEAM_WEBAPI_KEY": conf.SteamWebAPIKey,
			"STEAM_ID":         conf.SteamID,
		}

		var missing []string
		for key, value := range required {
			if strings.TrimSpace(value) == "" {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			log.Fatalf("STEAM_ENABLED=true requires: %s", strings.Join(missing, ", "))
		}
	}

	if conf.RetroAchievementsEnabled {
		required := map[string]string{
			"RETROACHIEVEMENTS_KEY":  conf.RetroAchievementsKey,
			"RETROACHIEVEMENTS_USER": conf.RetroAchievementsUser,
		}

		var missing []string
		for key, value := range required {
			if strings.TrimSpace(value) == "" {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			log.Fatalf("RETROACHIEVEMENTS_ENABLED=true requires: %s", strings.Join(missing, ", "))
		}
	}

	return conf
}

func resolveListenAddress(address string, port string) string {
	address = strings.TrimSpace(address)
	port = strings.TrimSpace(port)

	if address == "" {
		if port != "" {
			return ":" + port
		}
		return ":8080"
	}

	host, p, err := net.SplitHostPort(address)
	if err == nil {
		if host == "localhost" || host == "127.0.0.1" {
			return ":" + p
		}
		return address
	}

	return address
}
