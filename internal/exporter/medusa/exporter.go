// Package medusa 把统一商品模型导出为 Medusa 商品导入 CSV（Product Import Template）。
package medusa

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"math"
	"strconv"
	"strings"

	"dianxiaomi-converter/internal/model"
)

// baseHeaders 是 Medusa 模板中固定的列，顺序与官方模板一致。
// 模板中的 Variant Option N 与 Product Image N Url 属于编号列，由 Export 按本批数据动态生成。
var baseHeaders = []string{
	"Product Id", "Product Handle", "Product Title", "Product Subtitle", "Product Description",
	"Product Status", "Product Thumbnail", "Product Weight", "Product Length", "Product Width",
	"Product Height", "Product HS Code", "Product Origin Country", "Product MID Code", "Product Material",
	"Shipping Profile Id", "Product Sales Channel 1", "Product Collection Id", "Product Type Id", "Product Tag 1",
	"Product Discountable", "Product External Id", "Variant Id", "Variant Title", "Variant SKU",
	"Variant Barcode", "Variant Allow Backorder", "Variant Manage Inventory", "Variant Weight", "Variant Length",
	"Variant Width", "Variant Height", "Variant HS Code", "Variant Origin Country", "Variant MID Code",
	"Variant Material", "Variant Price EUR", "Variant Price USD",
}

// 模板自带 1 组 Option 与 2 个图片列，即使数据更少也保留，保持与模板一致。
const (
	minOptionColumns = 1
	minImageColumns  = 2
)

const (
	boolTrue  = "TRUE"
	boolFalse = "FALSE"
	// 未知或缺失状态一律按草稿导入，宁可让人工确认后再发布，也不把不确定的商品直接上架。
	fallbackStatus = "draft"
)

var validStatuses = map[string]bool{"published": true, "draft": true, "proposed": true, "rejected": true}

// Options 是 Medusa 目标的配置，与来源字段映射（source_fields）分开：
// 这里只描述“Medusa 输出缺什么时填什么”，不关心来源 CSV 的列名。
type Options struct {
	// PriceCurrency 表示来源价格的币种，决定价格写入哪一列 Variant Price XXX。不做汇率换算。
	PriceCurrency string `json:"price_currency"`
	// StatusMap 把来源状态（不区分大小写）映射到 Medusa 状态。
	StatusMap map[string]string `json:"status_map"`
	// Defaults 以 Medusa 列名为键，仅填充空白单元格。
	Defaults map[string]string `json:"defaults"`
}

// DefaultOptions 返回默认配置。loadConfig 在此基础上解码 JSON，
// encoding/json 解码到已有 map 时只增改出现的键，所以用户只需写要覆盖的状态或默认值。
func DefaultOptions() Options {
	return Options{
		PriceCurrency: "USD",
		// archived 默认转 draft：已归档的 Shopify 商品通常是停售商品，
		// Medusa 没有“归档”状态，映射成 published 会让它们在新店铺里被重新上架。
		StatusMap: map[string]string{"active": "published", "draft": "draft", "archived": "draft"},
		Defaults:  map[string]string{"Product Discountable": boolTrue},
	}
}

func (o Options) Validate() error {
	c := strings.TrimSpace(o.PriceCurrency)
	if len(c) != 3 || strings.Trim(strings.ToUpper(c), "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return fmt.Errorf("medusa.price_currency 必须是 3 位货币代码，例如 USD")
	}
	for k, v := range o.StatusMap {
		if !validStatuses[v] {
			return fmt.Errorf("medusa.status_map.%s 的值 %q 无效，可选 published/draft/proposed/rejected", k, v)
		}
	}
	for k := range o.Defaults {
		if !isBaseHeader(k) {
			return fmt.Errorf("未知 medusa.defaults 字段 %s", k)
		}
	}
	return nil
}

func isBaseHeader(k string) bool {
	for _, h := range baseHeaders {
		if h == k {
			return true
		}
	}
	return false
}

func (o Options) priceColumn() string {
	return "Variant Price " + strings.ToUpper(strings.TrimSpace(o.PriceCurrency))
}

// status 映射来源状态；ok 为 false 表示状态缺失或未配置，已回退为 draft。
func (o Options) status(s string) (string, bool) {
	for k, v := range o.StatusMap {
		if strings.EqualFold(k, s) {
			return v, true
		}
	}
	return fallbackStatus, false
}

// Header 按“固定列 → 编号 Option 列 → 编号图片列”生成表头。
// 编号 Option 插在价格列之后、图片列在最后，与官方模板的位置一致。
// 模板中没有的币种（如 GBP）追加在已有价格列之后，Medusa 按 “Variant Price 币种” 识别价格列。
func Header(opts Options, optionCount, imageCount int) []string {
	h := append([]string{}, baseHeaders...)
	if pc := opts.priceColumn(); !isBaseHeader(pc) {
		h = append(h, pc)
	}
	for i := 1; i <= max(optionCount, minOptionColumns); i++ {
		h = append(h, fmt.Sprintf("Variant Option %d Name", i), fmt.Sprintf("Variant Option %d Value", i))
	}
	for i := 1; i <= max(imageCount, minImageColumns); i++ {
		h = append(h, fmt.Sprintf("Product Image %d Url", i))
	}
	return h
}

