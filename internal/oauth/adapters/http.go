package adapters

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func nowPlus(minutes int) time.Time { return time.Now().Add(time.Duration(minutes) * time.Minute) }

func defaultHTTPClient() *http.Client { return &http.Client{Timeout: 30 * time.Second} }
func readResponseDo(client *http.Client, req *http.Request, out any) ([]byte, error) {
	started := time.Now()
	log.Printf("[oauth] outbound request method=%s url=%s", req.Method, safeURL(req.URL))
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[oauth] outbound response method=%s url=%s error=%v duration=%s", req.Method, safeURL(req.URL), err, time.Since(started))
		return nil, err
	}
	log.Printf("[oauth] outbound response method=%s url=%s status=%d duration=%s", req.Method, safeURL(req.URL), resp.StatusCode, time.Since(started))
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		return nil, errors.New("oauth response is too large or unreadable")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &e)
		if e.Error != "" {
			return nil, fmt.Errorf("oauth request failed: %s", e.Error)
		}
		return nil, fmt.Errorf("oauth request failed with status %d", resp.StatusCode)
	}
	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return nil, err
		}
	}
	return body, nil
}

func safeURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	return value.Scheme + "://" + value.Host + value.Path
}
