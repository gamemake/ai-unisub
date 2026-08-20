package server

import (
	"testing"
	"time"
)

func TestNextRequestLogCleanupUsesTopOfConfiguredHour(t *testing.T) {
	location := time.FixedZone("UTC+05:30", 5*60*60+30*60)
	now := time.Date(2026, 8, 19, 10, 47, 31, 0, location)
	next := nextRequestLogCleanup(now, location)
	want := time.Date(2026, 8, 19, 11, 0, 0, 0, location)
	if !next.Equal(want) {
		t.Fatalf("next cleanup = %s, want %s", next, want)
	}
}
