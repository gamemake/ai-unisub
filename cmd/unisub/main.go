package main

import (
	"log"
	"net/http"
	"os"

	"ai-unisub/internal/service"
	"ai-unisub/internal/unisub"
)

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "sqlite://./data/ai-unisub.db"
	}
	adminUsername := os.Getenv("ADMIN_USERNAME")
	if adminUsername == "" {
		adminUsername = "admin"
	}
	adminPassword := os.Getenv("ADMIN_PASSWORD")
	if adminPassword == "" {
		adminPassword = "admin12345"
	}
	srv, err := unisub.New(unisub.Config{Service: service.Config{DatabaseURL: dbURL, AdminUsername: adminUsername, AdminPassword: adminPassword, OAuthCallbackBaseURL: os.Getenv("OAUTH_CALLBACK_BASE_URL")}, Mode: os.Getenv("UNISUB_MODE"), WebDir: os.Getenv("UNISUB_WEB_DIR")})
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("AI UniSub listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, srv.Handler()))
}
