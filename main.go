package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Config struct {
	// PriceColumn 是旧版配置项，与 SourceFields.Price 含义相同，仅为兼容保留，见 effectiveSourceFields。
	PriceColumn     string                       `json:"price_column"`
	PriceMultiplier float64                      `json:"price_multiplier"`
	Defaults        map[string]string            `json:"defaults"`
	Overrides       map[string]map[string]string `json:"sku_overrides"`
	SourceFields    SourceFields                 `json:"source_fields"`
}

// SourceFields 把转换逻辑用到的“逻辑字段”映射到 CSV 的实际列名。
// convert 及其辅助函数只通过这些逻辑字段取值，换一种来源 CSV 只需改配置，不需改代码。
// 值为空字符串表示不读取该字段（仅对可选字段有效）。
type SourceFields struct {
	Handle      string `json:"handle"`
	Title       string `json:"title"`
	Description string `json:"description"`

	SKU string `json:"sku"`

	Option1Name  string `json:"option1_name"`
	Option1Value string `json:"option1_value"`
	Option2Name  string `json:"option2_name"`
	Option2Value string `json:"option2_value"`
	Option3Name  string `json:"option3_name"`
	Option3Value string `json:"option3_value"`

	Price        string `json:"price"`
	WeightGrams  string `json:"weight_grams"`
	InventoryQty string `json:"inventory_qty"`
	Barcode      string `json:"barcode"`

	ImageURL        string `json:"image_url"`
	ImagePosition   string `json:"image_position"`
	VariantImageURL string `json:"variant_image_url"`
}

// defaultSourceFields 返回 Shopify 官方商品导出 CSV 的表头。
// 这是项目中唯一允许出现 Shopify 表头字符串的位置。
func defaultSourceFields() SourceFields {
	return SourceFields{
		Handle:      "Handle",
		Title:       "Title",
		Description: "Body (HTML)",

		SKU: "Variant SKU",

		Option1Name:  "Option1 Name",
		Option1Value: "Option1 Value",
		Option2Name:  "Option2 Name",
		Option2Value: "Option2 Value",
		Option3Name:  "Option3 Name",
		Option3Value: "Option3 Value",

		Price:        "Variant Price",
		WeightGrams:  "Variant Grams",
		InventoryQty: "Variant Inventory Qty",
		Barcode:      "Variant Barcode",

		ImageURL:        "Image Src",
		ImagePosition:   "Image Position",
		VariantImageURL: "Variant Image",
	}
}

type sourceColumn struct {
	key      string // source_fields 中的 JSON 键名，用于报错提示
	column   string
	required bool
}

// columns 列出转换用到的全部源字段。
// 必需字段缺失时无法分组（handle）、无法区分 SKU 行（sku、option1_value）、
// 无法生成店小秘必填项（title、image_url），因此直接报错；
// 其余字段缺失时对应店小秘列留空或交给 defaults 补齐，不阻断转换，
// 这样字段较少的非 Shopify CSV 也能只配置自己有的列。
func (f SourceFields) columns() []sourceColumn {
	return []sourceColumn{
		{"handle", f.Handle, true},
		{"title", f.Title, true},
		{"description", f.Description, false},
		{"sku", f.SKU, true},
		{"option1_name", f.Option1Name, false},
		{"option1_value", f.Option1Value, true},
		{"option2_name", f.Option2Name, false},
		{"option2_value", f.Option2Value, false},
		{"option3_name", f.Option3Name, false},
		{"option3_value", f.Option3Value, false},
		{"price", f.Price, false},
		{"weight_grams", f.WeightGrams, false},
		{"inventory_qty", f.InventoryQty, false},
		{"barcode", f.Barcode, false},
		{"image_url", f.ImageURL, true},
		{"image_position", f.ImagePosition, false},
		{"variant_image_url", f.VariantImageURL, false},
	}
}

