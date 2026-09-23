# Shopify CSV → 多目标格式转换
> Open-source companion utility for the [Sweet Potato Head Product Collection Tool](https://www.sweetphotohead.com/tools/collection-jobs).

Go 命令行项目，无第三方依赖。输入为 Shopify 官方商品导出 UTF-8 CSV（列名可通过配置适配其他来源），当前支持两个输出目标：

| target | 输出 | 说明 |
| --- | --- | --- |
| `dianxiaomi`（默认） | 店小秘 Temu 模板 XLSX | 使用内置 `import_created_product_popTemu.xlsx`，保留列顺序、列宽及填写示例页 |
| `medusa` | Medusa Product Import CSV | 字段顺序与 `product-import-template` 一致，Option / 图片列按数据动态扩展 |

不访问网络、不执行源文件内容。这是本地格式转换，不代表已通过目标平台在线导入验证。

## 运行（PowerShell）

```powershell
go test ./...
go build -o bin/dxm-converter.exe .
# 默认：店小秘（也可直接把 CSV 拖到 exe 上）
.\bin\dxm-converter.exe "采集任务-412-20260923.csv"
# Medusa
.\bin\dxm-converter.exe -target medusa "采集任务-412-20260923.csv"
.\bin\dxm-converter.exe -target medusa -input shopify.csv -output products.csv
```

默认在 CSV 旁生成 `<名称>_店小秘.xlsx` 或 `<名称>_medusa.csv`，同名已存在时依次使用 `_1`、`_2`……，从不覆盖已有文件。同时生成 `<输出>.report.json` 问题报告（含 `target`）。`-strict` 在有任何问题时仅生成报告并返回失败。输出后缀必须与 target 匹配（`.xlsx` / `.csv`）。

输入必须是 UTF-8（可带 BOM）。Excel 另存的 GBK CSV 会明确报错，请另存为“CSV UTF-8”。

## 架构

```
Shopify CSV ─→ internal/source/shopify（按 source_fields 解析）
             ─→ internal/model（统一 Product / Variant）
             ─→ internal/exporter/dianxiaomi | internal/exporter/medusa
```

- Shopify 表头只出现在 `shopify.DefaultFields()`；导出器只读统一模型。
- 目标平台的限制只在各自导出器内：店小秘最多 2 组属性、轮播图最多 10 张；Medusa 不限制。

## Shopify 解析规则（两个目标共用）

- 按 handle 聚合商品；标题、描述、状态取该商品中出现的非空值。
- “变种行”（SKU、Option1 值、价格任一非空）与“图片行”（图片 URL 非空）分开判断：纯图片行不生成变种，但其图片计入商品。
- 商品图片按 Image Position 升序（非法位置排后），去空、去重。
- 后续变种行选项名称为空时沿用商品第一行；`Title / Default Title` 标记为单规格占位。
- Inventory Tracker 非空 → 管理库存；Inventory Policy 为 `continue` → 允许超卖。
- 必需列：handle、title、sku、option1_value、image_url；其余列缺失时对应值为空。

## 店小秘映射

| 逻辑字段（Shopify 默认列） | 店小秘 |
| --- | --- |
| title（Title） | 产品标题、英文标题（不自动翻译） |
| description（Body (HTML)） | 产品描述（保留 HTML） |
| handle（Handle） | 产品货号 |
| sku（Variant SKU） | SKU 货号，保留前导零 |
| option1/2（Option1/2 Name、Value） | 两组变种属性；Color/Colour→颜色，Size→尺寸；第三组报错停止 |
| price（Variant Price） | 申报价格 × `price_multiplier` |
| variant_image_url（Variant Image） | 预览图；缺失时用轮播图第一张 |
| image_url + image_position | 轮播图：换行拼接，最多 10 张，超出写入报告 |
| weight_grams（Variant Grams） | 重量（g）；0 视为缺失 |
| inventory_qty（Variant Inventory Qty） | 库存 |
| barcode（Variant Barcode） | 识别码；类型需显式配置 |

缺失申报价格默认 500，长宽高各 10 cm，重量 100 g，均为临时默认值；产品素材图使用预览图。dianxiaomi.defaults 优先于这些默认值，sku_overrides 优先级最高。素材图是否 1:1 且大于 800×800px 需人工核验。默认会把 Variant Price 当作申报价格；若它是零售价，设置 `"source_fields": {"price": ""}` 关闭或用 `price_multiplier` 换算。

## Medusa 映射

一行一个变种，商品级字段在每行重复。

| Medusa 列 | 来源 |
| --- | --- |
| Product Id、Variant Id | 留空（新建商品由 Medusa 生成） |
| Product Handle / Title / Description | Handle / Title / Body (HTML)（保留 HTML） |
| Product Status | Status 经 `status_map`：active→published，draft→draft，archived→draft；未知或缺失→draft 并报告 |
| Product Thumbnail | 最终图片列表第一张 |
| Product Discountable | 默认 TRUE |
| Variant Title | 属性值用 ` / ` 连接，如 `Red / XL`；单规格占位为 `Default` |
| Variant SKU / Barcode | Variant SKU / Variant Barcode |
| Variant Allow Backorder | Inventory Policy = continue → TRUE，否则 FALSE |
| Variant Manage Inventory | Inventory Tracker 非空 → TRUE，否则 FALSE |
| Variant Weight | Variant Grams（克） |
| Variant Price {币种} | Variant Price 写入 `medusa.price_currency` 对应列（默认 USD），不换算汇率 |
| Variant Option N Name / Value | Shopify Option1–3，按需扩展到 N=3；单规格占位不导出 |
| Product Image N Url | 商品图（按位置）+ 未出现过的变种图，一格一张，列数按本批最多图片数扩展（至少 2 列） |

以下列默认留空，只能通过 `medusa.defaults` 填写真实值：Product Subtitle、尺寸、HS Code、原产国、MID Code、Material、Shipping Profile Id、Sales Channel、Collection Id、Type Id、Tag、External Id。

不映射到 Medusa 的 Shopify 数据：Vendor（不是材质）、Type / Collection / Tags（是名称文本，不是 Medusa ID）、Variant Inventory Qty（商品导入模板没有库存列，库存需另行导入）、Compare At Price（模板无对应列）、变种与图片的专属关系（模板无变种图片列，变种图已并入商品图片并在报告中提示一次）。

## 配置

通过 `-config config.json` 使用，所有键都可省略，只需写要覆盖的项；`config.example.json` 列出全部默认值。

- `source_fields`：来源 CSV 哪一列是什么。未写的键保持 Shopify 默认。例如另一种来源：

  ```json
  { "source_fields": { "handle": "URL handle", "sku": "SKU", "image_url": "Product image URL", "image_position": "Image position" } }
  ```

- `dianxiaomi`：`price_multiplier`、`defaults`（仅填空白）、`sku_overrides`（按 SKU 覆盖）。键为模板列名（含星号、全角括号）；申报价列名为 `*申报价格\n(店铺币种)`，JSON 中 `\n` 表示换行。
- `medusa`：`price_currency`、`status_map`、`defaults`（键为 Medusa 列名，仅填空白）。
- 旧版顶层键继续兼容：`price_multiplier`、`defaults`、`sku_overrides` 作用于店小秘，与 `dianxiaomi` 块按键合并、同键以 `dianxiaomi` 块为准；`price_column` 等价于 `source_fields.price`，两者同时配置时 `price_column` 优先，且该列不存在会报错。
