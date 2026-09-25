package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"dianxiaomi-converter/internal/downloader"
)

func main() { os.Exit(run(os.Args[1:])) }

func loadConfig(path string) (downloader.Config, error) {
	cfg := downloader.DefaultConfig()
	if path == "" {
		return cfg, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(b, &root); err != nil {
		return cfg, fmt.Errorf("配置文件无效: %w", err)
	}
	payload := b
	if nested, ok := root["download_403"]; ok {
		payload = nested
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("download_403 配置无效: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return cfg, fmt.Errorf("配置文件只能包含一个 JSON 对象")
	}
	for _, p := range []*string{&cfg.BrowserPath, &cfg.ProfileDir} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(filepath.Dir(path), *p)
		}
	}
	return cfg, cfg.Validate()
}

func defaultConfigPath() string {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe), filepath.Dir(filepath.Dir(exe)))
	}
	dirs = append(dirs, ".")
	for _, dir := range dirs {
		for _, name := range []string{"config.dianxiaomi.json", "config.json"} {
			p := filepath.Join(dir, name)
			if info, err := os.Stat(p); err == nil && !info.IsDir() {
				return p
			}
		}
	}
	return ""
}

func run(args []string) int {
	fs := flag.NewFlagSet("image403-downloader", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath(), "配置文件路径；支持主配置中的 download_403")
	outRoot := fs.String("out", "", "独立模式的输出父目录，默认放在输入文件旁")
	outputDir := fs.String("output-dir", "", "精确输出目录；供主转换器调用")
	directURL := fs.String("url", "", "只检测一个图片 URL")
	confirmed := fs.String("confirmed-403", "", "已确认返回 403 的 urls.txt；直接补下载，不重复检测")
	noPause := fs.Bool("no-pause", false, "结束时不等待回车")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "用法: image403-downloader.exe [选项] shopify.csv [其他.csv]\n也可直接拖入 CSV / urls.txt。仅对普通 GET 返回 HTTP 403 的图片调用浏览器补下载。")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	cfg, err := loadConfig(*configPath)
	pause := !*noPause && cfg.PauseOnExit
	defer func() {
		if pause {
			fmt.Print("\n按回车退出...")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
	}()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	paths := fs.Args()
	if *confirmed != "" {
		if len(paths) > 0 || *directURL != "" {
			fmt.Fprintln(os.Stderr, "-confirmed-403 不能和其他输入同时使用")
			return 1
		}
		paths = []string{*confirmed}
	}
	if len(paths) == 0 && *directURL == "" {
		fmt.Println("请输入 Shopify CSV / urls.txt 路径，或将文件拖入本窗口后回车：")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		line = strings.Trim(strings.TrimSpace(line), "\"")
		if line == "" {
			return 0
		}
		paths = []string{line}
	}
	if *directURL != "" && len(paths) > 0 {
		fmt.Fprintln(os.Stderr, "-url 和输入文件不能同时使用")
		return 1
	}
	if *outputDir != "" && len(paths) > 1 {
		fmt.Fprintln(os.Stderr, "-output-dir 一次只能处理一个输入")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	browser := &downloader.Browser{Config: cfg}
	defer func() {
		if err := browser.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()
	client := downloader.NewProbeClient(time.Duration(cfg.ProbeTimeoutSeconds) * time.Second)
	defer client.CloseIdleConnections()
	if *directURL != "" {
		paths = []string{""}
	}
	exitCode := 0
	for _, path := range paths {
		var items []downloader.Item
		base, parent := "single-url", "."
		if path == "" {
			var u string
			u, err = downloader.NormalizeURL(*directURL)
			if err == nil {
				items = []downloader.Item{{URL: u}}
			}
		} else {
			path, err = filepath.Abs(path)
			if err == nil {
				items, err = downloader.ReadInput(path, cfg.IncludeDescriptionImages)
			}
			base, parent = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), filepath.Dir(path)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitCode = 1
			continue
		}
		if len(items) == 0 {
			fmt.Printf("%s: 未发现图片 URL。\n", path)
			continue
		}
		var out string
		if *outputDir != "" {
			out, err = filepath.Abs(*outputDir)
		} else {
			if *outRoot != "" {
				parent = *outRoot
			}
			if err = os.MkdirAll(parent, 0755); err == nil {
				out, err = os.MkdirTemp(parent, base+"_403_"+time.Now().Format("20060102_150405")+"_")
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitCode = 1
			continue
		}
		fmt.Printf("\n发现 %d 个去重图片 URL。输出目录：%s\n", len(items), out)
		progress := func(i, total int, r downloader.Result) {
			fmt.Printf("[%d/%d] %s | %s | %s\n", i, total, downloader.HTTPStatusLabel(r.HTTPStatus), downloader.StatusLabel(r.Status), r.URL)
			if r.Error != "" {
				fmt.Println("  " + r.Error)
			}
		}
		var summary downloader.Summary
		if *confirmed != "" {
			summary, err = downloader.RunConfirmed(ctx, items, out, cfg, browser, progress)
		} else {
			summary, err = downloader.Run(ctx, items, out, cfg, client, browser, progress)
		}
		fmt.Printf("检查 %d；403 %d；已下载 %d；跳过 %d；失败 %d\n报告：%s\n", summary.Total, summary.Forbidden, summary.Downloaded, summary.Skipped, summary.Failed, filepath.Join(out, "report.csv"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitCode = 1
		}
		if summary.Failed > 0 {
			exitCode = 1
		}
		if ctx.Err() != nil {
			break
		}
	}
	return exitCode
}
