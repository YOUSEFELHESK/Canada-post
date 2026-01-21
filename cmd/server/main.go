package main

import (
	"log"
	"net/http"

	"github.com/joho/godotenv"
	"lexmodo-plugin/config"
	"lexmodo-plugin/database"
	grpcapi "lexmodo-plugin/protocol/grpc"
	httpapi "lexmodo-plugin/protocol/http"
	"lexmodo-plugin/service"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️ No .env file found, using system environment variables")
	}

	cfg := config.LoadConfig()
	log.Println("✅ APP_CLIENT_ID =", cfg.AppClientID)
	log.Println("✅ REDIRECT_URI  =", cfg.RedirectURI)
	log.Println("✅ AUTHORIZE_URL =", cfg.AuthorizeURL)

	store, err := database.NewStore(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	app := httpapi.NewApp(cfg, store)
	mux := http.NewServeMux()
	app.RegisterRoutes(mux)

	log.Printf("🚀 Server running on :%s\n", cfg.Port)

	go grpcapi.Start(cfg.GRPCAddr, service.NewServer(store, cfg))

	log.Fatal(http.ListenAndServe("0.0.0.0:"+cfg.Port, mux))
}
