// Package shopify 把 Shopify 商品导出 CSV（或列名经 Fields 映射后的同构 CSV）解析为统一商品模型。
package shopify

import (
	"encoding/csv"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"dianxiaomi-converter/internal/model"
)

type record map[string]string

// get 按实际列名取值；列名为空表示该逻辑字段未启用。
func (r record) get(column string) string {
	if column == "" {
		return ""
	}
	return r[column]
}

// group 是解析过程中一个 Handle 下累积的原始行。
type group struct {
	product      model.Product
	optionNames  [3]string
	variantRows  []record
	imageRecords []record
}

// Parse 解析 CSV。extraRequired 是额外要求必须存在的字段键（如用户显式指定的价格列），
// 找不到说明配置写错，应报错而不是当作可选字段静默忽略。
//
// Shopify 导出中同一商品占多行：第一行带标题、描述、状态和第一个变种；后续变种行
// 只有 Handle 与变种列；图片多于变种时，多出的图片各占一行，只有 Handle、Image Src、
// Image Position。所以商品必须按 Handle 聚合，商品级字段取该商品中出现的非空值，
// 图片也必须跨所有行收集，否则纯图片行上的图片会丢失。
func Parse(b []byte, f Fields, extraRequired ...string) ([]model.Product, error) {
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("CSV 必须为 UTF-8，请先另存为 CSV UTF-8")
	}
	all, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(b), "\ufeff"))).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(all) < 2 {
		return nil, fmt.Errorf("CSV 没有数据")
	}
	if err = checkColumns(all[0], f, extraRequired); err != nil {
		return nil, err
	}

	groups := map[string]*group{}
	var order []*group
	for i, line := range all[1:] {
		r := record{}
		for j, h := range all[0] {
			r[h] = strings.TrimSpace(line[j])
		}
		h := r.get(f.Handle)
		if h == "" {
			return nil, fmt.Errorf("CSV 第 %d 条记录缺少 %s", i+2, f.Handle)
		}
		g := groups[h]
		if g == nil {
			g = &group{product: model.Product{Handle: h}}
			groups[h] = g
			order = append(order, g)
		}
		p := &g.product
		if t := r.get(f.Title); t != "" {
			if p.Title != "" && p.Title != t {
				return nil, fmt.Errorf("商品 %s 存在冲突标题", h)
			}
			p.Title = t
		}
		if d := r.get(f.Description); d != "" {
			p.Description = d
		}
		if s := r.get(f.Status); s != "" && p.Status == "" {
			p.Status = s
		}
		for k, col := range []string{f.Option1Name, f.Option2Name, f.Option3Name} {
			if n := r.get(col); n != "" && g.optionNames[k] == "" {
				g.optionNames[k] = n
			}
		}
		// 变种判断与图片判断是两个独立条件：一行可以既是变种又带图片，也可以只是图片行。
		if isVariantRow(r, f) {
			g.variantRows = append(g.variantRows, r)
		}
		if r.get(f.ImageURL) != "" {
			g.imageRecords = append(g.imageRecords, r)
		}
	}

	products := make([]model.Product, 0, len(order))
	for _, g := range order {
		if len(g.variantRows) == 0 {
			return nil, fmt.Errorf("商品 %s 没有变种数据", g.product.Handle)
		}
		for _, r := range g.variantRows {
			g.product.Variants = append(g.product.Variants, variantOf(r, f, g.optionNames))
		}
		g.product.Images = sortedImages(g.imageRecords, f)
		products = append(products, g.product)
	}
	return products, nil
}

func checkColumns(header []string, f Fields, extraRequired []string) error {
	cols := map[string]bool{}
	for _, s := range header {
		if cols[s] {
			return fmt.Errorf("重复列 %s", s)
		}
		cols[s] = true
	}
	extra := map[string]bool{}
	for _, k := range extraRequired {
		extra[k] = true
	}
	for _, c := range f.columns() {
		if !c.required && !extra[c.key] {
			continue
		}
		if c.name == "" {
			return fmt.Errorf("source_fields.%s 不能为空", c.key)
		}
		if !cols[c.name] {
			return fmt.Errorf("缺少列 %s（source_fields.%s）", c.name, c.key)
		}
	}
	return nil
}