// effectiveSourceFields 把旧版 price_column 归一到 SourceFields.Price，
// 之后全部价格读取只走 SourceFields.Price 一条路径。
// 优先级：price_column 非空时优先。因为 price_column 只可能由用户显式填写，
// 而 SourceFields.Price 可能只是 Shopify 默认值，无法区分是否为用户意图。
func (c Config) effectiveSourceFields() SourceFields {
	sf := c.SourceFields
	if c.PriceColumn != "" {
		sf.Price = c.PriceColumn
	}
	return sf
}

func defaultConfig() Config {
	return Config{PriceMultiplier: 1, Defaults: map[string]string{}, SourceFields: defaultSourceFields()}
}

// loadConfig 先填入默认配置再解码 JSON。encoding/json 只覆盖 JSON 中出现的键，
// 所以用户只写部分 source_fields 时，其余字段仍保留 Shopify 默认表头，不会被清空。
func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	b, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

type Issue struct {
	Row     int    `json:"output_row"`
	SKU     string `json:"sku"`
	Message string `json:"message"`
}
type Report struct {
	Products int     `json:"products"`
	Variants int     `json:"variants"`
	Issues   []Issue `json:"issues"`
}
type record map[string]string

// get 按实际列名取值；列名为空表示该逻辑字段未启用。
func (r record) get(column string) string {
	if column == "" {
		return ""
	}
	return r[column]
}

type product struct {
	title, description, handle string
	// Shopify 只在商品第一行写选项名称，后续变种行留空，所以在商品级保存一份用于补齐。
	option1Name, option2Name string
	rows                     []record // SKU 行
	images                   []record // 带图片 URL 的行（可能同时也是 SKU 行）
}

// 店小秘 *轮播图 最多接受 10 张图片链接。
const maxCarouselImages = 10

var headers = []string{"*产品标题", "*英文标题", "产品描述", "产品货号", "*变种属性名称一", "*变种属性值一", "变种属性名称二", "变种属性值二", "预览图", "*申报价格\n(店铺币种)", "SKU货号", "*长（cm）", "*宽（cm）", "*高（cm）", "*重量（g）", "识别码类型", "识别码", "站外产品链接", "*轮播图", "*产品素材图", "外包装形状", "外包装类型", "外包装图片", "建议售价（USD）", "库存", "发货时效（天）", "是否定制品", "产品视频url", "描述视频url", "产品说明书", "说明书语种", "SKU分类类型", "SKU分类数量", "SKU分类单位", "是否独立包装", "单品净含量", "单品净含量单位", "内计共含件数", "是否同品", "总净含量", "总净含量单位", "包装清单", "包装清单数量", "是否敏感属性", "敏感属性值", "储电容量", "刀具长度", "刀刃角度", "液体容量", "来源URL", "产地"}

//go:embed templates/import_created_product_popTemu.xlsx
var embeddedTemplate []byte

func main() {
	pause := len(os.Args) == 1 || (len(os.Args) == 2 && !strings.HasPrefix(os.Args[1], "-"))
	err := run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "转换失败：", err)
	}
	if pause {
		fmt.Println("按回车键关闭窗口……")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
	}
	if err != nil {
		os.Exit(1)
	}
}

