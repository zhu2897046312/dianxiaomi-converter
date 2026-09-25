package downloader

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Browser starts lazily on the first confirmed 403 and reuses one isolated session.
type Browser struct {
	Config      Config
	ctx         context.Context
	cancel      context.CancelFunc
	allocCancel context.CancelFunc
	profile     string
	profileRoot string
}

func (b *Browser) Close() error {
	if b.ctx != nil && chromedp.FromContext(b.ctx).Browser != nil {
		ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
		_ = chromedp.Cancel(ctx)
		cancel()
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.allocCancel != nil {
		b.allocCancel()
	}
	b.ctx, b.cancel, b.allocCancel = nil, nil, nil
	if b.profileRoot != "" {
		if err := removeOwnedProfile(b.profileRoot, b.profile); err != nil {
			return fmt.Errorf("清理本次浏览器临时目录失败（不影响下次任务）: %w", err)
		}
		b.profileRoot, b.profile = "", ""
	}
	return nil
}

func removeOwnedProfile(root, profile string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	profileAbs, err := filepath.Abs(profile)
	if err != nil {
		return err
	}
	if filepath.Dir(profileAbs) != rootAbs || !strings.HasPrefix(filepath.Base(profileAbs), "run-") {
		return fmt.Errorf("拒绝清理不属于本次任务的目录 %s", profileAbs)
	}
	for attempt := 0; attempt < 3; attempt++ {
		err = os.RemoveAll(profileAbs)
		if runtime.GOOS == "windows" && errors.Is(err, syscall.ELOOP) {
			err = removeProfileByPath(profileAbs)
		}
		if err == nil {
			return nil
		}
		if attempt < 2 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return err
}

func removeProfileByPath(profile string) error {
	var paths []string
	err := filepath.WalkDir(profile, func(path string, entry fs.DirEntry, walkErr error) error {
		if os.IsNotExist(walkErr) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(profile, path)
		if err != nil {
			return err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("拒绝清理目录外的路径 %s", path)
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(paths) - 1; i >= 0; i-- {
		if err := os.Remove(paths[i]); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (b *Browser) prepareProfile() (string, error) {
	if b.Config.ProfileDir != "" {
		profile, err := filepath.Abs(b.Config.ProfileDir)
		if err != nil {
			return "", err
		}
		if err = os.MkdirAll(profile, 0700); err != nil {
			return "", err
		}
		b.profile = profile
		return profile, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(cache, "image403-downloader", "browser-sessions")
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	profile, err := os.MkdirTemp(root, "run-")
	if err != nil {
		return "", err
	}
	b.profileRoot, b.profile = root, profile
	return profile, nil
}

func findBrowser(explicit string) (string, error) {
	if explicit != "" {
		p, err := exec.LookPath(explicit)
		if err != nil {
			return "", fmt.Errorf("browser_path 不可执行: %w", err)
		}
		return p, nil
	}
	var candidates []string
	if runtime.GOOS == "windows" {
		for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")} {
			if root != "" {
				candidates = append(candidates, filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"), filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"))
			}
		}
	} else if runtime.GOOS == "darwin" {
		candidates = append(candidates, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge")
	}
	candidates = append(candidates, "google-chrome", "chromium", "chromium-browser", "msedge", "chrome")
	for _, p := range candidates {
		if path, err := exec.LookPath(p); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到 Chrome 或 Edge；请安装浏览器或在配置中填写 browser_path")
}

func (b *Browser) start(parent context.Context) error {
	path, err := findBrowser(b.Config.BrowserPath)
	if err != nil {
		return err
	}
	profile, err := b.prepareProfile()
	if err != nil {
		return err
	}
	opts := []chromedp.ExecAllocatorOption{chromedp.ExecPath(path), chromedp.UserDataDir(profile), chromedp.NoFirstRun, chromedp.NoDefaultBrowserCheck, chromedp.WindowSize(1280, 900)}
	if b.Config.Headless {
		opts = append(opts, chromedp.Flag("headless", "new"))
	}
	alloc, cancel := chromedp.NewExecAllocator(parent, opts...)
	b.allocCancel = cancel
	b.ctx, b.cancel = chromedp.NewContext(alloc)
	if err := chromedp.Run(b.ctx); err != nil {
		_ = b.Close()
		if b.Config.ProfileDir != "" {
			return fmt.Errorf("启动浏览器失败：%s；配置目录 %s 可能正被另一实例占用: %w", path, profile, err)
		}
		return fmt.Errorf("启动浏览器失败：%s（本次已使用独立临时目录 %s）: %w", path, profile, err)
	}
	return nil
}

func (b *Browser) Fetch(parent context.Context, rawURL string) ([]byte, error) {
	if _, err := NormalizeURL(rawURL); err != nil {
		return nil, err
	}
	if b.ctx == nil {
		if err := b.start(parent); err != nil {
			return nil, err
		}
	}
	tab, closeTab := chromedp.NewContext(b.ctx)
	defer closeTab()
	ctx, cancel := context.WithTimeout(tab, time.Duration(b.Config.BrowserTimeoutSeconds)*time.Second)
	defer cancel()
	stop := context.AfterFunc(parent, cancel)
	defer stop()
	var mu sync.Mutex
	var requestID network.RequestID
	chromedp.ListenTarget(ctx, func(event any) {
		if e, ok := event.(*network.EventResponseReceived); ok && e.Type == network.ResourceTypeDocument {
			mu.Lock()
			requestID = e.RequestID
			mu.Unlock()
		}
	})
	maxBytes := int64(b.Config.MaxImageMB) * 1024 * 1024
	response, err := chromedp.RunResponse(ctx, network.Enable().WithMaxResourceBufferSize(maxBytes+1).WithMaxTotalBufferSize(2*(maxBytes+1)), chromedp.Navigate(rawURL))
	if err != nil {
		return nil, fmt.Errorf("浏览器访问失败: %w", err)
	}
	if response == nil {
		return nil, fmt.Errorf("浏览器未收到图片响应")
	}
	if response.Status < 200 || response.Status >= 300 {
		return nil, fmt.Errorf("浏览器仍返回 HTTP %d", response.Status)
	}
	mu.Lock()
	id := requestID
	mu.Unlock()
	if id == "" {
		return nil, fmt.Errorf("无法定位图片响应")
	}
	var data []byte
	err = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error { var e error; data, e = network.GetResponseBody(id).Do(ctx); return e }))
	if err != nil {
		return nil, fmt.Errorf("读取浏览器原始图片失败: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("图片超过 %d MB", b.Config.MaxImageMB)
	}
	return data, nil
}
