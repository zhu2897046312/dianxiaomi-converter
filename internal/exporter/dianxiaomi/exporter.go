// Package dianxiaomi 把统一商品模型导出为店小秘 Temu 批量导入模板的数据行。
package dianxiaomi

import (
	"dianxiaomi-converter/internal/imagefilter"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"dianxiaomi-converter/internal/model"
)

// Headers 是店小秘 Temu 模板第一张工作表的列，顺序与模板一致。
var Headers = []string{"*产品标题", "*英文标题", "产品描述", "产品货号", "*变种属性名称一", "*变种属性值一", "变种属性名称二", "变种属性值二", "预览图", "*申报价格\n(店铺币种)", "SKU货号", "*长（cm）", "*宽（cm）", "*高（cm）", "*重量（g）", "识别码类型", "识别码", "站外产品链接", "*轮播图", "*产品素材图", "外包装形状", "外包装类型", "外包装图片", "建议售价（USD）", "库存", "发货时效（天）", "是否定制品", "产品视频url", "描述视频url", "产品说明书", "说明书语种", "SKU分类类型", "SKU分类数量", "SKU分类单位", "是否独立包装", "单品净含量", "单品净含量单位", "内计共含件数", "是否同品", "总净含量", "总净含量单位", "包装清单", "包装清单数量", "是否敏感属性", "敏感属性值", "储电容量", "刀具长度", "刀刃角度", "液体容量", "来源URL", "产地"}

// 店小秘 *轮播图 最多接受 10 张图片链接。
const maxCarouselImages = 10

// 店小秘模板只有两组变种属性列。
const maxOptions = 2

// Options 是店小秘目标的配置。键均为模板列名（含星号、全角括号）。
type Options struct {
	FallbackImages     map[string]string            `json:"-"`                     // 本次检查移除的原始商品图，仅用于所有图片被移除时的必填兜底。
	SourceDescriptions map[string]string            `json:"-"`                     // 店小秘专用描述：保留待替换的 GIF 标签，其他 403 已清理。
	CleanProductCode   *bool                        `json:"clean_product_code"`    // 默认开启；产品货号只保留字母、数字、点、下划线和连字符。
	RepeatImagesToTen  *bool                        `json:"repeat_images_to_ten"`  // 旧配置兼容字段，已停用；始终只输出不同图片。
	RemoveChineseInSKU *bool                        `json:"remove_chinese_in_sku"` // 默认开启；控制汉字清理。Emoji 始终删除。
	CurrencyConversion CurrencyConversion           `json:"currency_conversion"`
	SuggestedPrice     SuggestedPrice               `json:"suggested_price"`
	PriceMultiplier    float64                      `json:"price_multiplier"`
	Defaults           map[string]string            `json:"defaults"`      // 仅填充空白单元格
	Overrides          map[string]map[string]string `json:"sku_overrides"` // 按来源 SKU 强制覆盖，优先级最高
}

// Rates 为每单位来源币种对应的 CNY 金额，新增币种只需添加配置。
type CurrencyConversion struct {
	Enabled        bool               `json:"enabled"`
	SourceCurrency string             `json:"source_currency"`
	Rates          map[string]float64 `json:"rates"`
}

// SuggestedPrice 控制模板“建议售价（USD）”。模板没有独立币种列，最终始终输出 USD。
type SuggestedPrice struct {
	Enabled        *bool  `json:"enabled"` // 默认开启。
	SourceCurrency string `json:"source_currency"`
}

func (s SuggestedPrice) enabled() bool { return s.Enabled == nil || *s.Enabled }

func (c CurrencyConversion) rate() (float64, error) {
	if !c.Enabled {
		return 1, nil
	}
	r, ok := c.Rates[strings.ToUpper(strings.TrimSpace(c.SourceCurrency))]
	if !ok || r <= 0 || math.IsNaN(r) || math.IsInf(r, 0) {
		return 0, fmt.Errorf("currency_conversion：来源币种 %q 缺少有效的 CNY 汇率（必须大于 0）", c.SourceCurrency)
	}
	return r, nil
}