func nextOutput(input string) (string, error) {
	base := strings.TrimSuffix(input, filepath.Ext(input)) + "_店小秘"
	for i := 0; ; i++ {
		path := base + ".xlsx"
		if i > 0 {
			path = fmt.Sprintf("%s_%d.xlsx", base, i)
		}
		available := true
		for _, candidate := range []string{path, path + ".report.json"} {
			_, err := os.Stat(candidate)
			if err == nil {
				available = false
			} else if !os.IsNotExist(err) {
				return "", err
			}
		}
		if available {
			return path, nil
		}
	}
}
func run() error {
	input := flag.String("input", "", "采集 CSV（UTF-8）")
	template := flag.String("template", "", "自定义店小秘模板（默认使用内置模板）")
	output := flag.String("output", "", "输出 XLSX（默认在 CSV 旁生成，不覆盖已有文件）")
	config := flag.String("config", "", "JSON 配置")
	strict := flag.Bool("strict", false, "存在待处理问题时不输出 XLSX")
	flag.Parse()
	args := flag.Args()
	if len(args) > 2 {
		return fmt.Errorf("一次拖入一个 CSV；命令行用法：tool.exe 输入.csv 输出.xlsx")
	}
	if len(args) > 0 {
		if *input != "" {
			return fmt.Errorf("不能同时使用位置参数和 -input")
		}
		*input = args[0]
	}
	if len(args) > 1 {
		if *output != "" {
			return fmt.Errorf("不能同时使用位置参数和 -output")
		}
		*output = args[1]
	}
	if *input == "" {
		flag.Usage()
		return fmt.Errorf("请把采集任务.csv 拖到本程序图标上，或运行：tool.exe 输入.csv 输出.xlsx")
	}
	if !strings.EqualFold(filepath.Ext(*input), ".csv") {
		return fmt.Errorf("输入文件必须是 .csv")
	}
	if *output == "" {
		var err error
		*output, err = nextOutput(*input)
		if err != nil {
			return err
		}
	}
	if !strings.EqualFold(filepath.Ext(*output), ".xlsx") {
		return fmt.Errorf("输出必须使用 .xlsx 后缀，例如 dianxiaomi.xlsx")
	}
	cfg := defaultConfig()
	if *config != "" {
		var e error
		if cfg, e = loadConfig(*config); e != nil {
			return e
		}
	}
	if cfg.PriceMultiplier <= 0 || math.IsNaN(cfg.PriceMultiplier) || math.IsInf(cfg.PriceMultiplier, 0) {
		return fmt.Errorf("price_multiplier 必须大于 0")
	}
	for k := range cfg.Defaults {
		if !known(k) {
			return fmt.Errorf("未知 defaults 字段 %s", k)
		}
	}
	for _, v := range cfg.Overrides {
		for k := range v {
			if !known(k) {
				return fmt.Errorf("未知 sku_overrides 字段 %s", k)
			}
		}
	}
	b, e := os.ReadFile(*input)
	if e != nil {
		return e
	}
	rows, report, e := convert(b, cfg)
	if e != nil {
		return e
	}
	reportPath := *output + ".report.json"
	if e = os.MkdirAll(filepath.Dir(*output), 0755); e != nil {
		return e
	}
	rb, e := json.MarshalIndent(report, "", "  ")
	if e != nil {
		return e
	}
	if e = writeNew(reportPath, rb); e != nil {
		return e
	}
	if *strict && len(report.Issues) > 0 {
		return fmt.Errorf("发现 %d 个待处理问题，未输出 XLSX，见 %s", len(report.Issues), reportPath)
	}
	if e = writeWorkbook(*template, *output, rows); e != nil {
		return e
	}
	fmt.Printf("完成：%d 个商品，%d 个 SKU，%d 个待处理问题\n输出：%s\n报告：%s\n", report.Products, report.Variants, len(report.Issues), *output, reportPath)
	return nil
}
func known(k string) bool {
	for _, h := range headers {
		if h == k {
			return true
		}
	}
	return false
}
func convert(b []byte, cfg Config) ([][]string, Report, error) {
	report := Report{Issues: []Issue{}}
	sf := cfg.effectiveSourceFields()
	if !utf8.Valid(b) {
		return nil, report, fmt.Errorf("CSV 必须为 UTF-8，请先另存为 CSV UTF-8")
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(b), "\ufeff")))
	all, e := reader.ReadAll()
	if e != nil {
		return nil, report, e
	}
	if len(all) < 2 {
		return nil, report, fmt.Errorf("CSV 没有数据")
	}
	cols := map[string]bool{}
	for _, s := range all[0] {
		if cols[s] {
			return nil, report, fmt.Errorf("重复列 %s", s)
		}
		cols[s] = true
	}
	for _, c := range sf.columns() {
		if !c.required {
			continue
		}
		if c.column == "" {
			return nil, report, fmt.Errorf("source_fields.%s 不能为空", c.key)
		}
		if !cols[c.column] {
			return nil, report, fmt.Errorf("缺少列 %s（source_fields.%s）", c.column, c.key)
		}
	}
	// 可选列缺失通常只是来源 CSV 没有该信息；但旧版 price_column 是用户显式指定的，
	// 找不到说明配置写错，静默忽略会导致申报价格悄悄变成默认值。
	if cfg.PriceColumn != "" && !cols[cfg.PriceColumn] {
		return nil, report, fmt.Errorf("价格列不存在：%s", cfg.PriceColumn)
	}
	products, e := groupProducts(all, sf)
	if e != nil {
		return nil, report, e
	}
	out := [][]string{}
	seen := map[string]bool{}
	titles := map[string]string{}
	for _, p := range products {
		h := p.handle
		report.Products++
		if len(p.rows) == 0 {
			return nil, report, fmt.Errorf("商品 %s 没有变种数据", h)
		}
		allImgs := carouselImages(p, sf)
		imgs := allImgs
		if len(imgs) > maxCarouselImages {
			imgs = imgs[:maxCarouselImages]
		}
		for i, r := range p.rows {
			rowNo := len(out) + 2
			sku := r.get(sf.SKU)
			warn := func(s string) { report.Issues = append(report.Issues, Issue{rowNo, sku, s}) }
			v1, v2 := r.get(sf.Option1Value), r.get(sf.Option2Value)
			m := map[string]string{
				"*产品标题":    p.title,
				"*英文标题":    p.title,
				"产品描述":     p.description,
				"产品货号":     h,
				"SKU货号":    sku,
				"*变种属性名称一": optionName(optionLabel(r.get(sf.Option1Name), p.option1Name, v1)),
				"*变种属性值一":  v1,
				"变种属性名称二":  optionName(optionLabel(r.get(sf.Option2Name), p.option2Name, v2)),
				"变种属性值二":   v2,
				"预览图":      r.get(sf.VariantImageURL),
				"*重量（g）":   weightGrams(r.get(sf.WeightGrams)),
				"库存":       r.get(sf.InventoryQty),
				"识别码":      r.get(sf.Barcode),
			}
			if old, ok := titles[p.title]; ok && old != h {
				warn("不同商品使用相同标题，店小秘可能合并，请修改标题")
			}
			titles[p.title] = h
			if sku == "" {
				warn("SKU 货号缺失")
			} else if seen[sku] {
				warn("SKU 货号重复")
			}
			seen[sku] = true
			// 店小秘模板只有两组变种属性。第三组无处安放，丢弃会让不同 SKU 变成相同属性组合，
			// 导入后无法区分，所以直接停止而不是静默截断。
			if v3 := r.get(sf.Option3Value); v3 != "" {
				return nil, report, fmt.Errorf("SKU %s 有第三变种属性 %s=%s，目标模板仅支持两种，停止转换以避免丢失", sku, r.get(sf.Option3Name), v3)
			}
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
			if v := r.get(sf.Price); v != "" {
				n, e := strconv.ParseFloat(v, 64)
				if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
					warn("价格不是有效数字：" + v)
				} else {
					m[headers[9]] = strconv.FormatFloat(n*cfg.PriceMultiplier, 'f', 2, 64)
				}
			}
			for k, v := range cfg.Defaults {
				if m[k] == "" {
					m[k] = v
				}
			}
			// User-authorized temporary defaults; explicit configuration takes precedence.
			for k, v := range map[string]string{
				headers[9]: "500", headers[11]: "10", headers[12]: "10",
				headers[13]: "10", headers[14]: "100",
			} {
				if m[k] == "" {
					m[k] = v
				}
			}
			// 第一版不做图片内容判断：素材图直接取预览图（已包含“商品首图”兜底）。
			if m["*产品素材图"] == "" {
				m["*产品素材图"] = m["预览图"]
			}
			for k, v := range cfg.Overrides[sku] {
				m[k] = v
			}
			for _, k := range headers {
				if strings.HasPrefix(k, "*") && m[k] == "" {
					warn("缺少必填字段：" + k)
				}
			}
			for _, k := range []string{headers[9], headers[11], headers[12], headers[13], headers[14]} {
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
			values := make([]string, len(headers))
			for i, k := range headers {
				values[i] = m[k]
				if len([]rune(values[i])) > 32767 {
					return nil, report, fmt.Errorf("SKU %s 字段 %s 超过 Excel 单元格长度限制", sku, k)
				}
			}
			out = append(out, values)
		}
	}
	report.Variants = len(out)
	return out, report, nil
}

