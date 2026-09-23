// 命令行入口：Shopify CSV → 统一商品模型 → 目标平台文件（店小秘 XLSX、Medusa CSV）。
package main

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dianxiaomi-converter/internal/exporter/dianxiaomi"
	"dianxiaomi-converter/internal/exporter/medusa"
	"dianxiaomi-converter/internal/model"
	"dianxiaomi-converter/internal/source/shopify"
)

// Config 按职责分块：source_fields 描述来源 CSV 哪一列是什么；
// dianxiaomi / medusa 各自描述目标输出缺失时填什么，互不影响。
type Config struct {
	// 以下 4 个顶层键是旧版配置，仅为兼容保留，归并规则见 sourceFields 与 dianxiaomiOptions。
	PriceColumn     string                       `json:"price_column"`
	PriceMultiplier float64                      `json:"price_multiplier"`
	Defaults        map[string]string            `json:"defaults"`
	Overrides       map[string]map[string]string `json:"sku_overrides"`

	SourceFields shopify.Fields     `json:"source_fields"`
	Dianxiaomi   dianxiaomi.Options `json:"dianxiaomi"`
	Medusa       medusa.Options     `json:"medusa"`
}

func defaultConfig() Config {
	return Config{
		PriceMultiplier: 1,
		Defaults:        map[string]string{},
		SourceFields:    shopify.DefaultFields(),
		Medusa:          medusa.DefaultOptions(),
	}
}

// loadConfig 先填入默认配置再解码 JSON。encoding/json 只覆盖 JSON 中出现的键（解码到已有 map 时只增改键），
// 所以用户只写部分 source_fields、status_map 或 defaults 时，其余项仍保留默认值，不会被清空。
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

// sourceFields 把旧版 price_column 归一到 source_fields.price，之后价格只从这一个字段读取。
// price_column 非空时优先：它只可能由用户显式填写，而 source_fields.price 可能只是 Shopify 默认值。
// 用户显式指定的价格列必须存在，因此同时返回需额外校验的字段键。
func (c Config) sourceFields() (shopify.Fields, []string) {
	f := c.SourceFields
	if c.PriceColumn != "" {
		f.Price = c.PriceColumn
		return f, []string{"price"}
	}
	return f, nil
}

// dianxiaomiOptions 合并旧版顶层键与新的 dianxiaomi 配置块，新配置块优先：
// price_multiplier 未在 dianxiaomi 中填写（为 0）时沿用顶层值；defaults、sku_overrides 按键合并，同键以新块为准。
func (c Config) dianxiaomiOptions() dianxiaomi.Options {
	o := dianxiaomi.Options{
		PriceMultiplier: c.Dianxiaomi.PriceMultiplier,
		Defaults:        map[string]string{},
		Overrides:       map[string]map[string]string{},
	}
	if o.PriceMultiplier == 0 {
		o.PriceMultiplier = c.PriceMultiplier
	}
	for _, src := range []map[string]string{c.Defaults, c.Dianxiaomi.Defaults} {
		for k, v := range src {
			o.Defaults[k] = v
		}
	}
	for _, src := range []map[string]map[string]string{c.Overrides, c.Dianxiaomi.Overrides} {
		for sku, fields := range src {
			if o.Overrides[sku] == nil {
				o.Overrides[sku] = map[string]string{}
			}
			for k, v := range fields {
				o.Overrides[sku][k] = v
			}
		}
	}
	return o
}

func (c Config) validate() error {
	if err := c.dianxiaomiOptions().Validate(); err != nil {
		return err
	}
	return c.Medusa.Validate()
}

// target 描述一个输出目标。各目标的业务规则都在各自的 exporter 包内，这里只做分发。
type target struct {
	suffix, ext string
	export      func(products []model.Product, cfg Config, template []byte) ([]byte, model.Report, error)
}

var targets = map[string]target{
	"dianxiaomi": {"_店小秘", ".xlsx", exportDianxiaomi},
	"medusa":     {"_medusa", ".csv", exportMedusa},
}

const defaultTarget = "dianxiaomi"

