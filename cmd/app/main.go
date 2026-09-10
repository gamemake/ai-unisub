package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"ai-unisub2/internal/oauth"
	"ai-unisub2/internal/oauth/adapters"
)

var errUsage = errors.New("invalid command usage")

const usageText = `Usage:
  app oauth login <provider> [--output <file>]
  app login <provider> [--output <file>]
  app oauth status --file <file>
  app oauth refresh --file <file>
  app oauth revoke --file <file>
  app oauth logout --file <file>

Providers:
  grok       Use Grok Device OAuth
  codex      Use Codex PKCE OAuth
  claude     Use Claude PKCE OAuth

Examples:
  app oauth login grok --output .\oauth\grok.json
  app status --file .\oauth\grok.json
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, errUsage) {
			fmt.Fprint(os.Stderr, usageText)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newManager(store oauth.CredentialStore) *oauth.OAuthManager {
	m := oauth.NewManager(store)
	_ = m.Register(adapters.NewGrok(adapters.GrokConfig{}))
	_ = m.Register(adapters.NewCodex(adapters.CodexConfig{}))
	_ = m.Register(adapters.NewClaude(adapters.ClaudeConfig{}))
	return m
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "oauth" {
		args = args[1:]
	}
	if len(args) < 1 {
		return errUsage
	}
	command := args[0]
	switch command {
	case "login":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "Missing provider for login.")
			return errUsage
		}
		provider := args[1]
		output := option(args[2:], "output")
		if output == "" {
			output = option(args[2:], "file")
		}
		return login(provider, output, stdout, stderr)
	case "status", "refresh", "revoke", "logout":
		path := option(args[1:], "file")
		if path == "" {
			fmt.Fprintf(stderr, "Missing --file for %s.\n", command)
			return errUsage
		}
		return operate(command, path, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Unknown OAuth command %q.\n", command)
		return errUsage
	}
}

func option(args []string, name string) string {
	for i, value := range args {
		if value == "--"+name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(value, "--"+name+"=") {
			return strings.TrimPrefix(value, "--"+name+"=")
		}
	}
	return ""
}

func login(provider, output string, stdout, stderr io.Writer) error {
	if provider != oauth.OAuthServiceGrok && provider != oauth.OAuthServiceCodex && provider != oauth.OAuthServiceClaude {
		return fmt.Errorf("unsupported provider %q", provider)
	}
	logCLI(stderr, "starting OAuth login: provider=%s", provider)
	callbackPath := "/callback"
	if provider == oauth.OAuthServiceCodex {
		callbackPath = "/auth/callback"
	}
	var listener net.Listener
	var err error
	redirect := ""
	if provider != oauth.OAuthServiceGrok {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		defer listener.Close()
		redirect = "http://" + listener.Addr().String() + callbackPath
		logCLI(stderr, "callback server reserved: %s", redirect)
	}
	manager := newManager(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	logCLI(stderr, "requesting authorization from %s", provider)
	start, err := manager.Start(ctx, provider, "", redirect)
	if err != nil {
		logCLI(stderr, "authorization request failed: %v", err)
		return err
	}
	logCLI(stderr, "authorization request completed")
	fmt.Fprintf(stderr, "OAuth authorization URL: %s\n", start.AuthorizationURL)
	if start.UserCode != "" {
		fmt.Fprintf(stderr, "User code: %s\n", start.UserCode)
	}
	if err := openBrowser(start.AuthorizationURL); err != nil {
		fmt.Fprintf(stderr, "Open the URL manually: %v\n", err)
	}

	if provider == oauth.OAuthServiceGrok {
		pollCount := 0
		for {
			pollCount++
			logCLI(stderr, "polling device authorization: attempt=%d", pollCount)
			credential, err := manager.Poll(ctx, start.SessionID)
			if err == nil {
				logCLI(stderr, "device authorization completed")
				return finishCredential(credential, output, stdout, stderr)
			}
			if !errors.Is(err, oauth.ErrAuthorizationPending) && !errors.Is(err, oauth.ErrSlowDown) {
				logCLI(stderr, "device authorization failed: %v", err)
				return err
			}
			logCLI(stderr, "authorization is still pending")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
	callback := make(chan struct {
		code, state string
		err         error
	}, 1)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != callbackPath {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("error") != "" {
			callback <- struct {
				code, state string
				err         error
			}{err: fmt.Errorf("oauth authorization failed: %s", q.Get("error"))}
			return
		}
		callback <- struct {
			code, state string
			err         error
		}{code: q.Get("code"), state: q.Get("state")}
		_, _ = io.WriteString(w, "OAuth authorization completed. You may close this window.")
	})}
	go server.Serve(listener)
	logCLI(stderr, "waiting for browser callback")
	defer server.Shutdown(context.Background())
	select {
	case result := <-callback:
		if result.err != nil {
			logCLI(stderr, "browser callback failed: %v", result.err)
			return result.err
		}
		logCLI(stderr, "browser callback received")
		credential, err := manager.Complete(ctx, start.SessionID, result.code, result.state)
		if err != nil {
			logCLI(stderr, "token exchange failed: %v", err)
			return err
		}
		logCLI(stderr, "OAuth login completed")
		return finishCredential(credential, output, stdout, stderr)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func logCLI(stderr io.Writer, format string, values ...any) {
	fmt.Fprintf(stderr, "[oauth] "+format+"\n", values...)
}

func finishCredential(credential *oauth.OAuthCredential, output string, stdout, stderr io.Writer) error {
	if output != "" {
		raw, err := json.MarshalIndent(credential, "", "  ")
		if err != nil {
			return err
		}
		if err := (oauth.FileCredentialStore{}).SaveCredential(output, raw); err != nil {
			return err
		}
		fmt.Fprintf(stderr, "OAuth credential saved to %s\n", output)
		return nil
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(credential)
}

func operate(command, path string, stdout, stderr io.Writer) error {
	store := oauth.FileCredentialStore{}
	raw, err := store.LoadCredential(path)
	if err != nil {
		return err
	}
	var credential oauth.OAuthCredential
	if err := json.Unmarshal(raw, &credential); err != nil {
		return err
	}
	if credential.Service == "" {
		return errors.New("credential file has no service")
	}
	if command == "status" {
		state := "valid"
		if !credential.ExpiresAt.IsZero() {
			if time.Now().After(credential.ExpiresAt) {
				state = "expired"
			} else if time.Until(credential.ExpiresAt) < time.Hour {
				state = "expiring"
			}
		}
		_, _ = fmt.Fprintf(stdout, "%s\n", state)
		return nil
	}
	manager := newManager(store)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch command {
	case "refresh":
		refreshed, err := manager.Refresh(ctx, credential.Service, &credential)
		if err != nil {
			return err
		}
		value, _ := json.MarshalIndent(refreshed, "", "  ")
		return store.SaveCredential(path, value)
	case "revoke":
		if err := manager.Revoke(ctx, credential.Service, &credential); err != nil {
			return err
		}
		return store.DeleteCredential(path)
	case "logout":
		if err := manager.Revoke(ctx, credential.Service, &credential); err != nil && !strings.Contains(err.Error(), "does not support") {
			return err
		}
		return store.DeleteCredential(path)
	default:
		return fmt.Errorf("unknown oauth command %q", command)
	}
}

func openBrowser(address string) error {
	if _, err := exec.LookPath("rundll32"); err == nil {
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", address).Start()
	}
	if _, err := exec.LookPath("open"); err == nil {
		return exec.Command("open", address).Start()
	}
	if _, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command("xdg-open", address).Start()
	}
	return errors.New("no supported browser launcher found")
}
