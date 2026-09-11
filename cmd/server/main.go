package main

import (
	"log"
	"net/http"
	"os"

	"ai-unisub/internal/service"
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
	srv, err := service.New(service.Config{DatabaseURL: dbURL, AdminUsername: adminUsername, AdminPassword: adminPassword})
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	if err := srv.Auth().EnsureAdmin(adminUsername, adminPassword); err != nil {
		log.Fatal(err)
	}
	for _, module := range []service.Module{service.NewStaticModule(), service.NewAPIModule(), service.NewProxyModule(), service.NewOAuthFlowModule()} {
		if err := srv.AddModule(module); err != nil {
			log.Fatal(err)
		}
	}

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("AI UniSub listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, srv.Handler()))
}
