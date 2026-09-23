package medusa

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"

	"dianxiaomi-converter/internal/model"
	"dianxiaomi-converter/internal/source/shopify"
	tu "dianxiaomi-converter/internal/testutil"
)

// table 以列名访问导出结果，避免测试依赖列下标。
type table struct {
	header []string
	rows   [][]string
	report model.Report
}

func (tb table) get(row int, col string) string {
	for i, h := range tb.header {
		if h == col {
			return tb.rows[row][i]
		}
	}
	return "<无此列>"
}

func (tb table) has(col string) bool { return tb.get(0, col) != "<无此列>" }

func (tb table) issues() string {
	var s []string
	for _, is := range tb.report.Issues {
		s = append(s, is.Message)
	}
	return strings.Join(s, "|")
}

func export(t *testing.T, o Options, rows ...tu.Row) table {
	t.Helper()
	ps, err := shopify.Parse(tu.Shopify(rows...), shopify.DefaultFields())
	if err != nil {
		t.Fatal(err)
	}
	h, r, rep := Export(ps, o)
	return table{h, r, rep}
}

// Case 1：1 商品 2 变种 6 张图 → 2 行，每行都有同样 6 张图，缩略图为第 1 张，商品级字段逐行重复。
func TestOneRowPerVariantWithAllImages(t *testing.T) {
	tb := export(t, DefaultOptions(),
		tu.Row{"Handle": "p", "Title": "T", "Body (HTML)": "<p>d</p>", "Status": "active", "Option1 Name": "quantity", "Option1 Value": "3pcs",
			"Variant SKU": "S1", "Variant Price": "23.99", "Image Src": "https://a/1", "Image Position": "1"},
		tu.Row{"Handle": "p", "Option1 Value": "6pcs", "Variant SKU": "S2", "Variant Price": "33.99", "Image Src": "https://a/2", "Image Position": "2"},
		tu.ImageRow("p", "https://a/3", "3"), tu.ImageRow("p", "https://a/4", "4"),
		tu.ImageRow("p", "https://a/5", "5"), tu.ImageRow("p", "https://a/6", "6"),
	)
	if len(tb.rows) != 2 || tb.report.Products != 1 || tb.report.Variants != 2 || tb.report.Target != "medusa" {
		t.Fatalf("%+v", tb.report)
	}
	if tb.has("Product Image 7 Url") || !tb.has("Product Image 6 Url") {
		t.Fatal(tb.header)
	}
	for r := range tb.rows {
		for i := 1; i <= 6; i++ {
			if got := tb.get(r, fmt.Sprintf("Product Image %d Url", i)); got != fmt.Sprintf("https://a/%d", i) {
				t.Fatalf("行 %d 图片 %d = %q", r, i, got)
			}
		}
		if tb.get(r, "Product Thumbnail") != "https://a/1" || tb.get(r, "Product Handle") != "p" ||
			tb.get(r, "Product Title") != "T" || tb.get(r, "Product Description") != "<p>d</p>" || tb.get(r, "Product Status") != "published" {
			t.Fatal(tb.rows[r])
		}
	}
	if tb.get(1, "Variant Price USD") != "33.99" || tb.get(1, "Variant Price EUR") != "" || tb.get(1, "Variant SKU") != "S2" ||
		tb.get(0, "Product Discountable") != "TRUE" || tb.get(0, "Product Id") != "" || tb.get(0, "Variant Id") != "" || tb.get(0, "Product Weight") != "" {
		t.Fatal(tb.rows)
	}
	if tb.issues() != "" {
		t.Fatal(tb.issues())
	}
}

// 表头：基础列顺序与官方模板一致，编号 Option 紧跟价格列，图片列在最后。
func TestHeaderLayout(t *testing.T) {
	template := "Product Id,Product Handle,Product Title,Product Subtitle,Product Description,Product Status,Product Thumbnail,Product Weight,Product Length,Product Width,Product Height,Product HS Code,Product Origin Country,Product MID Code,Product Material,Shipping Profile Id,Product Sales Channel 1,Product Collection Id,Product Type Id,Product Tag 1,Product Discountable,Product External Id,Variant Id,Variant Title,Variant SKU,Variant Barcode,Variant Allow Backorder,Variant Manage Inventory,Variant Weight,Variant Length,Variant Width,Variant Height,Variant HS Code,Variant Origin Country,Variant MID Code,Variant Material,Variant Price EUR,Variant Price USD,Variant Option 1 Name,Variant Option 1 Value,Product Image 1 Url,Product Image 2 Url"
	if got := strings.Join(Header(DefaultOptions(), 0, 0), ","); got != template {
		t.Fatalf("最小表头应与模板完全一致：\n%s", got)
	}
	h := strings.Join(Header(DefaultOptions(), 3, 3), ",")
	if !strings.Contains(h, "Variant Price USD,Variant Option 1 Name,Variant Option 1 Value,Variant Option 2 Name,Variant Option 2 Value,Variant Option 3 Name,Variant Option 3 Value,Product Image 1 Url,Product Image 2 Url,Product Image 3 Url") ||
		!strings.HasSuffix(h, "Product Image 3 Url") {
		t.Fatal(h)
	}
}

