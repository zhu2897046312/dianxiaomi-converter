package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tu "dianxiaomi-converter/internal/testutil"
)

func TestDefaultCurrencyInExportedWorkbook(t *testing.T) {
	legacy := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Variant SKU": "S中文-1", "Variant Price": "19.99", "Option1 Name": "Color", "Option1 Value": "Red", "Image Src": "https://a/1"})
	collection := tu.CSV([]string{"URL handle", "Title", "SKU", "Option1 name", "Option1 value", "Product image URL", "Price"},
		tu.Row{"URL handle": "p", "Title": "T", "SKU": "S中文-1", "Price": "19.99", "Option1 name": "Color", "Option1 value": "Red", "Product image URL": "https://a/1"})
	for _, input := range [][]byte{legacy, collection} {
		cfg, err := configForInput(input, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, enabled := range []bool{true, false} {
			cfg.Dianxiaomi.CurrencyConversion.Enabled = enabled
			out, _, err := convert(input, cfg, "dianxiaomi", nil)
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
			if err != nil {
				t.Fatal(err)
			}
			want := "139.93"
			if !enabled {
				want = "19.99"
			}
			if !strings.Contains(string(xml), `<c r="J2" t="n"><v>`+want+`</v></c>`) {
				t.Fatalf("export missing price %s", want)
			}
			if !strings.Contains(string(xml), `<c r="K2" t="inlineStr"><is><t xml:space="preserve">S-1</t></is></c>`) {
				t.Fatal("exported SKU still contains Chinese characters")
			}
			if !strings.Contains(string(xml), `<c r="X2" t="n"><v>19.99</v></c>`) {
				t.Fatal("export missing raw USD suggested price")
			}
			if !strings.Contains(string(xml), `<c r="Z2" t="n"><v>9</v></c>`) {
				t.Fatal("export missing default shipping lead time")
			}
			if !strings.Contains(string(xml), `<c r="AY2" t="inlineStr"><is><t xml:space="preserve">中国-广东省</t></is></c>`) {
				t.Fatal("export missing default origin")
			}
		}
	}
}

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
		"dianxiaomi": {"remove_chinese_in_sku": false},
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
	if cfg.Dianxiaomi.RemoveChineseInSKU == nil || *cfg.Dianxiaomi.RemoveChineseInSKU {
		t.Fatal("remove_chinese_in_sku=false 未生效")
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

func TestResultDirectoryAvoidsExistingRuns(t *testing.T) {
	input := filepath.Join(t.TempDir(), "shopify.csv")
	first, err := nextResultDir(input)
	if err != nil || !strings.HasSuffix(first, "shopify_转换结果") {
		t.Fatal(first, err)
	}
	if err := os.Mkdir(first, 0755); err != nil {
		t.Fatal(err)
	}
	second, err := nextResultDir(input)
	if err != nil || !strings.HasSuffix(second, "shopify_转换结果_1") {
		t.Fatal(second, err)
	}
}

func TestEmbeddedTemplate(t *testing.T) {
	b := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Option1 Name": "Color", "Option1 Value": "A", "Variant SKU": "S", "Image Src": "https://a/1"})
	out, _, err := convert(b, defaultConfig(), "dianxiaomi", nil)
	if err != nil || !bytes.HasPrefix(out, []byte("PK")) {
		t.Fatal(err)
	}
}

func TestExampleConfigValid(t *testing.T) {
	for _, path := range []string{"config.example.json", "config.dianxiaomi.json", "config.shopify-collection.json"} {
		cfg, err := loadConfig(path)
		if err != nil {
			t.Fatal(path, err)
		}
		if err = cfg.validate(); err != nil {
			t.Fatal(path, err)
		}
	}
}

func TestAutoDetectCollectionConfig(t *testing.T) {
	b := tu.CSV([]string{"Title", "URL handle", "Description", "Status", "SKU", "Option1 name", "Option1 value", "Price", "Inventory quantity", "Product image URL", "Variant image URL"},
		tu.Row{"Title": "商品", "URL handle": "p", "Description": "<p>说明</p>", "Status": "ACTIVE", "SKU": "001", "Option1 name": "Color", "Option1 value": "Black", "Price": "19.99", "Inventory quantity": "1000", "Product image URL": "https://a/1", "Variant image URL": "https://a/2"},
		tu.Row{"URL handle": "p", "SKU": "002", "Option1 value": "White", "Price": "20"})
	for _, input := range [][]byte{b, bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))} {
		cfg, err := configForInput(input, "")
		if err != nil {
			t.Fatal(err)
		}
		out, report, err := convert(input, cfg, "medusa", nil)
		if err != nil {
			t.Fatal(err)
		}
		col, rows := readCSV(t, out)
		if report.Products != 1 || report.Variants != 2 || rows[0][col["Variant Price USD"]] != "19.99" || rows[0][col["Product Description"]] != "<p>说明</p>" || rows[1][col["Variant Option 1 Name"]] != "Color" {
			t.Fatal(rows, report)
		}
		if _, report, err = convert(input, cfg, "dianxiaomi", nil); err != nil || report.Variants != 2 {
			t.Fatal(err, report)
		}
	}
	// 店小秘专用配置不重复 source_fields，叠加后仍保留自动识别的采集 CSV 字段。
	cfg, err := configForInput(b, "config.dianxiaomi.json")
	if err != nil || cfg.SourceFields.Handle != "URL handle" || cfg.Dianxiaomi.Defaults["发货时效（天）"] != "9" || cfg.Dianxiaomi.SuggestedPrice.SourceCurrency != "USD" {
		t.Fatal(cfg, err)
	}
	// 显式配置不能被自动识别覆盖，即使文件与配置不匹配。
	cfg, err = configForInput(b, "config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := convert(b, cfg, "medusa", nil); err == nil {
		t.Fatal("explicit configuration was ignored")
	}
	legacy := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Variant SKU": "001", "Option1 Name": "Color", "Option1 Value": "Red", "Image Src": "https://a/1"})
	cfg, err = configForInput(legacy, "")
	if err != nil || cfg.SourceFields.Handle != "Handle" {
		t.Fatal(cfg, err)
	}
	if _, _, err := convert(legacy, cfg, "dianxiaomi", nil); err != nil {
		t.Fatal(err)
	}
}
