package dianxiaomi

import (
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
	desc := `<p>🐶狗 &amp; &#x1F431;猫</p><a href="https://example.com/🐶">🐱Link</a><img src="https://example.com/🐱.png">`
	ps := []model.Product{{Handle: "p", Title: "🐶宠物🐱", Description: desc, Images: []model.ProductImage{{URL: "https://a/1"}}, Variants: []model.Variant{{SKU: "A😊", Options: []model.Option{{Name: "Color", Value: "🐶Red"}}}}}}
	o := opts()
	o.Overrides = map[string]map[string]string{"A😊": {"*英文标题": "🐶Pets", "包装清单": "🐱Ball"}}
	rows, _, err := Export(ps, o)
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r[0] != "宠物" || r[1] != "Pets" || r[5] != "Red" || r[10] != "A😊" || r[idx("包装清单")] != "Ball" {
		t.Fatal(r)
	}
	want := `<p>狗 &amp; 猫</p><a href="https://example.com/🐶">Link</a><img src="https://example.com/🐱.png">`
	if r[2] != want {
		t.Fatal(r[2])
	}
	h, mr, _ := medusa.Export(ps, medusa.DefaultOptions())
	for i, name := range h {
		if name == "Product Description" && mr[0][i] != desc {
			t.Fatal(mr[0][i])
		}
		if name == "Product Title" && mr[0][i] != "🐶宠物🐱" {
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