// suggestedPriceUSD 把来源价格转换成模板固定要求的 USD 建议售价。
// 来源就是 USD 时直接返回原值，不受申报价的 price_multiplier 影响。
func (c CurrencyConversion) suggestedPriceUSD(n float64, configuredSource string) (float64, error) {
	source := strings.ToUpper(strings.TrimSpace(configuredSource))
	if source == "" {
		source = strings.ToUpper(strings.TrimSpace(c.SourceCurrency))
	}
	if source == "" || source == "USD" {
		return n, nil
	}
	sourceRate, sourceOK := c.Rates[source]
	usdRate, usdOK := c.Rates["USD"]
	if !sourceOK || sourceRate <= 0 || math.IsNaN(sourceRate) || math.IsInf(sourceRate, 0) {
		return 0, fmt.Errorf("currency_conversion：来源币种 %q 缺少有效的 CNY 汇率（必须大于 0）", source)
	}
	if !usdOK || usdRate <= 0 || math.IsNaN(usdRate) || math.IsInf(usdRate, 0) {
		return 0, fmt.Errorf("currency_conversion：生成建议售价需要有效的 USD 对 CNY 汇率（必须大于 0）")
	}
	return n * sourceRate / usdRate, nil
}

func (o Options) Validate() error {
	if _, err := o.CurrencyConversion.rate(); err != nil {
		return err
	}
	if o.SuggestedPrice.enabled() {
		if _, err := o.CurrencyConversion.suggestedPriceUSD(1, o.SuggestedPrice.SourceCurrency); err != nil {
			return err
		}
	}
	if o.PriceMultiplier <= 0 || math.IsNaN(o.PriceMultiplier) || math.IsInf(o.PriceMultiplier, 0) {
		return fmt.Errorf("price_multiplier 必须大于 0")
	}
	for k := range o.Defaults {
		if !known(k) {
			return fmt.Errorf("未知店小秘 defaults 字段 %s", k)
		}
	}
	for _, v := range o.Overrides {
		for k := range v {
			if !known(k) {
				return fmt.Errorf("未知店小秘 sku_overrides 字段 %s", k)
			}
		}
	}
	return nil
}

func known(k string) bool {
	for _, h := range Headers {
		if h == k {
			return true
		}
	}
	return false
}

