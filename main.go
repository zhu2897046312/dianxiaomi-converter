// 命令行入口：Shopify CSV → 统一商品模型 → 目标平台文件（店小秘 XLSX、Medusa CSV）。
package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"

	"dianxiaomi-converter/internal/downloader"
	"dianxiaomi-converter/internal/exporter/dianxiaomi"
	"dianxiaomi-converter/internal/exporter/medusa"
	"dianxiaomi-converter/internal/imagefilter"
	"dianxiaomi-converter/internal/model"
	"dianxiaomi-converter/internal/source/shopify"
)

// Config 按职责分块：source_fields 描述来源 CSV 哪一列是什么；
// dianxiaomi / medusa 各自描述目标输出缺失时填什么，互不影响。
type Config struct {
	OutputMedusa bool `json:"output_medusa"` // 直接拖入默认只输出店小秘；开启后同时输出 Medusa。
	// 以下 4 个顶层键是旧版配置，仅为兼容保留，归并规则见 sourceFields 与 dianxiaomiOptions。
	PriceColumn     string                       `json:"price_column"`
	PriceMultiplier float64                      `json:"price_multiplier"`
	Defaults        map[string]string            `json:"defaults"`
	Overrides       map[string]map[string]string `json:"sku_overrides"`

	SourceFields shopify.Fields      `json:"source_fields"`
	Dianxiaomi   dianxiaomi.Options  `json:"dianxiaomi"`
	Medusa       medusa.Options      `json:"medusa"`
	ImageFilter  imagefilter.Options `json:"image_filter"`
	Download403  downloader.Config   `json:"download_403"`
}

func defaultConfig() Config {
	suggestedPriceEnabled := true
	inventoryQuantity := 200
	return Config{
		PriceMultiplier: 1,
		Defaults:        map[string]string{},
		SourceFields:    shopify.DefaultFields(),
		Dianxiaomi: dianxiaomi.Options{
			InventoryQuantity: &inventoryQuantity,
			CurrencyConversion: dianxiaomi.CurrencyConversion{
				Enabled: true, SourceCurrency: "USD", Rates: map[string]float64{"USD": 7, "EUR": 10, "CNY": 1},
			},
			SuggestedPrice: dianxiaomi.SuggestedPrice{Enabled: &suggestedPriceEnabled, SourceCurrency: "USD"},
		},
		Medusa:      medusa.DefaultOptions(),
		ImageFilter: imagefilter.DefaultOptions(),
		Download403: downloader.DefaultConfig(),
	}
}

