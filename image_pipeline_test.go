package main

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"dianxiaomi-converter/internal/exporter/dianxiaomi"
	"dianxiaomi-converter/internal/exporter/medusa"
	"dianxiaomi-converter/internal/model"
	"dianxiaomi-converter/internal/source/shopify"
	tu "dianxiaomi-converter/internal/testutil"
)

func TestSharedFilterProtectsBothExportsAndConfigOverrides(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		if r.URL.Path == "/forbidden" {
			w.WriteHeader(403)
		} else {
			w.WriteHeader(200)
		}
	}))
	defer s.Close()
	bad, good := s.URL+"/forbidden", s.URL+"/good"
	input := tu.Shopify(
		tu.Row{"Handle": "p", "Title": "Product", "Variant SKU": "A", "Option1 Name": "Color", "Option1 Value": "Red", "Image Src": bad, "Variant Image": bad, "Body (HTML)": `<p>Keep description</p><img src="` + bad + `"><img src="` + good + `"><img src="` + good + `">`},
		tu.Row{"Handle": "p", "Variant SKU": "B", "Option1 Value": "Blue", "Image Src": good, "Variant Image": good},
		tu.Row{"Handle": "p", "Image Src": good},
	)
	products, err := shopify.Parse(input, shopify.DefaultFields())
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.Dianxiaomi.Defaults = map[string]string{"外包装图片": bad}
	cfg.Dianxiaomi.Overrides = map[string]map[string]string{"A": {"*产品素材图": bad, "*轮播图": good + "\n" + good + "\n" + bad}}
	report, err := filterProducts(context.Background(), products, &cfg, s.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Checked != 2 || report.Removed != 1 || counts["/good"] != 1 || counts["/forbidden"] != 1 {
		t.Fatalf("%+v %v", report, counts)
	}
	rows, _, err := dianxiaomi.Export(products, cfg.dianxiaomiOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if strings.Contains(strings.Join(row, "|"), bad) {
			t.Fatal("403 leaked into dianxiaomi", row)
		}
		if row[18] != good || row[8] != good || row[19] != good {
			t.Fatalf("carousel/preview %q %q", row[18], row[8])
		}
	}
	out, _, err := exportDianxiaomi(products, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	f, err := z.Open("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	xml, err := io.ReadAll(f)
	f.Close()
	if err != nil || bytes.Contains(xml, []byte("/forbidden")) {
		t.Fatal("403 leaked into XLSX", err)
	}
	header, mrows, _ := medusa.Export(products, cfg.Medusa)
	col := map[string]int{}
	for i, h := range header {
		col[h] = i
	}
	for _, row := range mrows {
		if strings.Contains(strings.Join(row, "|"), bad) {
			t.Fatal("403 leaked into Medusa")
		}
		if row[col["Product Image 1 Url"]] != good || row[col["Product Image 2 Url"]] != "" {
			t.Fatal("duplicate Medusa image")
		}
		if row[col["Product Thumbnail"]] != good || strings.Count(row[col["Product Description"]], good) != 1 {
			t.Fatal("thumbnail/description filtering failed")
		}
	}
}

func TestAllBlockedImagesRetainOneOnlyForDianxiaomi(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer s.Close()
	products := []model.Product{{Handle: "p", Title: "P", Images: []model.ProductImage{{URL: s.URL}, {URL: s.URL + "/second"}}, Variants: []model.Variant{{SKU: "A", Image: s.URL, Options: []model.Option{{Name: "Color", Value: "Red"}}}, {SKU: "B", Image: s.URL + "/second", Options: []model.Option{{Name: "Color", Value: "Blue"}}}}}}
	cfg := defaultConfig()
	_, err := filterProducts(context.Background(), products, &cfg, s.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rows, report, err := dianxiaomi.Export(products, cfg.dianxiaomiOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0][8] != s.URL || rows[0][18] != s.URL || rows[0][19] != s.URL || rows[1][18] != s.URL || len(report.Issues) == 0 {
		t.Fatal(rows, report)
	}
	if _, _, err := exportDianxiaomi(products, cfg, nil); err != nil {
		t.Fatal(err)
	}
	h, rs, _ := medusa.Export(products, cfg.Medusa)
	if len(rs) != 2 {
		t.Fatal("product dropped")
	}
	for i, k := range h {
		if (strings.HasPrefix(k, "Product Image ") || k == "Product Thumbnail") && rs[0][i] != "" {
			t.Fatal("blocked image restored")
		}
	}
}

func TestBlockedDescriptionGIFIsReplacedOnlyForDianxiaomi(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/motion.gif" || r.URL.Path == "/blocked.jpg" {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(200)
	}))
	defer s.Close()
	good, gif, blocked := s.URL+"/good.jpg", s.URL+"/motion.gif", s.URL+"/blocked.jpg"
	products := []model.Product{{Handle: "p", Title: "P", Description: `<p>Text</p><img src="` + gif + `"><img src="` + blocked + `">`, Images: []model.ProductImage{{URL: good}}, Variants: []model.Variant{{SKU: "A", Options: []model.Option{{Name: "Color", Value: "Red"}}}}}}
	cfg := defaultConfig()
	_, err := filterProducts(context.Background(), products, &cfg, s.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(products[0].Description, gif) || strings.Contains(products[0].Description, blocked) {
		t.Fatal(products[0].Description)
	}
	rows, _, err := dianxiaomi.Export(products, cfg.dianxiaomiOptions())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rows[0][2], gif) || strings.Contains(rows[0][2], blocked) || !strings.Contains(rows[0][2], good) {
		t.Fatal(rows[0][2])
	}
	h, mrows, _ := medusa.Export(products, cfg.Medusa)
	for i, name := range h {
		if name == "Product Description" && (strings.Contains(mrows[0][i], gif) || strings.Contains(mrows[0][i], blocked) || strings.Contains(mrows[0][i], good)) {
			t.Fatal(mrows[0][i])
		}
	}
}
