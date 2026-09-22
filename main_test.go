package main

import (
	"archive/zip"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(rows ...[]string) []byte {
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Write([]string{"Title", "URL handle", "SKU", "Option1 name", "Option1 value", "Product image URL", "Price", "Option3 value", "Image position"})
	w.WriteAll(rows)
	return []byte("\ufeff" + b.String())
}
func TestGroupImagesAndVariants(t *testing.T) {
	b := fixture([]string{"Test", "p", "001", "Color", "Black", "https://a/2", "12.50", "", "2"}, []string{"", "p", "002", "Color", "White", "", "13", "", ""}, []string{"", "p", "", "", "", "https://a/1", "", "", "1"})
	rows, r, e := convert(b, Config{PriceColumn: "Price", PriceMultiplier: 2})
	if e != nil {
		t.Fatal(e)
	}
	if r.Products != 1 || r.Variants != 2 {
		t.Fatalf("%+v", r)
	}
	if rows[1][0] != "Test" || rows[0][9] != "25.00" || rows[0][10] != "001" || rows[0][18] != "https://a/1\nhttps://a/2" || rows[0][4] != "颜色" {
		t.Fatal(rows)
	}
	if rows[0][19] != "https://a/1" || rows[0][11] != "10" || rows[0][12] != "10" || rows[0][13] != "10" || rows[0][14] != "100" {
		t.Fatal("incorrect authorized defaults")
	}
}
func TestThirdOptionRejected(t *testing.T) {
	_, _, e := convert(fixture([]string{"T", "p", "a", "Color", "Red", "", "1", "Cotton", ""}), Config{})
	if e == nil {
		t.Fatal("third option silently lost")
	}
}
func TestExplicitPricingAndOverrides(t *testing.T) {
	b := fixture([]string{"T", "p", "a", "Color", "Red", "", "1", "", ""})
	r, _, e := convert(b, Config{Defaults: map[string]string{"*长（cm）": "10"}, Overrides: map[string]map[string]string{"a": {"*长（cm）": "20"}}})
	if e != nil {
		t.Fatal(e)
	}
	if r[0][9] != "500" || r[0][11] != "20" {
		t.Fatal(r)
	}
}
func TestWorkbookPreservesExampleAndText(t *testing.T) {
	source := "templates/import_created_product_popTemu.xlsx"
	dest := filepath.Join(t.TempDir(), "out.xlsx")
	row := make([]string, len(headers))
	row[0] = "=1+1 & <test>"
	row[10] = "00123"
	if e := writeWorkbook(source, dest, [][]string{row}); e != nil {
		t.Fatal(e)
	}
	read := func(p, name string) string {
		z, e := zip.OpenReader(p)
		if e != nil {
			t.Fatal(e)
		}
		defer z.Close()
		for _, f := range z.File {
			if f.Name == name {
				r, _ := f.Open()
				defer r.Close()
				b, _ := io.ReadAll(r)
				return string(b)
			}
		}
		return ""
	}
	if read(source, "xl/worksheets/sheet2.xml") != read(dest, "xl/worksheets/sheet2.xml") {
		t.Fatal("example changed")
	}
	s := read(dest, "xl/worksheets/sheet1.xml")
	if !strings.Contains(s, "00123") || strings.Contains(s, "<f>") || !strings.Contains(s, "&amp;") {
		t.Fatal(s)
	}
	if e := writeWorkbook(source, dest, [][]string{row}); e == nil {
		t.Fatal("overwrote existing output")
	}
	if _, e := os.Stat(dest); e != nil {
		t.Fatal(e)
	}
}

func TestAutoOutputAvoidsExistingFiles(t *testing.T) {
	input := filepath.Join(t.TempDir(), "采集 任务.csv")
	first, err := nextOutput(input)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(first+".report.json", []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	second, err := nextOutput(input)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasSuffix(second, "_1.xlsx") {
		t.Fatal(second)
	}
}
func TestEmbeddedTemplate(t *testing.T) {
	if err := writeWorkbook("", filepath.Join(t.TempDir(), "out.xlsx"), nil); err != nil {
		t.Fatal(err)
	}
}
