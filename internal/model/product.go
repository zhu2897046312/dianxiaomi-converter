// Package model 定义与来源、目标平台都无关的统一商品模型。
//
// 来源解析器（如 Shopify）负责把各自的 CSV 结构还原成这里的 Product；
// 各目标导出器只读取 Product，不接触任何来源 CSV 的列名。
// 这样新增来源或目标时，只需各自实现一端，而不用在同一个转换函数里堆叠平台分支。
package model

// Product 是一个商品及其全部变种、图片。
type Product struct {
	Handle      string
	Title       string
	Description string // 原始内容，可能含 HTML
	Status      string // 来源平台的原始状态值，由目标导出器自行映射

	// Images 是商品级图片，已按来源顺序排序、去空、去重。
	// 不包含只出现在变种上的图片（见 Variant.Image），是否合并由导出器决定。
	Images   []ProductImage
	Variants []Variant
}

type ProductImage struct {
	URL      string
	Position int // 来源中的位置；来源未提供或非法时为 0
}

// Option 是一组变种属性，例如 Color=Red。
type Option struct {
	Name  string
	Value string
}

type Variant struct {
	SKU string

	// Options 按来源顺序保存全部变种属性，Options[0] 对应第一组；末尾空属性已去掉。
	// 模型不限制数量，目标平台的上限（如店小秘只支持两组）由对应导出器检查。
	Options []Option
	// IsDefault 表示该变种只有来源平台的“单规格占位属性”，并非真实规格，
	// 例如 Shopify 的 Title=Default Title。Options 中仍保留原值，由导出器决定是否输出。
	IsDefault bool

	Price        string
	ComparePrice string

	WeightGrams string
	Barcode     string

	InventoryQty string
	// ManageInventory / AllowBackorder 是已按来源规则解释好的语义值，
	// 导出器不需要知道来源平台用什么字符串表示“跟踪库存”“缺货可下单”。
	ManageInventory bool
	AllowBackorder  bool

	Image string // 变种专属图片
}

// Issue 是转换过程中需要人工处理的问题，Row 为输出文件中的行号（含表头，数据从 2 开始）。
type Issue struct {
	Row     int    `json:"output_row"`
	SKU     string `json:"sku"`
	Message string `json:"message"`
}

type Report struct {
	Target   string  `json:"target"`
	Products int     `json:"products"`
	Variants int     `json:"variants"`
	Issues   []Issue `json:"issues"`
}