// decodeConfig 把 JSON 覆盖到给定基础配置。encoding/json 只覆盖 JSON 中出现的键，
// 所以店小秘专用配置无需重复 source_fields，仍可沿用按输入表头识别出的字段映射。
func decodeConfig(cfg Config, path string) (Config, error) {
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

func loadConfig(path string) (Config, error) { return decodeConfig(defaultConfig(), path) }

//go:embed config.shopify-collection.json
var collectionConfig []byte

// configForInput 先按必需表头选择内置字段映射，再叠加用户配置。
// 用户配置可以只写 dianxiaomi 块，无需复制来源 CSV 的完整字段映射。
func configForInput(b []byte, configPath string) (Config, error) {
	cfg := defaultConfig()
	header, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")))).Read()
	if err != nil {
		return cfg, err
	}
	cols := make(map[string]bool, len(header))
	for _, h := range header {
		cols[h] = true
	}
	matches := func(f shopify.Fields) bool {
		return cols[f.Handle] && cols[f.Title] && cols[f.SKU] && cols[f.Option1Value] && cols[f.ImageURL]
	}
	if matches(cfg.SourceFields) {
		if configPath != "" {
			return decodeConfig(cfg, configPath)
		}
		return cfg, nil
	}
	alternative := defaultConfig()
	if err := json.Unmarshal(collectionConfig, &alternative); err != nil {
		return cfg, fmt.Errorf("内置 Shopify 配置无效：%w", err)
	}
	if matches(alternative.SourceFields) {
		cfg = alternative
	}
	if configPath != "" {
		return decodeConfig(cfg, configPath)
	}
	return cfg, nil
}

// dianxiaomiConfigPath 查找可编辑的店小秘默认配置：当前目录、程序目录、程序目录的上一级。
// 这样根目录 main.exe 和 bin/dxm-converter.exe 都能自动使用同一份配置。
func dianxiaomiConfigPath() string {
	const name = "config.dianxiaomi.json"
	candidates := []string{name}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, name), filepath.Join(filepath.Dir(dir), name))
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil || seen[absolute] {
			continue
		}
		seen[absolute] = true
		if info, err := os.Stat(absolute); err == nil && !info.IsDir() {
			return absolute
		}
	}
	return ""
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
		RepeatImagesToTen:  c.Dianxiaomi.RepeatImagesToTen,
		RemoveChineseInSKU: c.Dianxiaomi.RemoveChineseInSKU,
		CleanProductCode:   c.Dianxiaomi.CleanProductCode,
		SourceDescriptions: c.Dianxiaomi.SourceDescriptions,
		CurrencyConversion: c.Dianxiaomi.CurrencyConversion,
		SuggestedPrice:     c.Dianxiaomi.SuggestedPrice,
		InventoryQuantity:  c.Dianxiaomi.InventoryQuantity,
		FallbackImages:     c.Dianxiaomi.FallbackImages,
		PriceMultiplier:    c.Dianxiaomi.PriceMultiplier,
		Defaults:           map[string]string{},
		Overrides:          map[string]map[string]string{},
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
	if err := c.ImageFilter.Validate(); err != nil {
		return err
	}
	if err := c.Download403.Validate(); err != nil {
		return fmt.Errorf("download_403: %w", err)
	}
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

func (c Config) defaultTargets() []string {
	if c.OutputMedusa {
		return []string{"dianxiaomi", "medusa"}
	}
	return []string{defaultTarget}
}

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

// convert is the pure parsing/export path for offline format tests.
// The CLI runs filterProducts once before dispatching to any exporter.
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
	names := []string{"all"}
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

