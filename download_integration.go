package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dianxiaomi-converter/internal/imagefilter"
)

func downloaderPath() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("IMAGE403_DOWNLOADER")); configured != "" {
		if info, err := os.Stat(configured); err == nil && !info.IsDir() {
			return filepath.Abs(configured)
		}
		return "", fmt.Errorf("IMAGE403_DOWNLOADER 指向的文件不存在：%s", configured)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(filepath.Dir(exe), "image403-downloader.exe"),
		filepath.Join(filepath.Dir(filepath.Dir(exe)), "image403-downloader.exe"),
		filepath.Join(filepath.Dir(exe), "bin", "image403-downloader.exe"),
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return filepath.Abs(p)
		}
	}
	return "", fmt.Errorf("未找到 image403-downloader.exe；请把两个 EXE 放在同一目录")
}

func downloadConfirmed403(ctx context.Context, report *imagefilter.Report, out, configPath string) error {
	var urls []string
	for _, check := range report.Results {
		if check.Removed {
			urls = append(urls, check.URL)
		}
	}
	if len(urls) == 0 {
		return nil
	}
	helper, err := downloaderPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return err
	}
	manifest := filepath.Join(out, "confirmed-403-urls.txt")
	if err := os.WriteFile(manifest, []byte(strings.Join(urls, "\r\n")+"\r\n"), 0644); err != nil {
		return err
	}
	args := []string{"-no-pause", "-confirmed-403", manifest, "-output-dir", out}
	if configPath != "" {
		args = append(args, "-config", configPath)
	}
	cmd := exec.CommandContext(ctx, helper, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	fmt.Printf("调用 403 图片下载器：%d 个 URL\n", len(urls))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s 执行失败：%w；详情见 %s", filepath.Base(helper), err, filepath.Join(out, "report.csv"))
	}
	return nil
}

func writeConversionSummary(dir string, imageReport *imagefilter.Report, downloadEnabled bool, downloadIssue string, targets []string) error {
	summary := struct {
		Targets            []string            `json:"targets"`
		ImageFilter        *imagefilter.Report `json:"image_filter"`
		Download403Enabled bool                `json:"download_403_enabled"`
		DownloadIssue      string              `json:"download_issue,omitempty"`
	}{Targets: targets, ImageFilter: imageReport, Download403Enabled: downloadEnabled, DownloadIssue: downloadIssue}
	b, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	return writeNew(filepath.Join(dir, "conversion-report.json"), append(b, '\n'))
}
