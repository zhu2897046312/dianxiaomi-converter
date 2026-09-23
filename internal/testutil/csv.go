// Package testutil 提供各包测试共用的 CSV 构造工具，仅供测试使用。
package testutil

import (
	"encoding/csv"
	"strings"
)

// ShopifyHeader 是 Shopify 官方商品导出 CSV 中本工具会读取的列，另含几列无关列模拟真实导出。
var ShopifyHeader = []string{"Handle", "Title", "Body (HTML)", "Vendor", "Type", "Tags", "Status",
	"Option1 Name", "Option1 Value", "Option2 Name", "Option2 Value", "Option3 Name", "Option3 Value",
	"Variant SKU", "Variant Grams", "Variant Inventory Tracker", "Variant Inventory Qty", "Variant Inventory Policy",
	"Variant Price", "Variant Compare At Price", "Variant Barcode",
	"Image Src", "Image Position", "Variant Image", "Collection"}

// Row 以列名为键描述一行，只写关心的列，便于看清测试意图。
type Row map[string]string

// CSV 按表头生成带 BOM 的 UTF-8 CSV。
func CSV(header []string, rows ...Row) []byte {
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Write(header)
	for _, r := range rows {
		line := make([]string, len(header))
		for i, h := range header {
			line[i] = r[h]
		}
		w.Write(line)
	}
	w.Flush()
	return []byte("\ufeff" + b.String())
}

func Shopify(rows ...Row) []byte { return CSV(ShopifyHeader, rows...) }

// ImageRow 构造 Shopify 纯图片行：只有 Handle、Image Src、Image Position。
func ImageRow(handle, url, pos string) Row {
	return Row{"Handle": handle, "Image Src": url, "Image Position": pos}
}
