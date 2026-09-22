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
	PriceColumn     string                       `json:"price_column"`
	PriceMultiplier float64                      `json:"price_multiplier"`
	Defaults        map[string]string            `json:"defaults"`
	Overrides       map[string]map[string]string `json:"sku_overrides"`
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
type product struct {
	title, description, handle string
	rows                       []record
	images                     []record
}

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
	cfg := Config{PriceMultiplier: 1, Defaults: map[string]string{}}
	if *config != "" {
		b, e := os.ReadFile(*config)
		if e != nil {
			return e
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if e = d.Decode(&cfg); e != nil {
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
	for _, s := range []string{"Title", "URL handle", "SKU", "Option1 value", "Product image URL"} {
		if !cols[s] {
			return nil, report, fmt.Errorf("缺少列 %s", s)
		}
	}
	if cfg.PriceColumn != "" && !cols[cfg.PriceColumn] {
		return nil, report, fmt.Errorf("价格列不存在：%s", cfg.PriceColumn)
	}
	groups := map[string]*product{}
	order := []string{}
	for i, line := range all[1:] {
		r := record{}
		for j, h := range all[0] {
			r[h] = strings.TrimSpace(line[j])
		}
		h := r["URL handle"]
		if h == "" {
			return nil, report, fmt.Errorf("CSV 第 %d 条记录缺少 URL handle", i+2)
		}
		p := groups[h]
		if p == nil {
			p = &product{handle: h}
			groups[h] = p
			order = append(order, h)
		}
		if r["Title"] != "" {
			if p.title != "" && p.title != r["Title"] {
				return nil, report, fmt.Errorf("商品 %s 存在冲突标题", h)
			}
			p.title = r["Title"]
		}
		if r["Description"] != "" {
			p.description = r["Description"]
		}
		if r["SKU"] != "" || r["Option1 value"] != "" || r["Price"] != "" {
			p.rows = append(p.rows, r)
		}
		if r["Product image URL"] != "" {
			p.images = append(p.images, r)
		}
	}
	out := [][]string{}
	seen := map[string]bool{}
	titles := map[string]string{}
	for _, h := range order {
		p := groups[h]
		report.Products++
		sort.SliceStable(p.images, func(i, j int) bool {
			a, _ := strconv.Atoi(p.images[i]["Image position"])
			b, _ := strconv.Atoi(p.images[j]["Image position"])
			return a < b
		})
		imgs := []string{}
		set := map[string]bool{}
		for _, r := range p.images {
			u := r["Product image URL"]
			if !set[u] {
				imgs = append(imgs, u)
				set[u] = true
			}
		}
		if len(p.rows) == 0 {
			return nil, report, fmt.Errorf("商品 %s 没有变种数据", h)
		}
		for _, r := range p.rows {
			rowNo := len(out) + 2
			sku := r["SKU"]
			warn := func(s string) { report.Issues = append(report.Issues, Issue{rowNo, sku, s}) }
			m := map[string]string{"*产品标题": p.title, "*英文标题": p.title, "产品描述": p.description, "产品货号": h, "SKU货号": sku, "*变种属性名称一": optionName(r["Option1 name"]), "*变种属性值一": r["Option1 value"], "变种属性名称二": optionName(r["Option2 name"]), "变种属性值二": r["Option2 value"], "预览图": r["Variant image URL"], "*重量（g）": r["Weight value (grams)"], "库存": r["Inventory quantity"], "识别码": r["Barcode"]}
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
			if r["Option3 value"] != "" {
				return nil, report, fmt.Errorf("SKU %s 有第三变种属性，目标模板仅支持两种，停止转换以避免丢失", sku)
			}
			if len(imgs) > 0 {
				n := len(imgs)
				if n > 10 {
					n = 10
					warn("轮播图超过 10 张，仅保留前 10 张")
				}
				m["*轮播图"] = strings.Join(imgs[:n], "\n")
				if m["预览图"] == "" {
					m["预览图"] = imgs[0]
				}
			}
			if cfg.PriceColumn != "" {
				v := r[cfg.PriceColumn]
				if v != "" {
					n, e := strconv.ParseFloat(v, 64)
					if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
						warn("价格不是有效数字：" + v)
					} else {
						m[headers[9]] = strconv.FormatFloat(n*cfg.PriceMultiplier, 'f', 2, 64)
					}
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
