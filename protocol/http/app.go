package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"lexmodo-plugin/config"
	"lexmodo-plugin/database"
	"lexmodo-plugin/service"
)

type App struct {
	Config config.Config
	OAuth  *oauth2.Config
	Store  *database.Store
	mu     sync.Mutex
	tokens map[string]settingsAccessToken
}

type settingsAccessToken struct {
	clientID int64
	expires  time.Time
	used     bool
}

func NewApp(cfg config.Config, store *database.Store) *App {
	return &App{
		Config: cfg,
		OAuth:  service.NewOAuthConfig(cfg),
		Store:  store,
		tokens: make(map[string]settingsAccessToken),
	}
}

func (a *App) createSessionToken(clientID int64, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	a.mu.Lock()
	a.tokens[token] = settingsAccessToken{
		clientID: clientID,
		expires:  time.Now().Add(ttl),
	}
	a.mu.Unlock()
	return token, nil
}

func (a *App) consumeSessionToken(clientID int64, token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.tokens[token]
	if !ok {
		return false
	}
	if entry.used || time.Now().After(entry.expires) || entry.clientID != clientID {
		delete(a.tokens, token)
		return false
	}
	entry.used = true
	a.tokens[token] = entry
	return true
}

func (a *App) validateSessionToken(clientID int64, token string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	entry, ok := a.tokens[token]
	if !ok {
		return false
	}
	if entry.used || time.Now().After(entry.expires) || entry.clientID != clientID {
		delete(a.tokens, token)
		return false
	}
	return true
}

func (a *App) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", a.home)
	mux.HandleFunc("/callback", a.callback)
	mux.HandleFunc("/callkey", a.callkey)
	mux.HandleFunc("/settings", a.settingsHandler)
	mux.HandleFunc("/labels/", a.labelHandler)
	mux.Handle(
		"/files/postage_label/",
		http.StripPrefix(
			"/files/postage_label/",
			http.FileServer(http.Dir("files/postage_label")),
		),
	)
	mux.Handle(
		"/orders/files/postage_label/",
		http.StripPrefix(
			"/orders/files/postage_label/",
			http.FileServer(http.Dir("files/postage_label")),
		),
	)
}
