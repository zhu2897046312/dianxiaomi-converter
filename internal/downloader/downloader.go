package downloader

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Fetcher interface {
	Fetch(context.Context, string) ([]byte, error)
}

type Result struct {
	Item
	HTTPStatus    int    `json:"http_status"`
	Status        string `json:"status"`
	File          string `json:"file,omitempty"`
	Bytes         int    `json:"bytes,omitempty"`
	Error         string `json:"error,omitempty"`
	SourceFormat  string `json:"source_format,omitempty"`
	OutputFormat  string `json:"output_format,omitempty"`
	ProbeAttempts int    `json:"probe_attempts"`
}

type Summary struct {
	Total      int `json:"total"`
	Forbidden  int `json:"forbidden"`
	Downloaded int `json:"downloaded"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`
}

func NewProbeClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("重定向超过 10 次")
		}
		_, err := NormalizeURL(req.URL.String())
		return err
	}}
}

func Probe(ctx context.Context, client *http.Client, rawURL string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func Run(ctx context.Context, items []Item, out string, cfg Config, client *http.Client, browser Fetcher, progress func(int, int, Result)) (Summary, error) {
	return run(ctx, items, out, cfg, client, browser, false, progress)
}

// RunConfirmed downloads URLs already confirmed as HTTP 403 by the converter, avoiding a second probe.
func RunConfirmed(ctx context.Context, items []Item, out string, cfg Config, browser Fetcher, progress func(int, int, Result)) (Summary, error) {
	return run(ctx, items, out, cfg, nil, browser, true, progress)
}

func run(ctx context.Context, items []Item, out string, cfg Config, client *http.Client, browser Fetcher, confirmed bool, progress func(int, int, Result)) (Summary, error) {
	var summary Summary
	if err := cfg.Validate(); err != nil {
		return summary, err
	}
	if err := os.MkdirAll(out, 0755); err != nil {
		return summary, err
	}
	report, err := os.OpenFile(filepath.Join(out, "report.csv"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return summary, err
	}
	defer report.Close()
	if _, err := report.WriteString("\ufeff"); err != nil {
		return summary, err
	}
	w := csv.NewWriter(report)
	if err := w.Write([]string{"URL", "检测HTTP状态", "处理结果", "本地文件", "字节数", "错误", "CSV来源(JSON)", "来源格式", "保存格式", "检测次数"}); err != nil {
		return summary, err
	}
	failed, err := os.OpenFile(filepath.Join(out, "failed_urls.txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return summary, err
	}
	defer failed.Close()
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		r := Result{Item: item}
		summary.Total++
		if confirmed {
			r.HTTPStatus = http.StatusForbidden
		} else {
			r.HTTPStatus, r.ProbeAttempts, err = probeWithRetry(ctx, client, item.URL, cfg, func(next int, probeErr error) {
				if progress != nil {
					progress(i+1, len(items), Result{Item: item, Status: "probe_retry", ProbeAttempts: next, Error: probeErr.Error()})
				}
			})
		}
		switch {
		case err != nil:
			r.Status, r.Error = "probe_error", err.Error()
			summary.Failed++
		case r.HTTPStatus != http.StatusForbidden:
			r.Status = "skipped_non_403"
			summary.Skipped++
		default:
			summary.Forbidden++
			if browser == nil {
				err = fmt.Errorf("浏览器下载器不可用")
			} else {
				var data []byte
				data, err = browser.Fetch(ctx, item.URL)
				if err == nil {
					data, r.SourceFormat, r.OutputFormat, err = PrepareImage(data, cfg)
				}
				if err == nil {
					digest := sha256.Sum256([]byte(item.URL))
					r.File = filepath.ToSlash(filepath.Join("images", hex.EncodeToString(digest[:])+"."+r.OutputFormat))
					err = saveAtomic(filepath.Join(out, filepath.FromSlash(r.File)), data)
					r.Bytes = len(data)
				}
			}
			if err != nil {
				r.Status, r.Error, r.File = "download_failed", err.Error(), ""
				summary.Failed++
			} else {
				r.Status = "downloaded_403"
				summary.Downloaded++
			}
		}
		refs, _ := json.Marshal(r.Sources)
		status := strconv.Itoa(r.HTTPStatus)
		if r.HTTPStatus == 0 {
			status = "未收到响应"
		}
		if err := w.Write([]string{r.URL, status, r.Status, r.File, strconv.Itoa(r.Bytes), r.Error, string(refs), r.SourceFormat, r.OutputFormat, strconv.Itoa(r.ProbeAttempts)}); err != nil {
			return summary, err
		}
		w.Flush()
		if err := w.Error(); err != nil {
			return summary, err
		}
		if r.Error != "" {
			if _, err := fmt.Fprintln(failed, r.URL); err != nil {
				return summary, err
			}
		}
		if progress != nil {
			progress(i+1, len(items), r)
		}
	}
	data, _ := json.MarshalIndent(summary, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "summary.json"), append(data, '\n'), 0644); err != nil {
		return summary, err
	}
	return summary, nil
}

func saveAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "*.part")
	if err != nil {
		return err
	}
	temp := f.Name()
	defer os.Remove(temp)
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temp, path)
}

func StatusLabel(s string) string {
	switch strings.TrimSpace(s) {
	case "downloaded_403":
		return "403 已补下载"
	case "skipped_non_403":
		return "非403，跳过"
	case "probe_error":
		return "检查失败"
	case "probe_retry":
		return "检查重试"
	default:
		return "补下载失败"
	}
}
