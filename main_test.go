package main

import (
	"archive/zip"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Shopify 官方商品导出 CSV 中本工具会读取的列（真实导出还有更多无关列）。
var shopifyHeader = []string{"Handle", "Title", "Body (HTML)", "Vendor",
	"Option1 Name", "Option1 Value", "Option2 Name", "Option2 Value", "Option3 Name", "Option3 Value",
	"Variant SKU", "Variant Grams", "Variant Inventory Qty", "Variant Price", "Variant Barcode",
	"Image Src", "Image Position", "Variant Image"}

// csvOf 按表头生成 CSV，每行用 map 只写需要的列，便于看清测试意图。
func csvOf(header []string, rows ...map[string]string) []byte {
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Write(header)
	for _, r := range rows {
		line := make([]string, len(header))
		for i, h := range header {
			line[i] = r[h]
		}
		w.Write(line)
	}
	w.Flush()
	return []byte("\ufeff" + b.String())
}

func shopify(rows ...map[string]string) []byte { return csvOf(shopifyHeader, rows...) }

// imageRow 构造 Shopify 纯图片行：只有 Handle、Image Src、Image Position。
func imageRow(handle, url, pos string) map[string]string {
	return map[string]string{"Handle": handle, "Image Src": url, "Image Position": pos}
}

func idx(name string) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	panic("未知列 " + name)
}