// groupProducts 按 Handle 把 CSV 行聚合成商品，并保持商品首次出现的顺序。
//
// Shopify 导出中同一商品占多行：第一行带标题、描述和第一个变种；后续变种行只有
// Handle 与变种列；图片多于变种时，多出的图片各占一行，只有 Handle、Image Src、
// Image Position。因此“是否 SKU 行”和“是否带图片”必须是两个独立判断：
// 一行可以既是 SKU 行又带图片，也可以只是图片行。只在 SKU 行收集图片会丢掉
// 纯图片行上的轮播图；把纯图片行当 SKU 又会输出空白变种。
func groupProducts(all [][]string, sf SourceFields) ([]*product, error) {
	groups := map[string]*product{}
	var order []*product
	for i, line := range all[1:] {
		r := record{}
		for j, h := range all[0] {
			r[h] = strings.TrimSpace(line[j])
		}
		h := r.get(sf.Handle)
		if h == "" {
			return nil, fmt.Errorf("CSV 第 %d 条记录缺少 %s", i+2, sf.Handle)
		}
		p := groups[h]
		if p == nil {
			p = &product{handle: h}
			groups[h] = p
			order = append(order, p)
		}
		if t := r.get(sf.Title); t != "" {
			if p.title != "" && p.title != t {
				return nil, fmt.Errorf("商品 %s 存在冲突标题", h)
			}
			p.title = t
		}
		if d := r.get(sf.Description); d != "" {
			p.description = d
		}
		if n := r.get(sf.Option1Name); n != "" && p.option1Name == "" {
			p.option1Name = n
		}
		if n := r.get(sf.Option2Name); n != "" && p.option2Name == "" {
			p.option2Name = n
		}
		if isVariantRow(r, sf) {
			p.rows = append(p.rows, r)
		}
		if r.get(sf.ImageURL) != "" {
			p.images = append(p.images, r)
		}
	}
	return order, nil
}