func exportDianxiaomi(products []model.Product, cfg Config, template []byte) ([]byte, model.Report, error) {
	rows, report, err := dianxiaomi.Export(products, cfg.dianxiaomiOptions())
	if err != nil {
		return nil, report, err
	}
	if template == nil {
		template = embeddedTemplate
	}
	b, err := dianxiaomi.Workbook(template, rows)
	return b, report, err
}

func exportMedusa(products []model.Product, cfg Config, _ []byte) ([]byte, model.Report, error) {
	header, rows, report := medusa.Export(products, cfg.Medusa)
	b, err := medusa.EncodeCSV(header, rows)
	return b, report, err
}

// convert 解析来源 CSV 并交给目标导出器，返回目标文件内容。template 为 nil 时使用内置店小秘模板。
func convert(b []byte, cfg Config, targetName string, template []byte) ([]byte, model.Report, error) {
	t, ok := targets[targetName]
	if !ok {
		return nil, model.Report{}, fmt.Errorf("未知 target %q，可选：%s", targetName, targetNames())
	}
	fields, extra := cfg.sourceFields()
	products, err := shopify.Parse(b, fields, extra...)
	if err != nil {
		return nil, model.Report{Target: targetName, Issues: []model.Issue{}}, err
	}
	return t.export(products, cfg, template)
}

func targetNames() string {
	var names []string
	for k := range targets {
		names = append(names, k)
	}
	sort.Strings(names)
	return strings.Join(names, "、")
}

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

// nextOutput 在输入文件旁生成不与已有输出或报告重名的路径，例如 x_medusa.csv、x_medusa_1.csv。
func nextOutput(input, suffix, ext string) (string, error) {
	base := strings.TrimSuffix(input, filepath.Ext(input)) + suffix
	for i := 0; ; i++ {
		path := base + ext
		if i > 0 {
			path = fmt.Sprintf("%s_%d%s", base, i, ext)
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
	targetName := flag.String("target", defaultTarget, "输出目标："+targetNames())
	template := flag.String("template", "", "自定义店小秘模板（默认使用内置模板，仅 dianxiaomi）")
	output := flag.String("output", "", "输出文件（默认在 CSV 旁生成，不覆盖已有文件）")
	config := flag.String("config", "", "JSON 配置")
	strict := flag.Bool("strict", false, "存在待处理问题时不输出目标文件")
	flag.Parse()
	t, ok := targets[*targetName]
	if !ok {
		return fmt.Errorf("未知 -target %q，可选：%s", *targetName, targetNames())
	}
	args := flag.Args()
	if len(args) > 2 {
		return fmt.Errorf("一次拖入一个 CSV；命令行用法：tool.exe [-target medusa] 输入.csv [输出文件]")
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
	if *template != "" && *targetName != "dianxiaomi" {
		return fmt.Errorf("-template 仅适用于 -target dianxiaomi")
	}
	if *output == "" {
		var err error
		if *output, err = nextOutput(*input, t.suffix, t.ext); err != nil {
			return err
		}
	}
	if !strings.EqualFold(filepath.Ext(*output), t.ext) {
		return fmt.Errorf("target %s 的输出必须使用 %s 后缀", *targetName, t.ext)
	}
	cfg := defaultConfig()
	if *config != "" {
		var e error
		if cfg, e = loadConfig(*config); e != nil {
			return e
		}
	}
	if e := cfg.validate(); e != nil {
		return e
	}
	var tpl []byte
	if *template != "" {
		var e error
		if tpl, e = os.ReadFile(*template); e != nil {
			return e
		}
	}
	b, e := os.ReadFile(*input)
	if e != nil {
		return e
	}
	data, report, e := convert(b, cfg, *targetName, tpl)
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
		return fmt.Errorf("发现 %d 个待处理问题，未输出目标文件，见 %s", len(report.Issues), reportPath)
	}
	if e = writeNew(*output, data); e != nil {
		return e
	}
	fmt.Printf("完成（%s）：%d 个商品，%d 个 SKU，%d 个待处理问题\n输出：%s\n报告：%s\n", *targetName, report.Products, report.Variants, len(report.Issues), *output, reportPath)
	return nil
}

// writeNew 只创建新文件，已存在时报错，保证不覆盖用户已有的输出。
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
