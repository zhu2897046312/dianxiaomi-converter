package main

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dianxiaomi-converter/internal/model"
	tu "dianxiaomi-converter/internal/testutil"
)

func TestCLIOutputMedusaConfiguration(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer s.Close()
	for _, tc := range []struct {
		name, cfg, target string
		dxm, medusa       bool
	}{
		{"default", `{}`, "", true, false},
		{"disabled", `{"output_medusa":false}`, "", true, false},
		{"enabled", `{"output_medusa":true}`, "", true, true},
		{"explicit_dxm", `{"output_medusa":true}`, "dianxiaomi", true, false},
		{"explicit_medusa", `{"output_medusa":false}`, "medusa", false, true},
		{"explicit_all", `{"output_medusa":false}`, "all", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			input, config := filepath.Join(dir, "shopify.csv"), filepath.Join(dir, "config.json")
			if err := os.WriteFile(input, tu.Shopify(tu.Row{"Handle": "p", "Title": "🐶Product", "Variant SKU": "A", "Option1 Name": "Color", "Option1 Value": "Red", "Image Src": s.URL}), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config, []byte(tc.cfg), 0600); err != nil {
				t.Fatal(err)
			}
			oldArgs, oldFlags := os.Args, flag.CommandLine
			defer func() { os.Args, flag.CommandLine = oldArgs, oldFlags }()
			flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
			os.Args = []string{"test", "-config", config}
			if tc.target != "" {
				os.Args = append(os.Args, "-target", tc.target)
			}
			os.Args = append(os.Args, input)
			if err := run(); err != nil {
				t.Fatal(err)
			}
			resultDir := filepath.Join(dir, "shopify_转换结果")
			for path, want := range map[string]bool{"shopify_店小秘.xlsx": tc.dxm, "shopify_medusa.csv": tc.medusa} {
				_, err := os.Stat(filepath.Join(resultDir, path))
				if (err == nil) != want {
					t.Fatalf("%s: %v", path, err)
				}
			}
			if _, err := os.Stat(filepath.Join(resultDir, "conversion-report.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFailedExportWritesReportOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invalid.xlsx")
	err := writeExport(path, nil, model.Report{Target: "dianxiaomi", Issues: []model.Issue{{Message: "第20列不能为空"}}}, false)
	if err == nil {
		t.Fatal("expected export failure")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("invalid workbook created", err)
	}
	b, err := os.ReadFile(path + ".report.json")
	if err != nil || !strings.Contains(string(b), "第20列不能为空") {
		t.Fatal(string(b), err)
	}
}