// isVariantRow 判断一行是否代表一个变种。Shopify 纯图片行不填 SKU、选项值和价格，
// 若把它当成变种，会导出没有规格、没有价格的空白 SKU。三者任一非空即说明这一行在描述变种；
// 只看 SKU 会漏掉未填货号的变种（由导出器报告“SKU 缺失”）。
func isVariantRow(r record, f Fields) bool {
	return r.get(f.SKU) != "" || r.get(f.Option1Value) != "" || r.get(f.Price) != ""
}

func variantOf(r record, f Fields, productNames [3]string) model.Variant {
	v := model.Variant{
		SKU:          r.get(f.SKU),
		Price:        r.get(f.Price),
		ComparePrice: r.get(f.CompareAtPrice),
		WeightGrams:  r.get(f.WeightGrams),
		Barcode:      r.get(f.Barcode),
		InventoryQty: r.get(f.InventoryQty),
		// Shopify 的 Inventory Tracker 填了跟踪方（通常是 shopify）就表示跟踪库存，空表示不跟踪。
		// 不能用库存数量判断：数量为 0 的商品同样可能在跟踪库存。
		ManageInventory: r.get(f.InventoryTracker) != "",
		// Inventory Policy 只有 continue 表示缺货仍可下单；deny、空或未知值都按不允许处理，避免超卖。
		AllowBackorder: strings.EqualFold(r.get(f.InventoryPolicy), "continue"),
		Image:          r.get(f.VariantImageURL),
	}
	pairs := [3][2]string{{f.Option1Name, f.Option1Value}, {f.Option2Name, f.Option2Value}, {f.Option3Name, f.Option3Value}}
	for k, p := range pairs {
		value := r.get(p[1])
		name := r.get(p[0])
		// Shopify 只在商品第一行写选项名称，后续变种行留空；该行确有选项值时回退到商品级名称。
		if name == "" && value != "" {
			name = productNames[k]
		}
		v.Options = append(v.Options, model.Option{Name: name, Value: value})
	}
	for len(v.Options) > 0 && v.Options[len(v.Options)-1].Value == "" {
		v.Options = v.Options[:len(v.Options)-1]
	}
	// Shopify 单规格商品也必须有一个选项，导出时固定写成 Title=Default Title。
	// 它只是占位，不是真实规格；标记出来让目标平台决定是否导出，避免出现名为 Title 的假属性。
	v.IsDefault = len(v.Options) == 1 &&
		strings.EqualFold(v.Options[0].Name, "Title") && strings.EqualFold(v.Options[0].Value, "Default Title")
	return v
}

// sortedImages 返回商品级图片列表。
//
//   - 按 Image Position 升序：CSV 行序不一定等于展示顺序，Position 才是 Shopify 的图片顺序。
//   - 稳定排序，同位置的图片保持 CSV 原顺序。
//   - 位置为空或不是整数的图片排在有效位置之后，不报错，避免个别脏数据阻断整个商品。
//   - 同一 URL 可能重复出现（例如变种行与图片行都写了同一张图），只保留排序后首次出现的位置。
//
// 不在这里限制数量，数量上限属于目标平台规则。
func sortedImages(rows []record, f Fields) []model.ProductImage {
	type img struct {
		model.ProductImage
		valid bool
	}
	list := make([]img, 0, len(rows))
	for _, r := range rows {
		pos, err := strconv.Atoi(r.get(f.ImagePosition))
		if err != nil {
			pos = 0
		}
		list = append(list, img{model.ProductImage{URL: r.get(f.ImageURL), Position: pos}, err == nil})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].valid != list[j].valid {
			return list[i].valid
		}
		return list[i].valid && list[i].Position < list[j].Position
	})
	var out []model.ProductImage
	seen := map[string]bool{}
	for _, it := range list {
		if it.URL == "" || seen[it.URL] {
			continue
		}
		seen[it.URL] = true
		out = append(out, it.ProductImage)
	}
	return out
}
