package service

import (
	"testing"

	"ai-unisub/internal/database"
)

func TestServiceComposesVersionTwoManagers(t *testing.T) {
	db, err := database.NewDatabase("sqlite::memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Open(); err != nil {
		t.Fatal(err)
	}
	service, err := NewWithDependencies(Config{}, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(service.AIProviders().ListSuppliers()); got != 7 {
		t.Fatalf("supplier count = %d, want 7", got)
	}
	if groups := service.Proxy().List(); len(groups) != 0 {
		t.Fatalf("proxy groups = %d, want 0", len(groups))
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}
