package httpapi

import (
	"net/http"

	"golang.org/x/oauth2"
	"lexmodo-plugin/config"
	"lexmodo-plugin/database"
	"lexmodo-plugin/service"
)

type App struct {
	Config config.Config
	OAuth  *oauth2.Config
	Store  *database.Store
}

func NewApp(cfg config.Config, store *database.Store) *App {
	return &App{
		Config: cfg,
		OAuth:  service.NewOAuthConfig(cfg),
		Store:  store,
	}
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
