package main

import (
	_ "embed"
	"encoding/json"
)

//go:embed wails.json
var wailsJSON []byte

var appVersion = func() string {
	type wailsConfig struct {
		Info struct {
			ProductVersion string `json:"productVersion"`
		} `json:"info"`
	}
	var cfg wailsConfig
	if err := json.Unmarshal(wailsJSON, &cfg); err == nil && cfg.Info.ProductVersion != "" {
		return cfg.Info.ProductVersion
	}
	return "1.0.2" // fallback
}()
