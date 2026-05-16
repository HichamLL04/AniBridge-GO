package config

import "testing"

func TestDefaultValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config should validate: %v", err)
	}
}

func TestInvalidScanMode(t *testing.T) {
	cfg := Default()
	cfg.ScanMode = "nope"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected invalid scan mode error")
	}
}
