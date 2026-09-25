package service

import (
	"net/http"
	"testing"
)

func TestPresentedAPIKeysFromAuthTokenAndAPIKey(t *testing.T) {
	h := http.Header{}
	if got := PresentedAPIKeys(h); len(got) != 0 {
		t.Fatalf("empty headers: %#v", got)
	}
	h.Set("X-Api-Key", " from-header ")
	if got := PresentedAPIKeys(h); len(got) != 1 || got[0] != "from-header" {
		t.Fatalf("x-api-key: %#v", got)
	}
	if PresentedAPIKey(h) != "from-header" {
		t.Fatal("PresentedAPIKey x-api-key")
	}
	h.Set("Authorization", "Bearer from-bearer")
	if got := PresentedAPIKeys(h); len(got) != 2 || got[0] != "from-bearer" || got[1] != "from-header" {
		t.Fatalf("both headers: %#v", got)
	}
	if PresentedAPIKey(h) != "from-bearer" {
		t.Fatal("PresentedAPIKey prefers bearer order")
	}
	h.Set("Authorization", "Basic from-bearer")
	if got := PresentedAPIKeys(h); len(got) != 1 || got[0] != "from-header" {
		t.Fatalf("non-bearer falls back: %#v", got)
	}
	h.Set("Authorization", "Bearer")
	if got := PresentedAPIKeys(h); len(got) != 1 || got[0] != "from-header" {
		t.Fatalf("empty bearer falls back: %#v", got)
	}
	h.Set("Authorization", "Bearer same")
	h.Set("X-Api-Key", "same")
	if got := PresentedAPIKeys(h); len(got) != 1 || got[0] != "same" {
		t.Fatalf("dedupe: %#v", got)
	}
}
