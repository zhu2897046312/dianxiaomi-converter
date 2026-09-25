package downloader

import (
	"bytes"
	"context"
	"encoding/csv"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeBrowser struct {
	data  []byte
	calls []string
}

func TestReadCSVAndURLListDeduplicate(t *testing.T) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.WriteAll([][]string{
		{"URL handle", "Product image URL", "Variant image URL", "Description"},
		{"p", "https://example.com/a.jpg?v=1", "https://example.com/a.jpg?v=1", `<img src="https://example.com/detail.gif">`},
	})
	csvPath := filepath.Join(t.TempDir(), "shopify.csv")
	if err := os.WriteFile(csvPath, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	items, err := ReadInput(csvPath, true)
	if err != nil || len(items) != 2 || len(items[0].Sources) != 2 {
		t.Fatal(items, err)
	}
	txtPath := filepath.Join(t.TempDir(), "urls.txt")
	if err := os.WriteFile(txtPath, []byte(strings.Join([]string{"https://example.com/a.png#one", "https://example.com/a.png#two"}, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	items, err = ReadInput(txtPath, true)
	if err != nil || len(items) != 1 || len(items[0].Sources) != 2 {
		t.Fatal(items, err)
	}
}

func (f *fakeBrowser) Fetch(_ context.Context, u string) ([]byte, error) {
	f.calls = append(f.calls, u)
	return f.data, nil
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	im := image.NewRGBA(image.Rect(0, 0, 2, 2))
	im.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestRunConfirmedSkipsProbeAndDownloads(t *testing.T) {
	browser := &fakeBrowser{data: testPNG(t)}
	out := t.TempDir()
	summary, err := RunConfirmed(context.Background(), []Item{{URL: "https://example.com/a.png"}}, out, DefaultConfig(), browser, nil)
	if err != nil || summary.Total != 1 || summary.Forbidden != 1 || summary.Downloaded != 1 || len(browser.calls) != 1 {
		t.Fatal(summary, browser.calls, err)
	}
	if _, err := os.Stat(filepath.Join(out, "report.csv")); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Join(out, "images"))
	if err != nil || len(files) != 1 || filepath.Ext(files[0].Name()) != ".png" {
		t.Fatal(files, err)
	}
}

func TestStandaloneRunOnlyDownloadsConfirmed403(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/blocked" {
			w.WriteHeader(403)
		} else {
			w.WriteHeader(200)
		}
	}))
	defer srv.Close()
	browser := &fakeBrowser{data: testPNG(t)}
	summary, err := Run(context.Background(), []Item{{URL: srv.URL + "/ok"}, {URL: srv.URL + "/blocked"}}, t.TempDir(), DefaultConfig(), srv.Client(), browser, nil)
	if err != nil || summary.Skipped != 1 || summary.Downloaded != 1 || len(browser.calls) != 1 {
		t.Fatal(summary, browser.calls, err)
	}
}
