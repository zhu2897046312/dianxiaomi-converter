// Package imagefilter checks each distinct URL once, then removes only confirmed 403 images.
package imagefilter

import (
	"context"
	"dianxiaomi-converter/internal/model"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

type Options struct {
	TimeoutSeconds int `json:"timeout_seconds"`
	Concurrency    int `json:"concurrency"`
}

func DefaultOptions() Options { return Options{TimeoutSeconds: 15, Concurrency: 6} }
func (o Options) Validate() error {
	if o.TimeoutSeconds < 1 || o.TimeoutSeconds > 120 || o.Concurrency < 1 || o.Concurrency > 16 {
		return fmt.Errorf("image_filter.timeout_seconds 必须为 1–120，concurrency 必须为 1–16")
	}
	return nil
}

type Check = model.ImageCheck
type Report = model.ImageFilterReport
type Policy struct {
	Report  Report
	blocked map[string]bool
}

// Key preserves query parameters (different image variants may use the same path).
func Key(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "//") {
		s = "https:" + s
	}
	return s
}

func List(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(s, "\n") {
		u := Key(raw)
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

func CheckURLs(ctx context.Context, urls []string, opts Options, client *http.Client, progress func(int, int, Check)) (*Policy, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数超过 10")
			}
			return nil
		}}
		defer client.CloseIdleConnections()
	}
	unique := List(strings.Join(urls, "\n"))
	p := &Policy{blocked: map[string]bool{}, Report: Report{Results: make([]Check, len(unique))}}
	jobs := make(chan int)
	type result struct {
		i int
		c Check
	}
	results := make(chan result)
	var wg sync.WaitGroup
	for n := 0; n < opts.Concurrency; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				c := probe(ctx, client, unique[i], time.Duration(opts.TimeoutSeconds)*time.Second)
				results <- result{i, c}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for i := range unique {
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	for r := range results {
		p.Report.Results[r.i] = r.c
		p.Report.Checked++
		if r.c.Removed {
			p.blocked[r.c.URL] = true
			p.Report.Removed++
		}
		if r.c.Error != "" {
			p.Report.Failed++
		}
		if progress != nil {
			progress(p.Report.Checked, len(unique), r.c)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

func probe(parent context.Context, client *http.Client, u string, timeout time.Duration) Check {
	c := Check{URL: u}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		c.Error = "无法检测：不是有效的 HTTP/HTTPS 图片地址"
		return c
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	resp, err := client.Do(req)
	if err != nil {
		c.Error = err.Error()
		return c
	}
	// GET headers are enough: never download or save the full image body.
	resp.Body.Close()
	c.Status = resp.StatusCode
	c.Removed = c.Status == http.StatusForbidden
	return c
}

func (p *Policy) List(s string) string {
	var out []string
	for _, u := range List(s) {
		if !p.blocked[u] {
			out = append(out, u)
		}
	}
	return strings.Join(out, "\n")
}

// HTMLURLs and HTML use the HTML tokenizer, preserving untouched markup byte-for-byte.
// Repeated <img> src URLs are removed within a description, not across SKU associations.
func HTMLURLs(s string) []string       { _, urls := rewriteHTML(s, nil, false, false); return urls }
func (p *Policy) HTML(s string) string { out, _ := rewriteHTML(s, p.blocked, true, false); return out }

// HTMLForDianxiaomi retains GIF tags even when their URL returned 403. The
// Dianxiaomi exporter replaces those URLs with a final carousel image. Other
// blocked image tags are removed in the same way as HTML.
func (p *Policy) HTMLForDianxiaomi(s string) string {
	out, _ := rewriteHTML(s, p.blocked, true, true)
	return out
}

func rewriteHTML(s string, blocked map[string]bool, clean, keepBlockedGIF bool) (string, []string) {
	z := html.NewTokenizer(strings.NewReader(s))
	var out strings.Builder
	var urls []string
	seen := map[string]bool{}
	pictureDepth := 0
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			if z.Err() != io.EOF {
				return s, urls
			}
			break
		}
		raw := string(z.Raw())
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken && tt != html.EndTagToken {
			out.WriteString(raw)
			continue
		}
		t := z.Token()
		if t.Data == "picture" {
			if tt == html.StartTagToken {
				pictureDepth++
			} else if tt == html.EndTagToken && pictureDepth > 0 {
				pictureDepth--
			}
		}
		if tt == html.EndTagToken || (t.Data != "img" && !(t.Data == "source" && pictureDepth > 0)) {
			out.WriteString(raw)
			continue
		}
		drop := false
		primary := ""
		for _, a := range t.Attr {
			var candidates []string
			switch a.Key {
			case "src", "data-src":
				candidates = []string{a.Val}
				if primary == "" {
					primary = Key(a.Val)
				}
			case "srcset", "data-srcset":
				for _, part := range strings.Split(a.Val, ",") {
					if fields := strings.Fields(part); len(fields) > 0 {
						candidates = append(candidates, fields[0])
					}
				}
			}
			for _, candidate := range candidates {
				u := Key(candidate)
				if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
					urls = append(urls, u)
					if blocked[u] && !(keepBlockedGIF && isGIFURL(u)) {
						drop = true
					}
				}
			}
		}
		if primary != "" && t.Data == "img" {
			if seen[primary] {
				drop = true
			}
			if !drop {
				seen[primary] = true
			}
		}
		if !clean || !drop {
			out.WriteString(raw)
		}
	}
	return out.String(), urls
}

func isGIFURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && strings.EqualFold(pathExtension(u.Path), ".gif")
}

func pathExtension(path string) string {
	if dot := strings.LastIndexByte(path, '.'); dot >= 0 && dot > strings.LastIndexByte(path, '/') {
		return path[dot:]
	}
	return ""
}
