package shopify

// Fields 把解析器用到的“逻辑字段”映射到 CSV 的实际列名。
// 解析器只通过这些逻辑字段取值，换一种列名不同的来源 CSV 只需改配置，不需改代码。
// 值为空字符串表示不读取该字段（仅对可选字段有效）。
type Fields struct {
	Handle      string `json:"handle"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`

	SKU string `json:"sku"`

	Option1Name  string `json:"option1_name"`
	Option1Value string `json:"option1_value"`
	Option2Name  string `json:"option2_name"`
	Option2Value string `json:"option2_value"`
	Option3Name  string `json:"option3_name"`
	Option3Value string `json:"option3_value"`

	Price          string `json:"price"`
	CompareAtPrice string `json:"compare_at_price"`

	WeightGrams string `json:"weight_grams"`
	Barcode     string `json:"barcode"`

	InventoryTracker string `json:"inventory_tracker"`
	InventoryQty     string `json:"inventory_qty"`
	InventoryPolicy  string `json:"inventory_policy"`

	ImageURL        string `json:"image_url"`
	ImagePosition   string `json:"image_position"`
	VariantImageURL string `json:"variant_image_url"`
}

// DefaultFields 返回 Shopify 官方商品导出 CSV 的表头。
// 这是项目中唯一允许出现 Shopify 表头字符串的位置。
func DefaultFields() Fields {
	return Fields{
		Handle:      "Handle",
		Title:       "Title",
		Description: "Body (HTML)",
		Status:      "Status",

		SKU: "Variant SKU",

		Option1Name:  "Option1 Name",
		Option1Value: "Option1 Value",
		Option2Name:  "Option2 Name",
		Option2Value: "Option2 Value",
		Option3Name:  "Option3 Name",
		Option3Value: "Option3 Value",

		Price:          "Variant Price",
		CompareAtPrice: "Variant Compare At Price",

		WeightGrams: "Variant Grams",
		Barcode:     "Variant Barcode",

		InventoryTracker: "Variant Inventory Tracker",
		InventoryQty:     "Variant Inventory Qty",
		InventoryPolicy:  "Variant Inventory Policy",

		ImageURL:        "Image Src",
		ImagePosition:   "Image Position",
		VariantImageURL: "Variant Image",
	}
}

type column struct {
	key      string // 配置中的 JSON 键名，用于报错提示
	name     string
	required bool
}

// columns 列出解析器用到的全部源字段。
// 必需字段缺失时无法分组（handle）、无法区分变种行（sku、option1_value）、
// 无法得到商品标题和图片（title、image_url），因此直接报错；
// 其余字段缺失时模型中对应值为空，不阻断解析，
// 这样字段较少的非 Shopify CSV 也能只配置自己有的列。
func (f Fields) columns() []column {
	return []column{
		{"handle", f.Handle, true},
		{"title", f.Title, true},
		{"description", f.Description, false},
		{"status", f.Status, false},
		{"sku", f.SKU, true},
		{"option1_name", f.Option1Name, false},
		{"option1_value", f.Option1Value, true},
		{"option2_name", f.Option2Name, false},
		{"option2_value", f.Option2Value, false},
		{"option3_name", f.Option3Name, false},
		{"option3_value", f.Option3Value, false},
		{"price", f.Price, false},
		{"compare_at_price", f.CompareAtPrice, false},
		{"weight_grams", f.WeightGrams, false},
		{"barcode", f.Barcode, false},
		{"inventory_tracker", f.InventoryTracker, false},
		{"inventory_qty", f.InventoryQty, false},
		{"inventory_policy", f.InventoryPolicy, false},
		{"image_url", f.ImageURL, true},
		{"image_position", f.ImagePosition, false},
		{"variant_image_url", f.VariantImageURL, false},
	}
}
