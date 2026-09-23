package main

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tu "dianxiaomi-converter/internal/testutil"
)

func writeConfig(t *testing.T, json string) Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(json), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.validate(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func readCSV(t *testing.T, b []byte) (map[string]int, [][]string) {
	t.Helper()
	all, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	col := map[string]int{}
	for i, h := range all[0] {
		col[h] = i
	}
	return col, all[1:]
}

// 配置只覆盖写出的键：source_fields、medusa.status_map、medusa.defaults 未写的项保持默认。
func TestPartialConfigKeepsDefaults(t *testing.T) {
	cfg := writeConfig(t, `{
		"source_fields": {"image_url": "Product image URL"},
		"medusa": {"status_map": {"archived": "rejected"}, "defaults": {"Shipping Profile Id": "sp_1"}}
	}`)
	if cfg.SourceFields.ImageURL != "Product image URL" || cfg.SourceFields.Handle != "Handle" || cfg.SourceFields.Status != "Status" {
		t.Fatalf("%+v", cfg.SourceFields)
	}
	m := cfg.Medusa
	if m.PriceCurrency != "USD" || m.StatusMap["active"] != "published" || m.StatusMap["archived"] != "rejected" ||
		m.Defaults["Product Discountable"] != "TRUE" || m.Defaults["Shipping Profile Id"] != "sp_1" {
		t.Fatalf("%+v", m)
	}
}

// 旧版顶层配置继续生效；新的 dianxiaomi 块同键优先。
func TestLegacyAndDianxiaomiConfigMerge(t *testing.T) {
	cfg := writeConfig(t, `{
		"price_multiplier": 2,
		"defaults": {"*长（cm）": "11", "*宽（cm）": "12"},
		"sku_overrides": {"a": {"*高（cm）": "13"}},
		"dianxiaomi": {"defaults": {"*宽（cm）": "22"}}
	}`)
	o := cfg.dianxiaomiOptions()
	if o.PriceMultiplier != 2 || o.Defaults["*长（cm）"] != "11" || o.Defaults["*宽（cm）"] != "22" || o.Overrides["a"]["*高（cm）"] != "13" {
		t.Fatalf("%+v", o)
	}
	cfg = writeConfig(t, `{"price_multiplier": 2, "dianxiaomi": {"price_multiplier": 3}}`)
	if cfg.dianxiaomiOptions().PriceMultiplier != 3 {
		t.Fatal("dianxiaomi.price_multiplier 应优先")
	}
}

// 旧版 price_column 优先于 source_fields.price，两个目标都从同一价格字段读取。
func TestLegacyPriceColumn(t *testing.T) {
	header := append(append([]string{}, tu.ShopifyHeader...), "Cost")
	b := tu.CSV(header, tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S", "Variant Price": "99", "Cost": "12.50"})
	cfg := writeConfig(t, `{"price_column": "Cost"}`)
	out, _, err := convert(b, cfg, "medusa", nil)
	if err != nil {
		t.Fatal(err)
	}
	col, rows := readCSV(t, out)
	if rows[0][col["Variant Price USD"]] != "12.50" {
		t.Fatal(rows[0])
	}
	cfg = writeConfig(t, `{"price_column": "Nope"}`)
	if _, _, err = convert(b, cfg, "dianxiaomi", nil); err == nil {
		t.Fatal("显式 price_column 不存在应报错")
	}
}

// Case 10：自定义来源列名，仅靠配置即可同时用于两个目标。
func TestCustomSourceFieldsBothTargets(t *testing.T) {
	header := []string{"Title", "URL handle", "SKU", "Option1 name", "Option1 value", "Product image URL", "Image position", "Price"}
	b := tu.CSV(header,
		tu.Row{"Title": "T", "URL handle": "p", "SKU": "001", "Option1 name": "Color", "Option1 value": "Black",
			"Product image URL": "https://a/2", "Image position": "2", "Price": "12.5"},
		tu.Row{"URL handle": "p", "SKU": "002", "Option1 value": "White"},
		tu.Row{"URL handle": "p", "Product image URL": "https://a/1", "Image position": "1"},
	)
	cfg := writeConfig(t, `{"source_fields": {"handle": "URL handle", "sku": "SKU", "option1_name": "Option1 name",
		"option1_value": "Option1 value", "image_url": "Product image URL", "image_position": "Image position", "price": "Price"}}`)

	out, rep, err := convert(b, cfg, "medusa", nil)
	if err != nil {
		t.Fatal(err)
	}
	col, rows := readCSV(t, out)
	if rep.Products != 1 || len(rows) != 2 || rows[1][col["Variant SKU"]] != "002" || rows[1][col["Variant Option 1 Name"]] != "Color" ||
		rows[0][col["Product Image 1 Url"]] != "https://a/1" || rows[0][col["Variant Price USD"]] != "12.5" {
		t.Fatal(rows)
	}
	// 来源 CSV 没有 Status 列：按 draft 导入并提示。
	if rows[0][col["Product Status"]] != "draft" || len(rep.Issues) != 1 {
		t.Fatal(rows[0], rep.Issues)
	}

	if _, rep, err = convert(b, cfg, "dianxiaomi", nil); err != nil || rep.Variants != 2 || rep.Target != "dianxiaomi" {
		t.Fatal(err, rep)
	}
}

// 同一份含三组属性的数据：Medusa 正常导出，店小秘按原规则拒绝。
func TestThirdOptionTargetSpecific(t *testing.T) {
	b := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Option1 Name": "Color", "Option1 Value": "Red", "Option2 Name": "Size", "Option2 Value": "L",
		"Option3 Name": "Qty", "Option3 Value": "3", "Variant SKU": "S"})
	if _, _, err := convert(b, defaultConfig(), "medusa", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := convert(b, defaultConfig(), "dianxiaomi", nil); err == nil {
		t.Fatal("店小秘应拒绝第三组属性")
	}
	if _, _, err := convert(b, defaultConfig(), "unknown", nil); err == nil {
		t.Fatal("未知 target 应报错")
	}
}

func TestAutoOutputAvoidsExistingFiles(t *testing.T) {
	input := filepath.Join(t.TempDir(), "采集 任务.csv")
	for _, tg := range targets {
		first, err := nextOutput(input, tg.suffix, tg.ext)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(first, tg.suffix+tg.ext) {
			t.Fatal(first)
		}
		if err = os.WriteFile(first+".report.json", []byte("{}"), 0644); err != nil {
			t.Fatal(err)
		}
		second, err := nextOutput(input, tg.suffix, tg.ext)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(second, tg.suffix+"_1"+tg.ext) {
			t.Fatal(second)
		}
	}
}

func TestEmbeddedTemplate(t *testing.T) {
	b := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S", "Image Src": "https://a/1"})
	out, _, err := convert(b, defaultConfig(), "dianxiaomi", nil)
	if err != nil || !bytes.HasPrefix(out, []byte("PK")) {
		t.Fatal(err)
	}
}

func TestExampleConfigValid(t *testing.T) {
	cfg, err := loadConfig("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.validate(); err != nil {
		t.Fatal(err)
	}
}