// Case 2 + Case 3：两组、三组属性；三组属性不报错（店小秘才有两组上限）。
func TestOptionsAndVariantTitle(t *testing.T) {
	tb := export(t, DefaultOptions(),
		tu.Row{"Handle": "p", "Title": "T", "Option1 Name": "Color", "Option1 Value": "Red", "Option2 Name": "Size", "Option2 Value": "XL", "Variant SKU": "S1"},
		tu.Row{"Handle": "q", "Title": "Q", "Option1 Name": "Color", "Option1 Value": "Red", "Option2 Name": "Size", "Option2 Value": "XL",
			"Option3 Name": "Quantity", "Option3 Value": "3pcs", "Variant SKU": "S2"},
	)
	if tb.get(0, "Variant Option 1 Name") != "Color" || tb.get(0, "Variant Option 1 Value") != "Red" ||
		tb.get(0, "Variant Option 2 Name") != "Size" || tb.get(0, "Variant Option 2 Value") != "XL" ||
		tb.get(0, "Variant Option 3 Name") != "" || tb.get(0, "Variant Title") != "Red / XL" {
		t.Fatal(tb.header, tb.rows[0])
	}
	if tb.get(1, "Variant Option 3 Name") != "Quantity" || tb.get(1, "Variant Option 3 Value") != "3pcs" || tb.get(1, "Variant Title") != "Red / XL / 3pcs" {
		t.Fatal(tb.rows[1])
	}
}

// Case 4：Shopify 单规格占位 Title/Default Title 不导出为属性，标题为 Default。
func TestDefaultTitleVariant(t *testing.T) {
	tb := export(t, DefaultOptions(),
		tu.Row{"Handle": "p", "Title": "T", "Option1 Name": "Title", "Option1 Value": "Default Title", "Variant SKU": "S1"})
	if tb.get(0, "Variant Title") != "Default" || tb.get(0, "Variant Option 1 Name") != "" || tb.get(0, "Variant Option 1 Value") != "" {
		t.Fatal(tb.rows[0])
	}
}

// Case 5 + Case 6：Inventory Policy / Tracker 转布尔值，输出大写 TRUE/FALSE。
func TestInventoryBooleans(t *testing.T) {
	tb := export(t, DefaultOptions(),
		tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Variant Inventory Policy": "continue", "Variant Inventory Tracker": "shopify"},
		tu.Row{"Handle": "p", "Option1 Value": "B", "Variant SKU": "S2", "Variant Inventory Policy": "deny", "Variant Inventory Qty": "10"},
		tu.Row{"Handle": "p", "Option1 Value": "C", "Variant SKU": "S3"},
	)
	want := [][2]string{{"TRUE", "TRUE"}, {"FALSE", "FALSE"}, {"FALSE", "FALSE"}}
	for i, w := range want {
		if tb.get(i, "Variant Allow Backorder") != w[0] || tb.get(i, "Variant Manage Inventory") != w[1] {
			t.Fatal(i, tb.rows[i])
		}
	}
	// 模板没有库存数量列，不能自行增加。
	for _, h := range tb.header {
		if strings.Contains(strings.ToLower(h), "qty") || strings.Contains(strings.ToLower(h), "quantity") {
			t.Fatal("不应输出库存数量列：", h)
		}
	}
}

// Case 7：状态映射；未知或缺失状态按 draft 并报告一次；status_map 可覆盖。
func TestStatusMapping(t *testing.T) {
	tb := export(t, DefaultOptions(),
		tu.Row{"Handle": "a", "Title": "A", "Status": "active", "Option1 Value": "x", "Variant SKU": "1"},
		tu.Row{"Handle": "d", "Title": "D", "Status": "draft", "Option1 Value": "x", "Variant SKU": "2"},
		tu.Row{"Handle": "r", "Title": "R", "Status": "archived", "Option1 Value": "x", "Variant SKU": "3"},
		tu.Row{"Handle": "u", "Title": "U", "Status": "weird", "Option1 Value": "x", "Variant SKU": "4"},
		tu.Row{"Handle": "u", "Option1 Value": "y", "Variant SKU": "5"},
	)
	for i, want := range []string{"published", "draft", "draft", "draft", "draft"} {
		if got := tb.get(i, "Product Status"); got != want {
			t.Fatalf("行 %d 状态 %q，期望 %q", i, got, want)
		}
	}
	if n := strings.Count(tb.issues(), "无法识别"); n != 1 {
		t.Fatalf("未知状态应只报告一次，实际 %d：%s", n, tb.issues())
	}
	o := DefaultOptions()
	o.StatusMap["archived"] = "rejected"
	tb = export(t, o, tu.Row{"Handle": "r", "Title": "R", "Status": "Archived", "Option1 Value": "x", "Variant SKU": "3"})
	if tb.get(0, "Product Status") != "rejected" {
		t.Fatal(tb.rows[0])
	}
}