// isVariantRow 判断一行是否代表一个 SKU。Shopify 纯图片行不填 SKU、选项值和价格，
// 三者任一非空即说明这一行在描述变种；只看 SKU 会漏掉未填货号的变种（后续报告“SKU 货号缺失”）。
func isVariantRow(r record, sf SourceFields) bool {
	return r.get(sf.SKU) != "" || r.get(sf.Option1Value) != "" || r.get(sf.Price) != ""
}

// carouselImages 返回商品级轮播图完整列表，商品下所有 SKU 共用。
//
//   - 按 Image Position 升序：CSV 行序不一定等于展示顺序，Position 才是 Shopify 的图片顺序。
//   - 使用稳定排序，同位置的图片保持 CSV 原顺序。
//   - 位置为空或不是整数的图片排在有效位置之后，不报错，避免个别脏数据阻断整个商品。
//   - 同一 URL 可能重复出现（例如变种行与图片行都写了同一张图），去重时保留排序后首次出现的位置。
//
// 不在这里截断到 10 张，由调用方截断并写入超限警告。
func carouselImages(p *product, sf SourceFields) []string {
	type img struct {
		url   string
		pos   int
		valid bool
	}
	list := make([]img, 0, len(p.images))
	for _, r := range p.images {
		pos, err := strconv.Atoi(r.get(sf.ImagePosition))
		list = append(list, img{r.get(sf.ImageURL), pos, err == nil})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].valid != list[j].valid {
			return list[i].valid
		}
		return list[i].valid && list[i].pos < list[j].pos
	})
	var urls []string
	seen := map[string]bool{}
	for _, it := range list {
		if it.url == "" || seen[it.url] {
			continue
		}
		seen[it.url] = true
		urls = append(urls, it.url)
	}
	return urls
}