// Export 为每个变种生成一行，列顺序与 Headers 一致。
func Export(products []model.Product, opts Options) ([][]string, model.Report, error) {
	report := model.Report{Target: "dianxiaomi", Issues: []model.Issue{}}
	rate, err := opts.CurrencyConversion.rate()
	if err != nil {
		return nil, report, err
	}
	out := [][]string{}
	seen := map[string]bool{}
	titles := map[string]string{}
	for _, p := range products {
		h := p.Handle
		description := p.Description
		if source, ok := opts.SourceDescriptions[h]; ok {
			description = source
		}
		report.Products++
		// 商品图优先，按 SKU 顺序追加不同的变种图，所有 SKU 共用轮播图。
		var allImgs []string
		imageSeen := map[string]bool{}
		addImage := func(url string) {
			if url != "" && !imageSeen[url] {
				allImgs = append(allImgs, url)
				imageSeen[url] = true
			}
		}
		for _, img := range p.Images {
			addImage(img.URL)
		}
		for _, v := range p.Variants {
			addImage(v.Image)
		}
		imgs := allImgs
		if len(imgs) > maxCarouselImages {
			imgs = imgs[:maxCarouselImages]
		}
		for i, v := range p.Variants {
			rowNo := len(out) + 2
			sourceSKU := v.SKU
			warn := func(s string) {
				report.Issues = append(report.Issues, model.Issue{Row: rowNo, SKU: sourceSKU, Message: s})
			}
			// 店小秘模板只有两组变种属性。第三组无处安放，丢弃会让不同 SKU 变成相同属性组合，
			// 导入后无法区分，所以直接停止而不是静默截断。这是店小秘独有的限制，统一模型不做截断。
			if len(v.Options) > maxOptions {
				o := v.Options[maxOptions]
				return nil, report, fmt.Errorf("SKU %s 有第三变种属性 %s=%s，目标模板仅支持两种，停止转换以避免丢失", sourceSKU, o.Name, o.Value)
			}
			opt := func(k int) model.Option {
				if k < len(v.Options) {
					return v.Options[k]
				}
				return model.Option{}
			}
			m := map[string]string{
				"*产品标题":    p.Title,
				"*英文标题":    p.Title,
				"产品描述":     description,
				"产品货号":     h,
				"SKU货号":    sourceSKU,
				"*变种属性名称一": optionName(opt(0).Name),
				"*变种属性值一":  opt(0).Value,
				"变种属性名称二":  optionName(opt(1).Name),
				"变种属性值二":   opt(1).Value,
				"预览图":      v.Image,
				"*重量（g）":   weightGrams(v.WeightGrams),
				"库存":       v.InventoryQty,
				"识别码":      v.Barcode,
			}
			if old, ok := titles[p.Title]; ok && old != h {
				warn("不同商品使用相同标题，店小秘可能合并，请修改标题")
			}
			titles[p.Title] = h
			// 超限只在商品第一个 SKU 上报一次，避免同一商品的每个 SKU 重复刷同一条警告。
			if i == 0 && len(allImgs) > maxCarouselImages {
				warn(fmt.Sprintf("商品共 %d 张图片，轮播图仅保留前 %d 张", len(allImgs), maxCarouselImages))
			}
			if len(imgs) > 0 {
				m["*轮播图"] = strings.Join(imgs, "\n")
				// 变种没有专属图片时，用商品首图作为预览图，保证每个 SKU 都有图。
				if m["预览图"] == "" {
					m["预览图"] = imgs[0]
				}
			}
			if v.Price != "" {
				n, e := strconv.ParseFloat(v.Price, 64)
				if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
					warn("价格不是有效数字：" + v.Price)
				} else {
					amount := n * opts.PriceMultiplier * rate
					if math.IsInf(amount, 0) || math.IsNaN(amount) {
						return nil, report, fmt.Errorf("SKU %s 换算后价格超出有效范围", sourceSKU)
					}
					m[Headers[9]] = strconv.FormatFloat(amount, 'f', 2, 64)
					if opts.SuggestedPrice.enabled() {
						suggested, priceErr := opts.CurrencyConversion.suggestedPriceUSD(n, opts.SuggestedPrice.SourceCurrency)
						if priceErr != nil {
							return nil, report, priceErr
						}
						if math.IsInf(suggested, 0) || math.IsNaN(suggested) {
							return nil, report, fmt.Errorf("SKU %s 建议售价换算后超出有效范围", sourceSKU)
						}
						m["建议售价（USD）"] = strconv.FormatFloat(suggested, 'f', 2, 64)
					}
				}
			}
			for k, val := range opts.Defaults {
				if m[k] == "" {
					m[k] = val
				}
			}
			// User-authorized temporary defaults; explicit configuration takes precedence.
			for k, val := range map[string]string{
				Headers[9]: "500", Headers[11]: "10", Headers[12]: "10",
				Headers[13]: "10", Headers[14]: "100",
				"发货时效（天）": "9", "产地": "中国-广东省",
			} {
				if m[k] == "" {
					m[k] = val
				}
			}
			for k, val := range opts.Overrides[sourceSKU] {
				m[k] = val
			}
			// Defaults and overrides must not restore duplicate carousel entries either.
			finalImages := imagefilter.List(m["*轮播图"])
			if len(finalImages) > maxCarouselImages {
				finalImages = finalImages[:maxCarouselImages]
			}
			m["*轮播图"] = strings.Join(finalImages, "\n")
			// All transformations and overrides run before the final required-field guard.
			// In particular, a filtered 403 override must not erase the material fallback.
			if strings.TrimSpace(m["预览图"]) == "" && len(finalImages) > 0 {
				m["预览图"] = finalImages[0]
			}
			if m["*轮播图"] == "" {
				fallback := imagefilter.List(strings.Join([]string{m["预览图"], m["*产品素材图"], strings.Join(imgs, "\n")}, "\n"))
				if len(fallback) == 0 && opts.FallbackImages[h] != "" {
					fallback = []string{opts.FallbackImages[h]}
					warn("图片全部为 403，为保证必填图片列保留一个原链接，店小秘仍可能抓取失败：" + fallback[0])
				}
				if len(fallback) > maxCarouselImages {
					fallback = fallback[:maxCarouselImages]
				}
				m["*轮播图"] = strings.Join(fallback, "\n")
				if strings.TrimSpace(m["预览图"]) == "" && len(fallback) > 0 {
					m["预览图"] = fallback[0]
				}
			}
			if strings.TrimSpace(m["*产品素材图"]) == "" {
				if previews := imagefilter.List(m["预览图"]); len(previews) > 0 {
					m["*产品素材图"] = previews[0]
				}
			}
			if opts.CleanProductCode == nil || *opts.CleanProductCode {
				original := m["产品货号"]
				m["产品货号"] = cleanProductCode(original)
				if i == 0 && original != m["产品货号"] {
					warn(fmt.Sprintf("产品货号已去除店小秘不支持的特殊字符：%q → %q", original, m["产品货号"]))
				}
			}
			cleanCopy(m, imagefilter.List(m["*轮播图"]), h)
			// 店小秘 SKU 不支持 Emoji。这是目标平台硬性规则，不受“去中文”配置影响。
			m["SKU货号"] = stripEmoji(m["SKU货号"])
			if opts.RemoveChineseInSKU == nil || *opts.RemoveChineseInSKU {
				m["SKU货号"] = removeChinese(m["SKU货号"])
			}
			finalSKU := m["SKU货号"]
			if finalSKU == "" {
				warn("SKU 货号缺失或清理中文、Emoji 后为空")
			} else if seen[finalSKU] {
				warn("SKU 货号重复")
			}
			seen[finalSKU] = true
			for _, k := range Headers {
				if (strings.HasPrefix(k, "*") || k == "产地") && strings.TrimSpace(m[k]) == "" {
					warn("缺少必填字段：" + k)
				}
			}
			for _, k := range []string{Headers[9], Headers[11], Headers[12], Headers[13], Headers[14]} {
				if m[k] != "" {
					n, e := strconv.ParseFloat(m[k], 64)
					if e != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
						warn("字段须为正数：" + k)
					}
				}
			}
			if m["*产品素材图"] != "" {
				warn("请人工确认素材图为 1:1 且大于 800×800px")
			}
			if m["识别码"] != "" && m["识别码类型"] == "" {
				warn("存在条码但缺少识别码类型，请填写 UPC/EAN/ISBN")
			}
			values := make([]string, len(Headers))
			for i, k := range Headers {
				values[i] = m[k]
				if len([]rune(values[i])) > 32767 {
					return nil, report, fmt.Errorf("SKU %s 字段 %s 超过 Excel 单元格长度限制", sourceSKU, k)
				}
			}
			out = append(out, values)
		}
	}
	report.Variants = len(out)
	return out, report, nil
}

func removeChinese(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Han, r) {
			return -1
		}
		return r
	}, s)
}

func optionName(s string) string {
	switch strings.ToLower(s) {
	case "color", "colour":
		return "颜色"
	case "size":
		return "尺寸"
	}
	return s
}

// weightGrams 处理重量。Shopify 未维护重量时导出 0，而 Temu 要求重量为正数，
// 把 0 视作缺失，交给 defaults 或临时默认值补齐，而不是输出一个必然报错的 0。
func weightGrams(s string) string {
	if n, err := strconv.ParseFloat(s, 64); err == nil && n == 0 {
		return ""
	}
	return s
}