// Case 8 + Case 9：变种图不在商品图中时追加；去重后顺序为 a b c；同一商品只提示一次。
func TestVariantImagesMergedAndDeduplicated(t *testing.T) {
	tb := export(t, DefaultOptions(),
		tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Image Src": "https://a", "Image Position": "1", "Variant Image": "https://b"},
		tu.Row{"Handle": "p", "Option1 Value": "B", "Variant SKU": "S2", "Image Src": "https://a", "Image Position": "2", "Variant Image": "https://c"},
		tu.ImageRow("p", "https://b", "3"),
	)
	got := []string{tb.get(1, "Product Image 1 Url"), tb.get(1, "Product Image 2 Url"), tb.get(1, "Product Image 3 Url")}
	if strings.Join(got, ",") != "https://a,https://b,https://c" || tb.has("Product Image 4 Url") {
		t.Fatal(tb.header, got)
	}
	if n := strings.Count(tb.issues(), "不支持 Variant Image"); n != 1 {
		t.Fatal(tb.issues())
	}
	// 只有变种图、没有商品图时，变种图也作为缩略图。
	tb = export(t, DefaultOptions(), tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Variant Image": "https://v"})
	if tb.get(0, "Product Thumbnail") != "https://v" || tb.get(0, "Product Image 1 Url") != "https://v" {
		t.Fatal(tb.rows[0])
	}
}

func TestPriceCurrencyAndDefaults(t *testing.T) {
	o := DefaultOptions()
	o.PriceCurrency = "eur"
	o.Defaults["Shipping Profile Id"] = "sp_123"
	o.Defaults["Product Discountable"] = "FALSE"
	tb := export(t, o,
		tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Variant Price": "10"},
		tu.Row{"Handle": "p", "Option1 Value": "B", "Variant SKU": "S1", "Variant Price": "abc"},
	)
	if tb.get(0, "Variant Price EUR") != "10" || tb.get(0, "Variant Price USD") != "" ||
		tb.get(0, "Shipping Profile Id") != "sp_123" || tb.get(1, "Product Discountable") != "FALSE" || tb.get(1, "Variant Price EUR") != "" {
		t.Fatal(tb.rows)
	}
	if !strings.Contains(tb.issues(), "价格不是有效数字") || !strings.Contains(tb.issues(), "SKU 重复") {
		t.Fatal(tb.issues())
	}
	// 模板没有的币种追加为新价格列。
	o = DefaultOptions()
	o.PriceCurrency = "GBP"
	tb = export(t, o, tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S1", "Variant Price": "5"})
	if tb.get(0, "Variant Price GBP") != "5" {
		t.Fatal(tb.header)
	}
}

func TestValidate(t *testing.T) {
	for _, o := range []Options{
		{PriceCurrency: "US"},
		{PriceCurrency: "USD", StatusMap: map[string]string{"active": "live"}},
		{PriceCurrency: "USD", Defaults: map[string]string{"Product Image 1 Url": "x"}},
	} {
		if o.Validate() == nil {
			t.Fatalf("%+v 应校验失败", o)
		}
	}
	if err := DefaultOptions().Validate(); err != nil {
		t.Fatal(err)
	}
}

// 输出必须能被标准 encoding/csv 读回，HTML 中的逗号、引号、换行不被破坏。
func TestCSVRoundTrip(t *testing.T) {
	desc := "<p class=\"a\">Hello, \"world\"</p>\n<ul><li>x</li></ul>"
	tb := export(t, DefaultOptions(), tu.Row{"Handle": "p", "Title": "A, \"B\"", "Body (HTML)": desc, "Option1 Value": "A", "Variant SKU": "S1"})
	b, err := EncodeCSV(tb.header, tb.rows)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) {
		t.Fatal("不应写 BOM")
	}
	back, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	rt := table{back[0], back[1:], model.Report{}}
	if len(back) != 2 || rt.get(0, "Product Description") != desc || rt.get(0, "Product Title") != "A, \"B\"" {
		t.Fatal(back)
	}
}
