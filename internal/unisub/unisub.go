// Package unisub composes the UniSub application from service modules.
package unisub

import (
	"ai-unisub/internal/database"
	"ai-unisub/internal/service"
	"ai-unisub/internal/web"
	"cmp"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
)

type Config struct {
	Service service.Config
	Mode    string
	WebDir  string
}

// New creates the application and owns all dependencies until Close.
// DEV reads the Vite output from disk on every request. PRD uses only embed.FS.
func New(cfg Config) (*service.Service, error) {
	files, err := staticFiles(cfg)
	if err != nil {
		return nil, err
	}
	srv, err := service.New(cfg.Service)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*service.Service, error) { _ = srv.Close(); return nil, err }
	if err := srv.Auth().EnsureAdmin(cfg.Service.AdminUsername, cfg.Service.AdminPassword); err != nil {
		return fail(err)
	}
	accounts, err := srv.Database().ListAccounts()
	if err != nil {
		return fail(err)
	}
	slices.SortStableFunc(accounts, func(a, b database.PersistedAccount) int {
		if (a.AIProvider == "group") == (b.AIProvider == "group") {
			return 0
		}
		if a.AIProvider == "group" {
			return 1
		}
		return -1
	})
	for _, account := range accounts {
		if _, err := srv.AIProviders().Create(account.ID, account.AIProvider, account.Config, account.State, account.Quota); err != nil {
			return fail(fmt.Errorf("load provider %d: %w", account.ID, err))
		}
	}
	for _, module := range []service.Module{NewStaticModule(files), NewAPIModule(), NewOAuthFlowModule(), NewGatewayModule()} {
		if err := srv.AddModule(module); err != nil {
			return fail(err)
		}
	}
	return srv, nil
}

func staticFiles(cfg Config) (fs.FS, error) {
	var files fs.FS
	switch strings.ToUpper(strings.TrimSpace(cfg.Mode)) {
	case "", "PRD", "PROD":
		files = web.Files()
	case "DEV":
		dir := cmp.Or(cfg.WebDir, "internal/web/dist")
		files = os.DirFS(dir)
	default:
		return nil, fmt.Errorf("invalid UNISUB_MODE %q: use DEV or PRD", cfg.Mode)
	}
	if _, err := fs.Stat(files, "index.html"); err != nil {
		return nil, fmt.Errorf("web build missing; run npm ci && npm run build: %w", err)
	}
	return files, nil
}
