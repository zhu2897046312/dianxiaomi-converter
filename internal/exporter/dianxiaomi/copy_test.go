package dianxiaomi

import (
	"fmt"
	"strings"
	"testing"

	"dianxiaomi-converter/internal/exporter/medusa"
	"dianxiaomi-converter/internal/model"
)

func TestStripEmoji(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"🐶狗🐱猫", "狗猫"},
		{"A👨‍👩‍👧‍👦👍🏽🇨🇳❤️B", "AB"},
		{"1️⃣#️⃣*️⃣普通0123#* ©®™ ©️®️™️", "普通0123#* ©®™ "},
		{"&#128054;&#x1F431; &amp; &#65;", " &amp; &#65;"},
		{"𠀀中文，café $19.99 €20 × 2 10cm\n", "𠀀中文，café $19.99 €20 × 2 10cm\n"},
		{"🏴\U000e0067\U000e0062\U000e007f text", " text"},
	} {
		if got := stripEmoji(tc.in); got != tc.want {
			t.Errorf("%q => %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCopyCleaningIsDianxiaomiOnly(t *testing.T) {
	desc := `<p>🐶狗 &amp; &#x1F431;猫</p><a href="https://example.com/keep.gif">🐱Link</a><img src="https://example.com/motion.GIF?v=1"><source srcset='https://example.com/two.gif 1x, https://example.com/still.png 2x'><img src="https://example.com/🐱.png">`
	var images []model.ProductImage
	allowed := map[string]bool{}
	for i := 1; i <= 12; i++ {
		u := fmt.Sprintf("https://a/%d.jpg", i)
		images = append(images, model.ProductImage{URL: u})
		if i <= 10 {
			allowed[u] = true
		}
	}
	ps := []model.Product{{Handle: "产品 🐶 / A.B_C-1+", Title: "🐶宠物🐱", Description: desc, Images: images, Variants: []model.Variant{{SKU: "A😊", Options: []model.Option{{Name: "Color", Value: "🐶Red"}}}}}}
	o := opts()
	o.Overrides = map[string]map[string]string{"A😊": {"*英文标题": "🐶Pets", "包装清单": "🐱Ball"}}
	rows, _, err := Export(ps, o)
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r[0] != "宠物" || r[1] != "Pets" || r[3] != "A.B_C-1" || r[5] != "Red" || r[10] != "A" || r[idx("包装清单")] != "Ball" {
		t.Fatal(r)
	}
	if !strings.Contains(r[2], `href="https://example.com/keep.gif"`) || strings.Contains(strings.ReplaceAll(r[2], `href="https://example.com/keep.gif"`, ""), ".gif") || strings.Contains(r[2], "🐶") || strings.Contains(r[2], "🐱Link") {
		t.Fatal(r[2])
	}
	for _, u := range imageAttributePattern.FindAllString(r[2], -1) {
		if strings.Contains(u, "https://a/") {
			found := false
			for candidate := range allowed {
				if strings.Contains(u, candidate) {
					found = true
				}
			}
			if !found {
				t.Fatal("description used image outside final carousel", u)
			}
		}
	}
	h, mr, _ := medusa.Export(ps, medusa.DefaultOptions())
	for i, name := range h {
		if name == "Product Description" && mr[0][i] != desc {
			t.Fatal(mr[0][i])
		}
		if name == "Product Title" && mr[0][i] != "🐶宠物🐱" {
			t.Fatal(mr[0][i])
		}
		if name == "Variant SKU" && mr[0][i] != "A😊" {
			t.Fatal(mr[0][i])
		}
	}
	o.Overrides["A😊"]["*英文标题"] = "🐶🐱"
	rows, _, err = Export(ps, o)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRequiredRows(rows); err == nil || !strings.Contains(err.Error(), "*英文标题") {
		t.Fatal(err)
	}
}

func TestProductCodeCleaningCanBeDisabled(t *testing.T) {
	ps := []model.Product{{Handle: "商品 🐶 / A_B-1.2+", Title: "P", Images: []model.ProductImage{{URL: "https://a/1"}}, Variants: []model.Variant{{SKU: "A", Options: []model.Option{{Name: "Color", Value: "Red"}}}}}}
	o := opts()
	disabled := false
	o.CleanProductCode = &disabled
	rows, _, err := Export(ps, o)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][idx("产品货号")] != ps[0].Handle {
		t.Fatal(rows[0][idx("产品货号")])
	}
}
