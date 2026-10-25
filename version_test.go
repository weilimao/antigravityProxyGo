package main

import (
	"encoding/json"
	"testing"
)

func TestAppVersion_Consistency(t *testing.T) {
	expectedVersion := "1.7.5"

	if appVersion != expectedVersion {
		t.Fatalf("expected appVersion to be %s, got %s", expectedVersion, appVersion)
	}

	type wailsConfig struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	var cfg wailsConfig
	if err := json.Unmarshal(wailsJSON, &cfg); err != nil {
		t.Fatalf("failed to unmarshal wails.json: %v", err)
	}

	if cfg.Info.ProductVersion != expectedVersion {
		t.Fatalf("expected wails.json productVersion to be %s, got %s", expectedVersion, cfg.Info.ProductVersion)
	}
}
