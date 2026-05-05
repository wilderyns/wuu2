package oauthflow

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"wuu2/internal/lib/persistence"
)

var (
	ErrStartDisabled = errors.New("oauth start is disabled until re-authentication is required")
	ErrInvalidState  = errors.New("invalid oauth state")
	ErrMissingCode   = errors.New("missing oauth code")
)

type AuthRequiredError struct {
	AuthorizeURL string
}

func (e *AuthRequiredError) Error() string {
	authorizeURL := strings.TrimSpace(e.AuthorizeURL)
	if authorizeURL == "" {
		return "oauth authorization required"
	}
	return fmt.Sprintf("oauth authorization required: open %s", authorizeURL)
}

type Manager struct {
	oauthConfig    *oauth2.Config
	tokenFilePath  string
	stateBytes     int
	onPersistError func(error)

	mu           sync.Mutex
	token        *oauth2.Token
	state        string
	startEnabled bool
}

// New creates a new OAuth flow manager.
// oauth2.Config is the config for communicating with the oauth provider
// stateBytes is the number of bytes to use for generating random state strings
// onPersistError is an optional callback that is invoked when an error occurs persisting the auth token state
// tokenFilePath is optional; if omitted or empty, token persistence is disabled.
// If oauthconfig isn't set we panic.
func New(oauthConfig *oauth2.Config, stateBytes int, onPersistError func(error), tokenFilePath ...string) *Manager {
	//TODO: further error handle individual oauthconfig fields
	if oauthConfig == nil {
		panic("oauthflow.New requires non-nil oauth2.Config")
	}

	path := ""
	if len(tokenFilePath) > 0 {
		path = tokenFilePath[0]
	}

	if stateBytes <= 0 {
		stateBytes = 16
	}
	clonedConfig := *oauthConfig

	return &Manager{
		oauthConfig:    &clonedConfig,
		tokenFilePath:  strings.TrimSpace(path),
		stateBytes:     stateBytes,
		onPersistError: onPersistError,
		startEnabled:   true,
	}
}

func ParseScopes(raw string) []string {
	raw = strings.ReplaceAll(raw, ",", " ")
	return strings.Fields(raw)
}

func (m *Manager) LoadPersistedTokenState() error {
	persisted, err := persistence.LoadAuthTokenState(m.tokenFilePath)
	if err != nil {
		return err
	}

	var parsedExpiry time.Time
	expiresAtRaw := strings.TrimSpace(persisted.ExpiresAt)
	if expiresAtRaw != "" {
		parsedExpiry, err = time.Parse(time.RFC3339, expiresAtRaw)
		if err != nil {
			return err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	accessToken := strings.TrimSpace(persisted.AccessToken)
	refreshToken := strings.TrimSpace(persisted.RefreshToken)
	if accessToken != "" || refreshToken != "" {
		m.token = &oauth2.Token{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			Expiry:       parsedExpiry,
		}
	} else {
		m.token = nil
	}

	m.startEnabled = persisted.StartEnabled
	if m.token == nil {
		m.startEnabled = true
	}

	return nil
}

func (m *Manager) StartAuthorizationURL() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.startEnabled {
		return "", ErrStartDisabled
	}

	state := randomHex(m.stateBytes)
	m.state = state
	return m.oauthConfig.AuthCodeURL(state), nil
}

func (m *Manager) ExchangeCode(ctx context.Context, code string, state string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return ErrMissingCode
	}

	m.mu.Lock()
	if !m.validateStateLocked(strings.TrimSpace(state)) {
		m.mu.Unlock()
		return ErrInvalidState
	}
	m.mu.Unlock()

	token, err := m.oauthConfig.Exchange(ctx, code)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return errors.New("empty access_token in oauth2 response")
	}

	m.applyToken(token, false)
	return nil
}

func (m *Manager) EnsureAccessToken(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	m.mu.Lock()
	token := cloneOAuthToken(m.token)
	if token == nil {
		if !m.startEnabled {
			m.startEnabled = true
			m.persistTokenStateLocked()
		}
		authorizeURL := m.oauthConfig.AuthCodeURL("")
		m.mu.Unlock()
		return "", &AuthRequiredError{AuthorizeURL: authorizeURL}
	}
	m.mu.Unlock()

	tokenSource := m.oauthConfig.TokenSource(ctx, token)
	refreshed, err := tokenSource.Token()
	if err != nil {
		m.EnableStart()
		return "", err
	}
	if strings.TrimSpace(refreshed.AccessToken) == "" {
		m.EnableStart()
		return "", errors.New("empty access_token in oauth2 response")
	}

	m.applyToken(refreshed, false)
	return refreshed.AccessToken, nil
}

func (m *Manager) EnableStart() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.startEnabled {
		return
	}
	m.startEnabled = true
	m.persistTokenStateLocked()
}

func (m *Manager) ClearAccessToken() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.token == nil {
		return
	}

	m.token.AccessToken = ""
	m.token.Expiry = time.Time{}
	m.persistTokenStateLocked()
}

func (m *Manager) applyToken(token *oauth2.Token, startEnabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	normalized := cloneOAuthToken(token)
	if oauthTokensEqual(m.token, normalized) && m.startEnabled == startEnabled {
		return
	}

	m.token = normalized
	m.startEnabled = startEnabled
	m.persistTokenStateLocked()
}

func (m *Manager) persistTokenStateLocked() {
	var accessToken string
	var refreshToken string
	var expiresAt time.Time
	if m.token != nil {
		accessToken = strings.TrimSpace(m.token.AccessToken)
		refreshToken = strings.TrimSpace(m.token.RefreshToken)
		expiresAt = m.token.Expiry
	}

	persisted := persistence.AuthTokenState{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		StartEnabled: m.startEnabled,
	}
	if !expiresAt.IsZero() {
		persisted.ExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	}

	if err := persistence.SaveAuthTokenState(m.tokenFilePath, persisted); err != nil {
		m.reportPersistError(err)
	}
}

func (m *Manager) validateStateLocked(state string) bool {
	if m.state == "" {
		return true
	}
	if state == "" || state != m.state {
		return false
	}

	m.state = ""
	return true
}

func (m *Manager) reportPersistError(err error) {
	if err == nil || m.onPersistError == nil {
		return
	}
	m.onPersistError(err)
}

func cloneOAuthToken(token *oauth2.Token) *oauth2.Token {
	if token == nil {
		return nil
	}
	return new(*token)
}

func oauthTokensEqual(a *oauth2.Token, b *oauth2.Token) bool {
	if a == nil || b == nil {
		return a == b
	}
	return strings.TrimSpace(a.AccessToken) == strings.TrimSpace(b.AccessToken) &&
		strings.TrimSpace(a.RefreshToken) == strings.TrimSpace(b.RefreshToken) &&
		strings.TrimSpace(a.TokenType) == strings.TrimSpace(b.TokenType) &&
		a.Expiry.Equal(b.Expiry)
}

func randomHex(size int) string {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