// nextResultDir keeps every drag-and-drop run together without overwriting an earlier run.
func nextResultDir(input string) (string, error) {
	base := strings.TrimSuffix(input, filepath.Ext(input)) + "_转换结果"
	for i := 0; ; i++ {
		path := base
		if i > 0 {
			path = fmt.Sprintf("%s_%d", base, i)
		}
		_, err := os.Stat(path)
		if os.IsNotExist(err) {
			return path, nil
		}
		if err != nil {
			return "", err
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
	// Explicit CLI target/output choices take precedence over configured defaults.
	explicitTarget := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "target" {
			explicitTarget = true
		}
	})
	if !explicitTarget && *output != "" {
		if strings.EqualFold(filepath.Ext(*output), ".csv") {
			*targetName = "medusa"
		} else {
			*targetName = "dianxiaomi"
		}
	}
	selected := []string{*targetName}
	if *targetName == "all" {
		if *output != "" {
			return fmt.Errorf("-target all 不支持单个 -output；请指定 -target dianxiaomi 或 medusa")
		}
		selected = []string{"dianxiaomi", "medusa"}
	} else if _, ok := targets[*targetName]; !ok {
		return fmt.Errorf("未知 -target %q，可选：%s", *targetName, targetNames())
	}
	if *input == "" {
		flag.Usage()
		return fmt.Errorf("请把采集任务.csv 拖到本程序图标上，或运行：tool.exe 输入.csv 输出.xlsx")
	}
	if !strings.EqualFold(filepath.Ext(*input), ".csv") {
		return fmt.Errorf("输入文件必须是 .csv")
	}
	if *template != "" && *targetName != "dianxiaomi" && *targetName != "all" {
		return fmt.Errorf("-template 仅适用于 -target dianxiaomi")
	}
	if *output != "" && !strings.EqualFold(filepath.Ext(*output), targets[*targetName].ext) {
		return fmt.Errorf("target %s 的输出必须使用 %s 后缀", *targetName, targets[*targetName].ext)
	}
	b, e := os.ReadFile(*input)
	if e != nil {
		return e
	}
	configPath := *config
	if configPath == "" {
		configPath = dianxiaomiConfigPath()
	}
	cfg, e := configForInput(b, configPath)
	if e != nil {
		return e
	}
	if e := cfg.validate(); e != nil {
		return e
	}
	if !explicitTarget && *output == "" {
		selected = cfg.defaultTargets()
	}
	var tpl []byte
	if *template != "" {
		var e error
		if tpl, e = os.ReadFile(*template); e != nil {
			return e
		}
	}
	fields, extra := cfg.sourceFields()
	products, e := shopify.Parse(b, fields, extra...)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	resultDir := ""
	if *output == "" {
		resultDir, e = nextResultDir(*input)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(resultDir, 0755); e != nil {
			return e
		}
	}
	fmt.Println("检查图片 URL（同一链接只检查一次；过滤 HTTP 403；店小秘全 403 时保留一张原图）……")
	imageReport, e := filterProducts(ctx, products, &cfg, nil, func(done, total int, c imagefilter.Check) {
		if done%20 == 0 || done == total {
			fmt.Printf("图片检查：%d/%d\n", done, total)
		}
	})
	if e != nil {
		return e
	}
	fmt.Printf("图片检查完成：%d 个不同 URL；剔除 %d 个 403；%d 个检测失败（保留并记录）\n", imageReport.Checked, imageReport.Removed, imageReport.Failed)
	downloadIssue := ""
	if cfg.Download403.Enabled && imageReport.Removed > 0 {
		downloadDir := filepath.Join(resultDir, "403-images")
		if resultDir == "" {
			downloadDir = strings.TrimSuffix(*output, filepath.Ext(*output)) + "_403-images"
		}
		if err := downloadConfirmed403(ctx, imageReport, downloadDir, configPath); err != nil {
			downloadIssue = "403 图片补下载未全部完成：" + err.Error()
			fmt.Fprintln(os.Stderr, downloadIssue)
		}
	}
	var failures []string
	for _, name := range selected {
		t := targets[name]
		path := *output
		if path == "" {
			base := strings.TrimSuffix(filepath.Base(*input), filepath.Ext(*input))
			path = filepath.Join(resultDir, base+t.suffix+t.ext)
		}
		data, report, err := t.export(products, cfg, tpl)
		if err != nil {
			fmt.Printf("%s：%v\n", name, err)
			report.Issues = append(report.Issues, model.Issue{Message: err.Error()})
			data = nil
		}
		report.ImageFilter = imageReport
		if downloadIssue != "" {
			report.Issues = append(report.Issues, model.Issue{Message: downloadIssue})
		}
		for _, check := range imageReport.Results {
			if check.Error != "" {
				report.Issues = append(report.Issues, model.Issue{Message: "图片检查失败，保留原链接：" + check.URL + "；" + check.Error})
			}
		}
		if err := writeExport(path, data, report, *strict); err != nil {
			failures = append(failures, name+": "+err.Error())
		}
	}
	if resultDir != "" {
		if err := writeConversionSummary(resultDir, imageReport, cfg.Download403.Enabled, downloadIssue, selected); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "\n"))
	}
	return nil
}

func writeExport(path string, data []byte, report model.Report, strict bool) error {
	reportPath := path + ".report.json"
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	rb, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err = writeNew(reportPath, rb); err != nil {
		return err
	}
	if data == nil {
		return fmt.Errorf("转换失败，未输出目标文件，请查看报告：%s", reportPath)
	}
	if strict && len(report.Issues) > 0 {
		return fmt.Errorf("发现 %d 个待处理问题，未输出目标文件，见 %s", len(report.Issues), reportPath)
	}
	if err = writeNew(path, data); err != nil {
		return err
	}
	fmt.Printf("完成（%s）：%d 个商品，%d 个 SKU，%d 个待处理问题\n输出：%s\n报告：%s\n", report.Target, report.Products, report.Variants, len(report.Issues), path, reportPath)
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
