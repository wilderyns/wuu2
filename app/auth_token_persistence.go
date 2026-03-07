package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
)

type persistedAuthTokenState struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	StartEnabled bool   `json:"startEnabled"`
}

func loadPersistedAuthTokenState(path string) (persistedAuthTokenState, error) {
	var persisted persistedAuthTokenState

	path = strings.TrimSpace(path)
	if path == "" {
		return persisted, nil
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persisted, nil
		}
		return persisted, err
	}

	if len(payload) == 0 {
		return persisted, nil
	}

	if err := json.Unmarshal(payload, &persisted); err != nil {
		return persisted, err
	}

	return persisted, nil
}

func savePersistedAuthTokenState(path string, persisted persistedAuthTokenState) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}

	payload, err := json.Marshal(persisted)
	if err != nil {
		return err
	}

	return writeFileAtomically(path, payload, 0o600)
}
