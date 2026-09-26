package main

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"ai-unisub/internal/logger"
	"ai-unisub/internal/service"
	"ai-unisub/internal/unisub"
)

func main() {
	if err := run(); err != nil {
		ModuleLogger.Error("server_failed", err.Error())
		os.Exit(1)
	}
}

func run() error {
	if err := logger.InitLogging(logger.LogConfig{}); err != nil {
		return err
	}
	dbURL := cmp.Or(os.Getenv("DATABASE_URL"), "sqlite://./data/ai-unisub.db")
	adminUsername := cmp.Or(os.Getenv("ADMIN_USERNAME"), "admin")
	adminPassword := cmp.Or(os.Getenv("ADMIN_PASSWORD"), "admin12345")
	srv, err := unisub.New(unisub.Config{Service: service.Config{DatabaseURL: dbURL, AdminUsername: adminUsername, AdminPassword: adminPassword, OAuthCallbackBaseURL: os.Getenv("OAUTH_CALLBACK_BASE_URL")}, Mode: os.Getenv("UNISUB_MODE"), WebDir: os.Getenv("UNISUB_WEB_DIR")})
	if err != nil {
		return err
	}
	defer func() {
		if err := srv.Close(); err != nil {
			ModuleLogger.Error("shutdown_failed", err.Error())
		}
	}()
	addr := cmp.Or(os.Getenv("LISTEN_ADDR"), ":8080")
	ModuleLogger.Info("server_starting", fmt.Sprintf("AI UniSub listening on %s", addr))
	server := &http.Server{Addr: addr, Handler: srv.Handler(), ErrorLog: ModuleLogger.StandardLogger(slog.LevelError, "http_server_error")}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
