package dianxiaomi

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"dianxiaomi-converter/internal/source/shopify"
	tu "dianxiaomi-converter/internal/testutil"
)

const templatePath = "../../../templates/import_created_product_popTemu.xlsx"

func opts() Options { repeat := false; return Options{PriceMultiplier: 1, RepeatImagesToTen: &repeat} }

func TestNeverRepeatImagesEvenWithLegacyConfig(t *testing.T) {
	for _, legacy := range []*bool{nil, boolPtr(true), boolPtr(false)} {
		b := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Variant SKU": "1", "Image Src": "https://a/1", "Variant Image": "https://a/1"})
		rows, msgs := export(t, b, Options{PriceMultiplier: 1, RepeatImagesToTen: legacy})
		if rows[0][idx("*轮播图")] != "https://a/1" || strings.Contains(strings.Join(msgs, "|"), "重复补齐") {
			t.Fatal(rows, msgs)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

func export(t *testing.T, b []byte, o Options) ([][]string, []string) {
	t.Helper()
	ps, err := shopify.Parse(b, shopify.DefaultFields())
	if err != nil {
		t.Fatal(err)
	}
	rows, r, err := Export(ps, o)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, is := range r.Issues {
		msgs = append(msgs, is.Message)
	}
	return rows, msgs
}

func idx(name string) int {
	for i, h := range Headers {
		if h == name {
			return i
		}
	}
	panic("未知列 " + name)
}

var priceCol = idx("*申报价格\n(店铺币种)")
var suggestedPriceCol = idx("建议售价（USD）")

func TestSKUChineseAndEmojiRemoval(t *testing.T) {
	b := tu.Shopify(
		tu.Row{"Handle": "p", "Title": "T", "Variant SKU": "XL2608-【太阳花】😊_A", "Option1 Value": "A"},
		tu.Row{"Handle": "p", "Variant SKU": "XL2608-【月季】😊_A", "Option1 Value": "B"},
		tu.Row{"Handle": "p", "Variant SKU": "中文", "Option1 Value": "C"},
	)
	rows, msgs := export(t, b, opts())
	if rows[0][idx("SKU货号")] != "XL2608-【】_A" || rows[1][idx("SKU货号")] != "XL2608-【】_A" {
		t.Fatal(rows)
	}
	all := strings.Join(msgs, "|")
	if !strings.Contains(all, "SKU 货号重复") || !strings.Contains(all, "清理中文、Emoji 后为空") {
		t.Fatal(msgs)
	}
	o := opts()
	disabled := false
	o.RemoveChineseInSKU = &disabled
	rows, _ = export(t, b, o)
	if rows[0][idx("SKU货号")] != "XL2608-【太阳花】_A" {
		t.Fatal(rows[0])
	}

	// SKU 覆盖也不能重新带入 Emoji；清理必须发生在覆盖之后。
	o = opts()
	o.Overrides = map[string]map[string]string{"XL2608-【太阳花】😊_A": {"SKU货号": "OV🐶-A"}}
	rows, _ = export(t, b, o)
	if rows[0][idx("SKU货号")] != "OV-A" {
		t.Fatal(rows[0][idx("SKU货号")])
	}
}

// 2 个 SKU + 6 张图：每个 SKU 的轮播图都是 6 张，换行分隔。
func TestCarouselSharedAcrossSKUs(t *testing.T) {
	b := tu.Shopify(
		tu.Row{"Handle": "p", "Title": "T", "Body (HTML)": "<p>d</p>", "Option1 Name": "Color", "Option1 Value": "Red",
			"Variant SKU": "S1", "Variant Price": "12.50", "Image Src": "https://a/1", "Image Position": "1"},
		tu.Row{"Handle": "p", "Option1 Value": "Blue", "Variant SKU": "S2", "Image Src": "https://a/2", "Image Position": "2"},
		tu.ImageRow("p", "https://a/3", "3"), tu.ImageRow("p", "https://a/4", "4"),
		tu.ImageRow("p", "https://a/5", "5"), tu.ImageRow("p", "https://a/6", "6"),
	)
	o := opts()
	o.PriceMultiplier = 2
	rows, _ := export(t, b, o)
	if len(rows) != 2 {
		t.Fatal(len(rows))
	}
	want := "https://a/1\nhttps://a/2\nhttps://a/3\nhttps://a/4\nhttps://a/5\nhttps://a/6"
	for _, row := range rows {
		if row[idx("*轮播图")] != want {
			t.Fatalf("%q", row[idx("*轮播图")])
		}
	}
	a, c := rows[0], rows[1]
	if a[priceCol] != "25.00" || a[idx("*变种属性名称一")] != "颜色" || c[idx("*变种属性名称一")] != "颜色" || c[idx("产品描述")] != "<p>d</p>" {
		t.Fatal(rows)
	}
	// 临时默认值与素材图兜底。
	if c[priceCol] != "500" || c[idx("*长（cm）")] != "10" || c[idx("*重量（g）")] != "100" || c[idx("*产品素材图")] != "https://a/1" || c[idx("发货时效（天）")] != "9" || c[idx("产地")] != "中国-广东省" {
		t.Fatal(c)
	}
}

func TestMoreThanTenImages(t *testing.T) {
	rs := []tu.Row{
		{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1"},
		{"Handle": "p", "Option1 Value": "B", "Variant SKU": "S2"},
	}
	for i := 1; i <= 12; i++ {
		rs = append(rs, tu.ImageRow("p", fmt.Sprintf("https://a/%d", i), fmt.Sprint(i)))
	}
	rows, msgs := export(t, tu.Shopify(rs...), opts())
	for _, row := range rows {
		imgs := strings.Split(row[idx("*轮播图")], "\n")
		if len(imgs) != 10 || imgs[9] != "https://a/10" {
			t.Fatal(imgs)
		}
	}
	if n := strings.Count(strings.Join(msgs, "|"), "仅保留前 10 张"); n != 1 {
		t.Fatalf("超限警告应出现 1 次，实际 %d 次", n)
	}
}

func TestPreviewPrefersVariantImage(t *testing.T) {
	b := tu.Shopify(
		tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Image Src": "https://a/1"},
		tu.Row{"Handle": "p", "Option1 Value": "B", "Variant SKU": "S2", "Variant Image": "https://v/b"},
	)
	rows, _ := export(t, b, opts())
	if rows[0][idx("预览图")] != "https://a/1" || rows[1][idx("预览图")] != "https://v/b" || rows[1][idx("*产品素材图")] != "https://v/b" {
		t.Fatal(rows)
	}
	// 商品图不足十张时追加 SKU 图片。
	if rows[1][idx("*轮播图")] != "https://a/1\nhttps://v/b" {
		t.Fatal(rows[1][idx("*轮播图")])
	}
}

func TestCarouselFilledFromSKUs(t *testing.T) {
	var rs []tu.Row
	for i := 0; i < 12; i++ {
		rs = append(rs, tu.Row{"Handle": "p", "Title": "T", "Variant SKU": fmt.Sprint(i), "Option1 Value": fmt.Sprint(i), "Image Src": "https://a/0", "Variant Image": fmt.Sprintf("https://a/%d", i)})
	}
	rows, _ := export(t, tu.Shopify(rs...), opts())
	for _, row := range rows {
		images := strings.Split(row[idx("*轮播图")], "\n")
		if len(images) != 10 || images[0] != "https://a/0" || images[9] != "https://a/9" {
			t.Fatal(images)
		}
	}
}

func TestCurrencyConversion(t *testing.T) {
	b := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Variant SKU": "001", "Variant Price": "24.99"}, tu.Row{"Handle": "p", "Variant SKU": "002"})
	for _, tc := range []struct {
		enabled                       bool
		currency, want, wantSuggested string
	}{{false, "USD", "24.99", "24.99"}, {true, "USD", "174.93", "24.99"}, {true, "EUR", "249.90", "35.70"}, {true, "GBP", "199.92", "28.56"}, {true, "CNY", "24.99", "3.57"}} {
		o := opts()
		o.CurrencyConversion = CurrencyConversion{Enabled: tc.enabled, SourceCurrency: tc.currency, Rates: map[string]float64{"USD": 7, "EUR": 10, "GBP": 8, "CNY": 1}}
		rows, _ := export(t, b, o)
		if rows[0][priceCol] != tc.want || rows[0][suggestedPriceCol] != tc.wantSuggested || rows[1][priceCol] != "500" || rows[1][suggestedPriceCol] != "" {
			t.Fatal(tc, rows)
		}
		o.Overrides = map[string]map[string]string{"001": {Headers[9]: "99"}}
		rows, _ = export(t, b, o)
		if rows[0][priceCol] != "99" {
			t.Fatal(rows)
		}
	}
	o := opts()
	o.CurrencyConversion = CurrencyConversion{Enabled: true, SourceCurrency: "USD"}
	if o.Validate() == nil {
		t.Fatal("missing rate must fail")
	}
	o.CurrencyConversion.Rates = map[string]float64{"USD": -7}
	if o.Validate() == nil {
		t.Fatal("negative rate must fail")
	}
	o.CurrencyConversion.Rates = map[string]float64{"USD": 7, "EUR": 10}
	o.PriceMultiplier = 2
	rows, _ := export(t, b, o)
	if rows[0][priceCol] != "349.86" {
		t.Fatal(rows)
	}
	o.SuggestedPrice.SourceCurrency = "EUR"
	rows, _ = export(t, b, o)
	if rows[0][suggestedPriceCol] != "35.70" {
		t.Fatal(rows)
	}
	disabled := false
	o.SuggestedPrice.Enabled = &disabled
	rows, _ = export(t, b, o)
	if rows[0][suggestedPriceCol] != "" {
		t.Fatal(rows)
	}
}

// 店小秘只支持两组属性，第三组必须报错停止（Medusa 则允许，见 medusa 包测试）。
func TestThirdOptionRejected(t *testing.T) {
	b := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "Red", "Option2 Value": "L", "Option3 Name": "Material", "Option3 Value": "Cotton", "Variant SKU": "a"})
	ps, err := shopify.Parse(b, shopify.DefaultFields())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = Export(ps, opts()); err == nil || !strings.Contains(err.Error(), "第三变种") {
		t.Fatal(err)
	}
}

func TestDefaultsOverridesAndWeight(t *testing.T) {
	b := tu.Shopify(
		tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "Red", "Variant SKU": "a", "Variant Grams": "0", "Variant Inventory Qty": "7", "Variant Barcode": "123"},
		tu.Row{"Handle": "p", "Option1 Value": "Blue", "Variant SKU": "b", "Variant Grams": "250"},
	)
	o := opts()
	o.Defaults = map[string]string{"*长（cm）": "15"}
	o.Overrides = map[string]map[string]string{"a": {"*长（cm）": "20"}}
	rows, msgs := export(t, b, o)
	a, c := rows[0], rows[1]
	if a[idx("*长（cm）")] != "20" || c[idx("*长（cm）")] != "15" || a[priceCol] != "500" {
		t.Fatal(rows)
	}
	if a[idx("*重量（g）")] != "100" || c[idx("*重量（g）")] != "250" || a[idx("库存")] != "200" || c[idx("库存")] != "200" || a[idx("识别码")] != "123" {
		t.Fatal(rows)
	}
	if !strings.Contains(strings.Join(msgs, "|"), "缺少识别码类型") {
		t.Fatal(msgs)
	}
}

func TestInventoryIsUniformAndConfigurable(t *testing.T) {
	b := tu.Shopify(
		tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "Red", "Variant SKU": "a", "Variant Inventory Qty": "7"},
		tu.Row{"Handle": "p", "Option1 Value": "Blue", "Variant SKU": "b", "Variant Inventory Qty": "999"},
	)
	quantity := 350
	o := opts()
	o.InventoryQuantity = &quantity
	o.Defaults = map[string]string{"库存": "88"}
	o.Overrides = map[string]map[string]string{"a": {"库存": "99"}}
	rows, _ := export(t, b, o)
	for _, row := range rows {
		if row[idx("库存")] != "350" {
			t.Fatal(rows)
		}
	}
	zero := 0
	o.InventoryQuantity = &zero
	rows, _ = export(t, b, o)
	if rows[0][idx("库存")] != "0" || rows[1][idx("库存")] != "0" {
		t.Fatal(rows)
	}
}

