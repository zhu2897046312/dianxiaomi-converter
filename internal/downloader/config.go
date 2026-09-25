package downloader

import (
	"fmt"
	"strings"
)

// Config controls both the standalone downloader and the converter's helper process.
type Config struct {
	Enabled                  bool   `json:"enabled"`
	ProbeTimeoutSeconds      int    `json:"probe_timeout_seconds"`
	ProbeRetries             int    `json:"probe_retries"`
	ProbeRetryDelayMS        int    `json:"probe_retry_delay_ms"`
	BrowserTimeoutSeconds    int    `json:"browser_timeout_seconds"`
	MaxImageMB               int    `json:"max_image_mb"`
	Headless                 bool   `json:"headless"`
	BrowserPath              string `json:"browser_path"`
	ProfileDir               string `json:"profile_dir"`
	IncludeDescriptionImages bool   `json:"include_description_images"`
	PauseOnExit              bool   `json:"pause_on_exit"`
	OutputFormat             string `json:"output_format"`
	JPEGQuality              int    `json:"jpeg_quality"`
}

func DefaultConfig() Config {
	return Config{
		Enabled: true, ProbeTimeoutSeconds: 20, ProbeRetries: 2, ProbeRetryDelayMS: 1000,
		BrowserTimeoutSeconds: 60, MaxImageMB: 25, Headless: true,
		IncludeDescriptionImages: true, PauseOnExit: true, OutputFormat: "png", JPEGQuality: 90,
	}
}

func (c Config) Validate() error {
	if c.ProbeRetries < 0 || c.ProbeRetries > 5 || c.ProbeRetryDelayMS < 0 || c.ProbeRetryDelayMS > 30000 {
		return fmt.Errorf("probe_retries 必须为 0–5；probe_retry_delay_ms 必须为 0–30000")
	}
	if c.ProbeTimeoutSeconds < 1 || c.BrowserTimeoutSeconds < 1 || c.MaxImageMB < 1 || c.MaxImageMB > 100 {
		return fmt.Errorf("超时必须大于 0；max_image_mb 必须在 1–100 之间")
	}
	switch c.OutputExtension() {
	case "png", "jpg", "original":
	default:
		return fmt.Errorf("output_format 必须是 png、jpg/jpeg 或 original")
	}
	if c.JPEGQuality < 1 || c.JPEGQuality > 100 {
		return fmt.Errorf("jpeg_quality 必须在 1–100 之间")
	}
	return nil
}

func (c Config) OutputExtension() string {
	f := strings.ToLower(strings.TrimSpace(c.OutputFormat))
	if f == "" {
		return "png"
	}
	if f == "jpeg" {
		return "jpg"
	}
	return f
}
