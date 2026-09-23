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

func opts() Options { return Options{PriceMultiplier: 1} }

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
	if c[priceCol] != "500" || c[idx("*长（cm）")] != "10" || c[idx("*重量（g）")] != "100" || c[idx("*产品素材图")] != "https://a/1" {
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
	// 店小秘轮播图只用商品图，不并入变种图。
	if rows[1][idx("*轮播图")] != "https://a/1" {
		t.Fatal(rows[1][idx("*轮播图")])
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
	if a[idx("*重量（g）")] != "100" || c[idx("*重量（g）")] != "250" || a[idx("库存")] != "7" || a[idx("识别码")] != "123" {
		t.Fatal(rows)
	}
	if !strings.Contains(strings.Join(msgs, "|"), "缺少识别码类型") {
		t.Fatal(msgs)
	}
}

func TestValidate(t *testing.T) {
	if (Options{}).Validate() == nil {
		t.Fatal("price_multiplier 为 0 应报错")
	}
	if (Options{PriceMultiplier: 1, Defaults: map[string]string{"不存在": "1"}}).Validate() == nil {
		t.Fatal("未知字段应报错")
	}
}

func TestWorkbookPreservesExampleAndText(t *testing.T) {
	tpl, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	row := make([]string, len(Headers))
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