func TestValidate(t *testing.T) {
	if (Options{}).Validate() == nil {
		t.Fatal("price_multiplier 为 0 应报错")
	}
	if (Options{PriceMultiplier: 1, Defaults: map[string]string{"不存在": "1"}}).Validate() == nil {
		t.Fatal("未知字段应报错")
	}
	negative := -1
	if (Options{PriceMultiplier: 1, InventoryQuantity: &negative}).Validate() == nil {
		t.Fatal("负库存配置应报错")
	}
}

func TestWorkbookPreservesExampleAndText(t *testing.T) {
	tpl, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	row := validRow(t)
	row[0] = "=1+1 & <test>"
	row[10] = "00123"
	out, err := Workbook(tpl, [][]string{row})
	if err != nil {
		t.Fatal(err)
	}
	read := func(b []byte, name string) string {
		z, e := zip.NewReader(strings.NewReader(string(b)), int64(len(b)))
		if e != nil {
			t.Fatal(e)
		}
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
	if read(tpl, "xl/worksheets/sheet2.xml") != read(out, "xl/worksheets/sheet2.xml") {
		t.Fatal("example changed")
	}
	s := read(out, "xl/worksheets/sheet1.xml")
	if !strings.Contains(s, "00123") || strings.Contains(s, "<f>") || !strings.Contains(s, "&amp;") {
		t.Fatal(s)
	}
}