// optionLabel 取变种属性名称。Shopify 只在商品第一行写选项名称，后续变种行为空，
// 所以行内为空且该行确有选项值时，回退到商品级名称；没有值则不输出名称。
func optionLabel(rowName, productName, value string) string {
	if rowName != "" {
		return rowName
	}
	if value != "" {
		return productName
	}
	return ""
}

// weightGrams 处理重量。Shopify 未维护重量时导出 0，而 Temu 要求重量为正数，
// 把 0 视作缺失，交给 defaults 或临时默认值补齐，而不是输出一个必然报错的 0。
func weightGrams(s string) string {
	if n, err := strconv.ParseFloat(s, 64); err == nil && n == 0 {
		return ""
	}
	return s
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
func writeNew(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func col(n int) string {
	s := ""
	for n > 0 {
		n--
		s = string(rune('A'+n%26)) + s
		n /= 26
	}
	return s
}
func escaped(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }

// Replace only data rows of the supplied template; all other ZIP entries stay intact.
func writeWorkbook(template, output string, rows [][]string) error {
	data := embeddedTemplate
	if template != "" {
		var err error
		data, err = os.ReadFile(template)
		if err != nil {
			return err
		}
	}
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return e
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	found := false
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			return e
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			return e
		}
		if f.Name == "xl/worksheets/sheet1.xml" {
			found = true
			s := string(b)
			start := strings.Index(s, "<sheetData>")
			end := strings.Index(s, "</sheetData>")
			if start < 0 || end < 0 {
				return fmt.Errorf("模板缺少 sheetData")
			}
			data := s[start+11 : end]
			firstEnd := strings.Index(data, "</row>")
			if firstEnd < 0 {
				return fmt.Errorf("模板缺少标题行")
			}
			var parsed struct {
				Rows []struct {
					Cells []struct {
						Inline struct {
							Text string `xml:"t"`
						} `xml:"is"`
					} `xml:"c"`
				} `xml:"row"`
			}
			if e := xml.Unmarshal([]byte("<sheetData>"+data+"</sheetData>"), &parsed); e != nil {
				return e
			}
			if len(parsed.Rows) == 0 || len(parsed.Rows[0].Cells) != len(headers) {
				return fmt.Errorf("模板列数与提供的 Temu 模板不一致")
			}
			for i, c := range parsed.Rows[0].Cells {
				if c.Inline.Text != headers[i] {
					return fmt.Errorf("模板第 %d 列标题不匹配", i+1)
				}
			}
			var d strings.Builder
			d.WriteString(data[:firstEnd+6])
			numeric := map[int]bool{9: true, 11: true, 12: true, 13: true, 14: true, 24: true}
			for i, row := range rows {
				fmt.Fprintf(&d, "<row r=\"%d\">", i+2)
				for j, v := range row {
					if v == "" {
						continue
					}
					ref := fmt.Sprintf("%s%d", col(j+1), i+2)
					n, err := strconv.ParseFloat(v, 64)
					if numeric[j] && err == nil && !math.IsInf(n, 0) && !math.IsNaN(n) {
						fmt.Fprintf(&d, "<c r=\"%s\" t=\"n\"><v>%s</v></c>", ref, escaped(v))
					} else {
						fmt.Fprintf(&d, "<c r=\"%s\" t=\"inlineStr\"><is><t xml:space=\"preserve\">%s</t></is></c>", ref, escaped(v))
					}
				}
				d.WriteString("</row>")
			}
			s = s[:start+11] + d.String() + s[end:]
			s = regexp.MustCompile(`<dimension ref="[^"]*"\s*/>`).ReplaceAllString(s, fmt.Sprintf(`<dimension ref="A1:AY%d"/>`, len(rows)+1))
			b = []byte(s)
		}
		h := f.FileHeader
		entry, e := w.CreateHeader(&h)
		if e != nil {
			return e
		}
		if _, e = entry.Write(b); e != nil {
			return e
		}
	}
	if !found {
		return fmt.Errorf("模板缺少第一工作表")
	}
	if e = w.Close(); e != nil {
		return e
	}
	return writeNew(output, buf.Bytes())
}