// Export 生成表头和数据行。
//
// Medusa 导入模板是“一行一个变种”的扁平结构：同一商品的多行靠 Product Handle 归为一个商品，
// 所以商品级字段（标题、描述、状态、缩略图、图片等）必须在该商品的每一行重复填写，
// 只填第一行会让后续变种行被当成缺少商品信息。
func Export(products []model.Product, opts Options) ([]string, [][]string, model.Report) {
	report := model.Report{Target: "medusa", Issues: []model.Issue{}}
	images := make([][]string, len(products))
	optionCount, imageCount := 0, 0
	for i, p := range products {
		images[i] = productImages(p)
		imageCount = max(imageCount, len(images[i]))
		for _, v := range p.Variants {
			if !v.IsDefault {
				optionCount = max(optionCount, len(v.Options))
			}
		}
	}
	header := Header(opts, optionCount, imageCount)

	var rows [][]string
	seen := map[string]bool{}
	for i, p := range products {
		report.Products++
		status, statusOK := opts.status(p.Status)
		hasVariantImage := false
		for _, v := range p.Variants {
			hasVariantImage = hasVariantImage || v.Image != ""
		}
		for j, v := range p.Variants {
			rowNo := len(rows) + 2
			warn := func(s string) { report.Issues = append(report.Issues, model.Issue{Row: rowNo, SKU: v.SKU, Message: s}) }
			// 商品级提示只在该商品第一行报一次。
			if j == 0 {
				if !statusOK {
					warn(fmt.Sprintf("商品 %s 的状态 %q 无法识别，已按 draft 导入", p.Handle, p.Status))
				}
				// Medusa 导入模板没有变种图片列，无法保留“某张图属于某变种”的关系；
				// 为了不丢图，变种图已并入商品图片（见 productImages）。
				if hasVariantImage {
					warn("Medusa CSV 模板不支持 Variant Image 关联，已将变种图片加入产品图片集合")
				}
			}
			if v.SKU == "" {
				warn("SKU 缺失")
			} else if seen[v.SKU] {
				warn("SKU 重复")
			}
			seen[v.SKU] = true

			m := map[string]string{
				// Product Id / Variant Id 留空：新建商品时由 Medusa 生成，自行编造会被当成更新不存在的记录。
				"Product Handle":      p.Handle,
				"Product Title":       p.Title,
				"Product Description": p.Description, // 第一版保留原始 HTML
				"Product Status":      status,
				// Product Weight 留空：重量是变种级数据，取某个变种的重量当商品重量会误导运费计算。
				"Variant Title":            variantTitle(v),
				"Variant SKU":              v.SKU,
				"Variant Barcode":          v.Barcode,
				"Variant Allow Backorder":  boolText(v.AllowBackorder),
				"Variant Manage Inventory": boolText(v.ManageInventory),
				"Variant Weight":           v.WeightGrams,
			}
			// 以下 Medusa 列需要的是 Medusa 内部 ID（Collection/Type/Tag/Sales Channel/Shipping Profile），
			// 来源 CSV 里只有名称文本（如 Shopify 的 Collection、Type、Tags），名称不是 ID，
			// 直接写入会导致导入失败或挂到错误对象上，所以默认留空，只允许通过 medusa.defaults 填写真实 ID。
			// 同理 Vendor 不是材质，不映射到 Product Material。
			if len(images[i]) > 0 {
				m["Product Thumbnail"] = images[i][0]
			}
			for k, u := range images[i] {
				m[fmt.Sprintf("Product Image %d Url", k+1)] = u
			}
			if v.Price != "" {
				if n, err := strconv.ParseFloat(v.Price, 64); err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
					warn("价格不是有效数字：" + v.Price)
				} else {
					m[opts.priceColumn()] = v.Price
				}
			}
			// Shopify 的 Title=Default Title 只是单规格占位，导出成 Medusa 选项会生成一个名为 Title 的假属性。
			// 与店小秘不同，Medusa 支持任意编号的 Variant Option N，所以第三组属性照常导出，不做限制。
			if !v.IsDefault {
				for k, o := range v.Options {
					if o.Value == "" {
						continue
					}
					if o.Name == "" {
						warn(fmt.Sprintf("变种属性 %d 缺少名称", k+1))
					}
					m[fmt.Sprintf("Variant Option %d Name", k+1)] = o.Name
					m[fmt.Sprintf("Variant Option %d Value", k+1)] = o.Value
				}
			}
			for k, val := range opts.Defaults {
				if m[k] == "" {
					m[k] = val
				}
			}
			row := make([]string, len(header))
			for k, h := range header {
				row[k] = m[h]
			}
			rows = append(rows, row)
		}
	}
	report.Variants = len(rows)
	return header, rows, report
}

// productImages 返回商品的最终图片列表：先按来源顺序放商品图，再追加尚未出现过的变种图。
// 变种图不能丢，但模板没有变种图片列，只能并入商品图片；追加在后面是为了不打乱商品主图顺序。
// 与店小秘不同，这里不限制数量，图片列数随本批最大图片数扩展。
func productImages(p model.Product) []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, img := range p.Images {
		add(img.URL)
	}
	for _, v := range p.Variants {
		add(v.Image)
	}
	return out
}

// variantTitle 用各属性值拼出变种标题，例如 “Red / XL”。
// 没有真实属性（Shopify 单规格占位或无属性）时用 Default，而不是 “Default Title”。
func variantTitle(v model.Variant) string {
	if v.IsDefault {
		return "Default"
	}
	var parts []string
	for _, o := range v.Options {
		if o.Value != "" {
			parts = append(parts, o.Value)
		}
	}
	if len(parts) == 0 {
		return "Default"
	}
	return strings.Join(parts, " / ")
}

func boolText(b bool) string {
	if b {
		return boolTrue
	}
	return boolFalse
}

// EncodeCSV 用 encoding/csv 输出 UTF-8（无 BOM），自动处理 HTML 中的逗号、引号和换行。
// 不写 BOM：否则首列表头会变成 “\ufeffProduct Id”，导入端可能无法识别。
func EncodeCSV(header []string, rows [][]string) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(header); err != nil {
		return nil, err
	}
	if err := w.WriteAll(rows); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
