package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMainAndStandaloneConfig(t *testing.T) {
	for _, body := range []string{
		`{"download_403":{"enabled":true,"probe_timeout_seconds":12,"output_format":"jpg"}}`,
		`{"enabled":true,"probe_timeout_seconds":12,"output_format":"jpg"}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := loadConfig(path)
		if err != nil || cfg.ProbeTimeoutSeconds != 12 || cfg.OutputExtension() != "jpg" || cfg.BrowserTimeoutSeconds != 60 {
			t.Fatal(cfg, err)
		}
	}
}
