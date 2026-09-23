package shopify

import (
	"strings"
	"testing"

	"dianxiaomi-converter/internal/model"
	tu "dianxiaomi-converter/internal/testutil"
)

func mustParse(t *testing.T, b []byte, f Fields, extra ...string) []model.Product {
	t.Helper()
	ps, err := Parse(b, f, extra...)
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func urls(p model.Product) []string {
	var out []string
	for _, img := range p.Images {
		out = append(out, img.URL)
	}
	return out
}

// 2 个变种 + 6 张图，后 4 张在纯图片行：纯图片行不能成为变种，但图片全部归入商品。
func TestGroupVariantsAndImageOnlyRows(t *testing.T) {
	b := tu.Shopify(
		tu.Row{"Handle": "p", "Title": "T", "Status": "active", "Option1 Name": "quantity", "Option1 Value": "3pcs", "Variant SKU": "S1", "Image Src": "https://a/1", "Image Position": "1"},
		tu.Row{"Handle": "p", "Option1 Value": "6pcs", "Variant SKU": "S2", "Image Src": "https://a/2", "Image Position": "2"},
		tu.ImageRow("p", "https://a/3", "3"), tu.ImageRow("p", "https://a/4", "4"),
		tu.ImageRow("p", "https://a/5", "5"), tu.ImageRow("p", "https://a/6", "6"),
		tu.Row{"Handle": "q", "Title": "Q", "Option1 Value": "A", "Variant SKU": "Q1"},
	)
	ps := mustParse(t, b, DefaultFields())
	if len(ps) != 2 || ps[0].Handle != "p" || ps[1].Handle != "q" {
		t.Fatalf("%+v", ps)
	}
	p := ps[0]
	if len(p.Variants) != 2 || len(p.Images) != 6 || p.Status != "active" || p.Title != "T" {
		t.Fatalf("%+v", p)
	}
	// 后续变种行的选项名称回退到商品第一行。
	if p.Variants[1].Options[0] != (model.Option{Name: "quantity", Value: "6pcs"}) {
		t.Fatal(p.Variants[1].Options)
	}
}

func TestImagesSortedDeduplicated(t *testing.T) {
	b := tu.Shopify(
		tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S", "Image Src": "https://a/x", "Image Position": "abc"},
		tu.ImageRow("p", "https://a/2", "3"),
		tu.ImageRow("p", "https://a/1", "1"),
		tu.ImageRow("p", "https://a/1", "2"),
		tu.ImageRow("p", "", "4"),
		tu.ImageRow("p", "https://a/y", ""),
	)
	got := strings.Join(urls(mustParse(t, b, DefaultFields())[0]), ",")
	if got != "https://a/1,https://a/2,https://a/x,https://a/y" {
		t.Fatal(got)
	}
}

// 模型保留全部 3 组属性，不因为某个目标平台只支持两组而截断或报错。
func TestThreeOptionsKept(t *testing.T) {
	b := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Option1 Name": "Color", "Option1 Value": "Red",
		"Option2 Name": "Size", "Option2 Value": "XL", "Option3 Name": "Quantity", "Option3 Value": "3pcs", "Variant SKU": "S"})
	v := mustParse(t, b, DefaultFields())[0].Variants[0]
	if len(v.Options) != 3 || v.Options[2] != (model.Option{Name: "Quantity", Value: "3pcs"}) || v.IsDefault {
		t.Fatal(v)
	}
}

func TestDefaultTitleAndInventorySemantics(t *testing.T) {
	b := tu.Shopify(
		tu.Row{"Handle": "p", "Title": "T", "Option1 Name": "Title", "Option1 Value": "Default Title", "Variant SKU": "S1",
			"Variant Inventory Tracker": "shopify", "Variant Inventory Policy": "continue", "Variant Inventory Qty": "0"},
		tu.Row{"Handle": "q", "Title": "Q", "Option1 Value": "A", "Variant SKU": "S2", "Variant Inventory Policy": "deny", "Variant Inventory Qty": "9"},
	)
	ps := mustParse(t, b, DefaultFields())
	a, c := ps[0].Variants[0], ps[1].Variants[0]
	if !a.IsDefault || !a.ManageInventory || !a.AllowBackorder {
		t.Fatalf("%+v", a)
	}
	// 有库存数量但没有 Tracker 仍然是不管理库存。
	if c.IsDefault || c.ManageInventory || c.AllowBackorder {
		t.Fatalf("%+v", c)
	}
}

func TestCustomFields(t *testing.T) {
	header := []string{"Name", "URL handle", "SKU", "Option1 value", "Product image URL"}
	b := tu.CSV(header,
		tu.Row{"Name": "T", "URL handle": "p", "SKU": "001", "Option1 value": "Black", "Product image URL": "https://a/1"},
		tu.Row{"URL handle": "p", "Product image URL": "https://a/2"},
	)
	f := DefaultFields()
	f.Title, f.Handle, f.SKU, f.Option1Value, f.ImageURL = "Name", "URL handle", "SKU", "Option1 value", "Product image URL"
	ps := mustParse(t, b, f)
	if len(ps) != 1 || len(ps[0].Variants) != 1 || len(ps[0].Images) != 2 || ps[0].Title != "T" {
		t.Fatalf("%+v", ps)
	}
}

func TestColumnValidation(t *testing.T) {
	b := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Option1 Value": "A", "Variant SKU": "S", "Image Src": "https://a/1"})
	f := DefaultFields()
	f.ImageURL = "Product image URL"
	if _, err := Parse(b, f); err == nil || !strings.Contains(err.Error(), "source_fields.image_url") {
		t.Fatal(err)
	}
	// 可选列缺失不阻断解析，但被显式要求的列缺失要报错。
	f = DefaultFields()
	f.Barcode, f.Price = "不存在", "Cost"
	if _, err := Parse(b, f); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(b, f, "price"); err == nil {
		t.Fatal("显式要求的价格列缺失应报错")
	}
}

func TestImageOnlyProductRejected(t *testing.T) {
	b := tu.Shopify(tu.Row{"Handle": "p", "Title": "T", "Image Src": "https://a/1"})
	if _, err := Parse(b, DefaultFields()); err == nil || !strings.Contains(err.Error(), "没有变种") {
		t.Fatal(err)
	}
}

func TestTitleConflictAndEncoding(t *testing.T) {
	b := tu.Shopify(tu.Row{"Handle": "p", "Title": "A", "Variant SKU": "1"}, tu.Row{"Handle": "p", "Title": "B", "Variant SKU": "2"})
	if _, err := Parse(b, DefaultFields()); err == nil {
		t.Fatal("冲突标题应报错")
	}
	if _, err := Parse([]byte{0xa8, 0x43}, DefaultFields()); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatal(err)
	}
}
