package downloader

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/html"
)

type Source struct {
	Row    int    `json:"row"`
	Handle string `json:"handle"`
	Column string `json:"column"`
}

type Item struct {
	URL     string   `json:"url"`
	Sources []Source `json:"sources"`
}

func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("无效的 HTTP/HTTPS 图片地址: %q", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("图片地址不能包含用户名密码")
	}
	u.Fragment = ""
	return u.String(), nil
}

// ReadInput accepts Shopify/collection CSV files and plain URL lists, preserving query parameters.
func ReadInput(path string, descriptions bool) ([]Item, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var items []Item
	seen := map[string]int{}
	add := func(raw string, src Source) error {
		if strings.TrimSpace(raw) == "" {
			return nil
		}
		u, err := NormalizeURL(raw)
		if err != nil {
			return fmt.Errorf("第 %d 行 %s: %w", src.Row, src.Column, err)
		}
		if i, ok := seen[u]; ok {
			items[i].Sources = append(items[i].Sources, src)
		} else {
			seen[u] = len(items)
			items = append(items, Item{URL: u, Sources: []Source{src}})
		}
		return nil
	}
	if strings.EqualFold(filepath.Ext(path), ".txt") {
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 4096), 1024*1024)
		for row := 1; s.Scan(); row++ {
			line := strings.TrimSpace(strings.TrimPrefix(s.Text(), "\ufeff"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if err := add(line, Source{Row: row, Column: "URL"}); err != nil {
				return nil, err
			}
		}
		return items, s.Err()
	}
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	headers, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("读取 CSV 表头: %w", err)
	}
	imageCols := map[int]string{}
	handleCol, bodyCol, bodyName := -1, -1, ""
	for i, h := range headers {
		h = strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))
		switch strings.ToLower(h) {
		case "image src", "variant image", "product image url", "variant image url":
			imageCols[i] = h
		case "handle", "url handle":
			handleCol = i
		case "body (html)", "description":
			bodyCol, bodyName = i, h
		}
	}
	if len(imageCols) == 0 && (!descriptions || bodyCol < 0) {
		return nil, fmt.Errorf("未找到图片列：支持 Image Src / Variant Image / Body (HTML)，或采集格式 Product image URL / Variant image URL / Description")
	}
	get := func(row []string, i int) string {
		if i >= 0 && i < len(row) {
			return row[i]
		}
		return ""
	}
	handle := ""
	for rowNumber := 2; ; rowNumber++ {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取 CSV 第 %d 行: %w", rowNumber, err)
		}
		if h := get(row, handleCol); h != "" {
			handle = h
		}
		for i := range headers {
			if name, ok := imageCols[i]; ok {
				if err := add(get(row, i), Source{Row: rowNumber, Handle: handle, Column: name}); err != nil {
					return nil, err
				}
			}
		}
		if descriptions && bodyCol >= 0 {
			z := html.NewTokenizer(strings.NewReader(get(row, bodyCol)))
			for {
				tt := z.Next()
				if tt == html.ErrorToken {
					break
				}
				if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
					continue
				}
				t := z.Token()
				if t.Data != "img" && t.Data != "source" {
					continue
				}
				for _, a := range t.Attr {
					if a.Key != "src" && a.Key != "data-src" {
						continue
					}
					if strings.HasPrefix(a.Val, "http://") || strings.HasPrefix(a.Val, "https://") || strings.HasPrefix(a.Val, "//") {
						if err := add(a.Val, Source{Row: rowNumber, Handle: handle, Column: bodyName}); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	return items, nil
}
