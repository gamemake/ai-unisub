package service

import (
	"net/http"
	"testing"
)

func TestPresentedAPIKeyPrefersBearerThenXApiKey(t *testing.T) {
	h := http.Header{}
	if PresentedAPIKey(h) != "" {
		t.Fatal("empty headers")
	}
	h.Set("X-Api-Key", " from-header ")
	if PresentedAPIKey(h) != "from-header" {
		t.Fatal("x-api-key")
	}
	h.Set("Authorization", "Bearer from-bearer")
	if PresentedAPIKey(h) != "from-bearer" {
		t.Fatal("bearer wins")
	}
	h.Set("Authorization", "Basic from-bearer")
	if PresentedAPIKey(h) != "from-header" {
		t.Fatal("non-bearer falls back to x-api-key")
	}
	h.Set("Authorization", "Bearer")
	if PresentedAPIKey(h) != "from-header" {
		t.Fatal("empty bearer falls back to x-api-key")
	}
}