func mustConvert(t *testing.T, b []byte, cfg Config) ([][]string, Report) {
	t.Helper()
	rows, r, err := convert(b, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return rows, r
}

// Case 1：2 个 SKU + 6 张图片，后 4 张在纯图片行上。
func TestShopifyTwoSKUsSixImages(t *testing.T) {
	b := shopify(
		map[string]string{"Handle": "p", "Title": "T", "Body (HTML)": "<p>d</p>", "Option1 Name": "quantity", "Option1 Value": "3pcs",
			"Variant SKU": "S1", "Variant Price": "23.99", "Image Src": "https://a/1", "Image Position": "1"},
		map[string]string{"Handle": "p", "Option1 Name": "quantity", "Option1 Value": "6pcs",
			"Variant SKU": "S2", "Variant Price": "33.99", "Image Src": "https://a/2", "Image Position": "2"},
		imageRow("p", "https://a/3", "3"),
		imageRow("p", "https://a/4", "4"),
		imageRow("p", "https://a/5", "5"),
		imageRow("p", "https://a/6", "6"),
	)
	rows, r := mustConvert(t, b, defaultConfig())
	if r.Products != 1 || r.Variants != 2 {
		t.Fatalf("期望 1 商品 2 SKU，得到 %+v", r)
	}
	want := "https://a/1\nhttps://a/2\nhttps://a/3\nhttps://a/4\nhttps://a/5\nhttps://a/6"
	for i, row := range rows {
		if got := row[idx("*轮播图")]; got != want {
			t.Fatalf("SKU %d 轮播图错误：%q", i, got)
		}
	}
	if rows[1][idx("*产品标题")] != "T" || rows[1][idx("产品描述")] != "<p>d</p>" || rows[1][idx("产品货号")] != "p" {
		t.Fatal("变种行未继承商品级标题/描述/货号", rows[1])
	}
	if rows[0][idx("SKU货号")] != "S1" || rows[1][idx("*变种属性值一")] != "6pcs" || rows[1][idx("*申报价格\n(店铺币种)")] != "33.99" {
		t.Fatal(rows)
	}
}

// Case 2：重复 URL 只保留一次，且按 Image Position 排序而非 CSV 行序。
func TestImagesDeduplicatedAndSorted(t *testing.T) {
	b := shopify(
		map[string]string{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Image Src": "https://a/2", "Image Position": "3"},
		imageRow("p", "https://a/1", "1"),
		imageRow("p", "https://a/1", "2"),
		imageRow("p", "", "4"),
	)
	rows, _ := mustConvert(t, b, defaultConfig())
	if got := rows[0][idx("*轮播图")]; got != "https://a/1\nhttps://a/2" {
		t.Fatalf("%q", got)
	}
}

// 非法或空的 Image Position 不能 panic，排在有效位置之后并保持原顺序。
func TestInvalidImagePosition(t *testing.T) {
	b := shopify(
		map[string]string{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Image Src": "https://a/x", "Image Position": "abc"},
		imageRow("p", "https://a/y", ""),
		imageRow("p", "https://a/1", "1"),
	)
	rows, _ := mustConvert(t, b, defaultConfig())
	if got := rows[0][idx("*轮播图")]; got != "https://a/1\nhttps://a/x\nhttps://a/y" {
		t.Fatalf("%q", got)
	}
}

// Case 3：12 张图片只输出前 10 张，并在报告中警告一次。
func TestMoreThanTenImages(t *testing.T) {
	rs := []map[string]string{
		{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Image Src": "https://a/1", "Image Position": "1"},
		{"Handle": "p", "Option1 Value": "B", "Variant SKU": "S2"},
	}
	for i := 2; i <= 12; i++ {
		rs = append(rs, imageRow("p", fmt.Sprintf("https://a/%d", i), fmt.Sprint(i)))
	}
	rows, r := mustConvert(t, shopify(rs...), defaultConfig())
	for _, row := range rows {
		imgs := strings.Split(row[idx("*轮播图")], "\n")
		if len(imgs) != 10 || imgs[0] != "https://a/1" || imgs[9] != "https://a/10" {
			t.Fatal(imgs)
		}
	}
	n := 0
	for _, is := range r.Issues {
		if strings.Contains(is.Message, "仅保留前 10 张") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("超限警告应出现 1 次，实际 %d 次：%+v", n, r.Issues)
	}
}

// Case 4：没有 Variant Image 时，预览图和素材图取轮播图第一张；有则优先用变种图。
func TestPreviewFallback(t *testing.T) {
	b := shopify(
		map[string]string{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Image Src": "https://a/2", "Image Position": "2"},
		map[string]string{"Handle": "p", "Option1 Value": "B", "Variant SKU": "S2", "Variant Image": "https://v/b"},
		imageRow("p", "https://a/1", "1"),
	)
	rows, _ := mustConvert(t, b, defaultConfig())
	if rows[0][idx("预览图")] != "https://a/1" || rows[0][idx("*产品素材图")] != "https://a/1" {
		t.Fatal(rows[0])
	}
	if rows[1][idx("预览图")] != "https://v/b" || rows[1][idx("*产品素材图")] != "https://v/b" {
		t.Fatal(rows[1])
	}
}

// Case 5：另一种来源 CSV，仅通过配置文件改列名即可识别。
func TestCustomSourceFields(t *testing.T) {
	header := []string{"Title", "URL handle", "SKU", "Option1 value", "Product image URL", "Image position",
		"Option1 Name", "Variant Price"}
	b := csvOf(header,
		map[string]string{"Title": "T", "URL handle": "p", "SKU": "001", "Option1 value": "Black", "Option1 Name": "Color",
			"Product image URL": "https://a/2", "Image position": "2", "Variant Price": "12.5"},
		map[string]string{"URL handle": "p", "SKU": "002", "Option1 value": "White"},
		map[string]string{"URL handle": "p", "Product image URL": "https://a/1", "Image position": "1"},
	)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	json := `{"price_multiplier": 2, "source_fields": {"handle": "URL handle", "sku": "SKU", "option1_value": "Option1 value",
		"image_url": "Product image URL", "image_position": "Image position"}}`
	if err := os.WriteFile(cfgPath, []byte(json), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	// 未配置的字段必须保留 Shopify 默认值，而不是被 JSON 清空。
	if cfg.SourceFields.Title != "Title" || cfg.SourceFields.Price != "Variant Price" || cfg.SourceFields.Handle != "URL handle" {
		t.Fatalf("%+v", cfg.SourceFields)
	}
	rows, r := mustConvert(t, b, cfg)
	if r.Products != 1 || r.Variants != 2 {
		t.Fatalf("%+v", r)
	}
	if rows[0][idx("*轮播图")] != "https://a/1\nhttps://a/2" || rows[1][idx("SKU货号")] != "002" ||
		rows[0][idx("*申报价格\n(店铺币种)")] != "25.00" || rows[1][idx("*变种属性名称一")] != "颜色" {
		t.Fatal(rows)
	}
}

// Case 6：标准 Shopify CSV、不提供任何配置即可转换；纯图片行不输出为 SKU。
func TestShopifyWithoutConfig(t *testing.T) {
	b := shopify(
		map[string]string{"Handle": "p", "Title": "T", "Option1 Name": "Color", "Option1 Value": "Red", "Option2 Name": "Size", "Option2 Value": "L",
			"Variant SKU": "00123", "Variant Grams": "0", "Variant Inventory Qty": "5", "Variant Barcode": "123",
			"Image Src": "https://a/1", "Image Position": "1"},
		// Shopify 只在第一行写选项名称，后续变种行为空。
		map[string]string{"Handle": "p", "Option1 Value": "Blue", "Option2 Value": "M", "Variant SKU": "00124", "Variant Grams": "250"},
		imageRow("p", "https://a/2", "2"),
	)
	rows, r := mustConvert(t, b, defaultConfig())
	if r.Variants != 2 {
		t.Fatalf("%+v", r)
	}
	a, c := rows[0], rows[1]
	if a[idx("SKU货号")] != "00123" || a[idx("库存")] != "5" || a[idx("识别码")] != "123" {
		t.Fatal(a)
	}
	if c[idx("*变种属性名称一")] != "颜色" || c[idx("变种属性名称二")] != "尺寸" || c[idx("变种属性值二")] != "M" {
		t.Fatal("后续变种行未回退到商品级选项名称", c)
	}
	// 重量 0 视为缺失，使用临时默认值；非零重量原样保留。
	if a[idx("*重量（g）")] != "100" || c[idx("*重量（g）")] != "250" {
		t.Fatal(a[idx("*重量（g）")], c[idx("*重量（g）")])
	}
	// 无价格时使用临时默认值。
	if a[idx("*申报价格\n(店铺币种)")] != "500" || a[idx("*长（cm）")] != "10" {
		t.Fatal(a)
	}
}

// 必需字段必须按生效后的 SourceFields 校验，报错中带出配置键便于定位。
func TestRequiredColumnsFollowConfig(t *testing.T) {
	b := shopify(map[string]string{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S", "Image Src": "https://a/1"})
	cfg := defaultConfig()
	cfg.SourceFields.ImageURL = "Product image URL"
	_, _, err := convert(b, cfg)
	if err == nil || !strings.Contains(err.Error(), "source_fields.image_url") {
		t.Fatal(err)
	}
	// 可选列缺失不阻断转换。
	cfg = defaultConfig()
	cfg.SourceFields.Barcode = "不存在的列"
	cfg.SourceFields.ImagePosition = ""
	if _, _, err = convert(b, cfg); err != nil {
		t.Fatal(err)
	}
}

// 旧版 price_column 仍可用且优先于 source_fields.price；显式指定但不存在时报错。
func TestLegacyPriceColumn(t *testing.T) {
	header := append(append([]string{}, shopifyHeader...), "Cost")
	b := csvOf(header, map[string]string{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S",
		"Variant Price": "99", "Cost": "12.50", "Image Src": "https://a/1"})
	cfg := defaultConfig()
	cfg.PriceColumn = "Cost"
	cfg.PriceMultiplier = 2
	rows, _ := mustConvert(t, b, cfg)
	if rows[0][idx("*申报价格\n(店铺币种)")] != "25.00" {
		t.Fatal(rows[0])
	}
	cfg.PriceColumn = "Nope"
	if _, _, err := convert(b, cfg); err == nil {
		t.Fatal("显式 price_column 不存在应报错")
	}
}

func TestThirdOptionRejected(t *testing.T) {
	b := shopify(map[string]string{"Handle": "p", "Title": "T", "Option1 Value": "Red", "Option3 Name": "Material", "Option3 Value": "Cotton", "Variant SKU": "a", "Image Src": "https://a/1"})
	if _, _, err := convert(b, defaultConfig()); err == nil {
		t.Fatal("third option silently lost")
	}
}

func TestExplicitPricingAndOverrides(t *testing.T) {
	b := shopify(map[string]string{"Handle": "p", "Title": "T", "Option1 Value": "Red", "Variant SKU": "a", "Image Src": "https://a/1"})
	cfg := defaultConfig()
	cfg.Defaults = map[string]string{"*长（cm）": "10"}
	cfg.Overrides = map[string]map[string]string{"a": {"*长（cm）": "20"}}
	r, _ := mustConvert(t, b, cfg)
	if r[0][9] != "500" || r[0][11] != "20" {
		t.Fatal(r)
	}
}

func TestImageOnlyProductRejected(t *testing.T) {
	b := shopify(map[string]string{"Handle": "p", "Title": "T", "Image Src": "https://a/1"})
	if _, _, err := convert(b, defaultConfig()); err == nil || !strings.Contains(err.Error(), "没有变种") {
		t.Fatal(err)
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
