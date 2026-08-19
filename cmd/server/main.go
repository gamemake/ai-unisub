package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ai-unisub/ai-unisub/internal/config"
	"github.com/ai-unisub/ai-unisub/internal/cryptox"
	"github.com/ai-unisub/ai-unisub/internal/database"
	"github.com/ai-unisub/ai-unisub/internal/repository"
	appserver "github.com/ai-unisub/ai-unisub/internal/server"
)

func main() {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}
	cipher, err := cryptox.New(cfg.MasterKey)
	if err != nil {
		slog.Error("credential cipher error", "error", err)
		os.Exit(1)
	}
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		slog.Error("database error", "error", err)
		os.Exit(1)
	}
	repo := repository.New(db, cipher, cfg.CredentialKeyID)
	defer repo.Close()
	created, err := repo.BootstrapAdmin(ctx, cfg.AdminUsername, cfg.AdminPassword)
	if err != nil {
		slog.Error("admin bootstrap error", "error", err)
		os.Exit(1)
	}
	if created {
		slog.Info("initial admin created", "username", cfg.AdminUsername)
		if cfg.AdminPassword == "admin" {
			slog.Warn("initial admin uses the default password; change it before exposing the service")
		}
	}

	application := appserver.New(cfg, repo)
	httpServer := &http.Server{Addr: cfg.ListenAddress, Handler: application.Handler(), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		slog.Info("server listening", "address", cfg.ListenAddress)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server stopped", "error", err)
			os.Exit(1)
		}
	}()

	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-signalContext.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	_ = httpServer.Shutdown(shutdownContext)
	_ = application.Shutdown(shutdownContext)
}
