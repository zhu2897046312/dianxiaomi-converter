package dianxiaomi

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
)

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

// Workbook 用 rows 替换模板第一张工作表的数据行并返回新的 XLSX 内容。
// 只替换数据行，其余 ZIP 条目（含“导入示例”页、样式、列宽）原样保留。
func Workbook(template []byte, rows [][]string) ([]byte, error) {
	if err := validateRequiredRows(rows); err != nil {
		return nil, err
	}
	z, e := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if e != nil {
		return nil, e
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	found := false
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(r)
		r.Close()
		if e != nil {
			return nil, e
		}
		if f.Name == "xl/worksheets/sheet1.xml" {
			found = true
			s := string(b)
			start := strings.Index(s, "<sheetData>")
			end := strings.Index(s, "</sheetData>")
			if start < 0 || end < 0 {
				return nil, fmt.Errorf("模板缺少 sheetData")
			}
			data := s[start+11 : end]
			firstEnd := strings.Index(data, "</row>")
			if firstEnd < 0 {
				return nil, fmt.Errorf("模板缺少标题行")
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
				return nil, e
			}
			if len(parsed.Rows) == 0 || len(parsed.Rows[0].Cells) != len(Headers) {
				return nil, fmt.Errorf("模板列数与提供的 Temu 模板不一致")
			}
			for i, c := range parsed.Rows[0].Cells {
				if c.Inline.Text != Headers[i] {
					return nil, fmt.Errorf("模板第 %d 列标题不匹配", i+1)
				}
			}
			var d strings.Builder
			d.WriteString(data[:firstEnd+6])
			numeric := map[int]bool{9: true, 11: true, 12: true, 13: true, 14: true, 23: true, 24: true, 25: true}
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
			return nil, e
		}
		if _, e = entry.Write(b); e != nil {
			return nil, e
		}
	}
	if !found {
		return nil, fmt.Errorf("模板缺少第一工作表")
	}
	if e = w.Close(); e != nil {
		return nil, e
	}
	return buf.Bytes(), nil
}

// Validate at the serialization boundary: even callers using Export directly
// cannot accidentally write a workbook whose mandatory cells are empty.
func validateRequiredRows(rows [][]string) error {
	var problems []string
	for i, row := range rows {
		if len(row) != len(Headers) {
			return fmt.Errorf("第 %d 行列数不正确", i+2)
		}
		for j, name := range Headers {
			if (strings.HasPrefix(name, "*") || name == "产地") && strings.TrimSpace(row[j]) == "" {
				problems = append(problems, fmt.Sprintf("第 %d 行（SKU %s）第 %d 列 %s 不能为空", i+2, row[10], j+1, name))
			}
		}
		for _, j := range []int{9, 11, 12, 13, 14} {
			if strings.TrimSpace(row[j]) == "" {
				continue
			}
			n, err := strconv.ParseFloat(row[j], 64)
			if err != nil || n <= 0 || math.IsNaN(n) || math.IsInf(n, 0) {
				problems = append(problems, fmt.Sprintf("第 %d 行第 %d 列 %s 必须为正数", i+2, j+1, Headers[j]))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("店小秘必填字段校验失败，未生成 XLSX；请补充可用图片或修正配置后重试：\n%s", strings.Join(problems, "\n"))
	}
	return nil
}
