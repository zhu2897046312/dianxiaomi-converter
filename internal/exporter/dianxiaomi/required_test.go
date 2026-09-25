package dianxiaomi

import (
	"os"
	"strings"
	"testing"

	tu "dianxiaomi-converter/internal/testutil"
)

func validRow(t *testing.T) []string {
	rows, _ := export(t, tu.Shopify(tu.Row{"Handle": "p", "Title": "Product", "Variant SKU": "A", "Option1 Name": "Color", "Option1 Value": "Red", "Image Src": "https://a/1"}), opts())
	return rows[0]
}

func TestWorkbookRejectsEveryEmptyRequiredField(t *testing.T) {
	tpl, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range Headers {
		if !strings.HasPrefix(name, "*") && name != "产地" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			row := validRow(t)
			row[i] = " \n\t"
			b, err := Workbook(tpl, [][]string{row})
			if err == nil || b != nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("%v", err)
			}
		})
	}
	for _, value := range []string{"0", "-1", "NaN", "Inf", "bad"} {
		row := validRow(t)
		row[9] = value
		if _, err := Workbook(tpl, [][]string{row}); err == nil {
			t.Fatal(value)
		}
	}
}

func TestFinalImageFallbackAfterOverrides(t *testing.T) {
	for _, fields := range []map[string]string{
		{"*产品素材图": "", "预览图": "", "*轮播图": ""},
		{"*产品素材图": " \t", "预览图": "https://a/new", "*轮播图": "https://a/new\nhttps://a/new"},
	} {
		o := opts()
		o.Overrides = map[string]map[string]string{"A": fields}
		rows, _ := export(t, tu.Shopify(tu.Row{"Handle": "p", "Title": "P", "Variant SKU": "A", "Option1 Name": "Color", "Option1 Value": "Red", "Image Src": "https://a/1"}), o)
		if err := validateRequiredRows(rows); err != nil {
			t.Fatal(err)
		}
		if rows[0][19] != rows[0][8] || strings.Contains(rows[0][18], "\n") {
			t.Fatal(rows)
		}
	}
}
