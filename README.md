# Shopify CSV → 多目标格式转换
> Open-source companion utility for the [Sweet Potato Head Product Collection Tool](https://www.sweetphotohead.com/tools/collection-jobs).

Go 命令行项目（源码构建需要 Go 1.26+，EXE 无需安装 Go）。输入为 Shopify 官方商品导出 UTF-8 CSV（列名可通过配置适配其他来源），当前支持两个输出目标：

| target | 输出 | 说明 |
| --- | --- | --- |
| `all` | 同时输出以下两种格式 | 解析和图片检查仅执行一次，店小秘单独执行全 403 图片兜底 |
| `dianxiaomi`（默认/直接拖入） | 店小秘 Temu 模板 XLSX | 使用内置 `import_created_product_popTemu.xlsx`，保留列顺序、列宽及填写示例页 |
| `medusa` | Medusa Product Import CSV | 字段顺序与 `product-import-template` 一致，Option / 图片列按数据动态扩展 |

转换前联网检查图片，不执行 CSV 或 HTML 内容。仅检查 HTTP 响应状态，不下载图片文件，也不需要另外运行 image403-downloader。这是本地格式转换，不代表已通过目标平台在线导入验证。

## 运行（PowerShell）

```powershell
go test ./...
go build -o bin/dxm-converter.exe .
# 默认：只生成店小秘（也可直接把 CSV 拖到 exe 上）
.\bin\dxm-converter.exe "采集任务-412-20260923.csv"
# 只生成店小秘
.\bin\dxm-converter.exe -target dianxiaomi "采集任务-412-20260923.csv"
# Medusa
.\bin\dxm-converter.exe -target medusa "采集任务-412-20260923.csv"
.\bin\dxm-converter.exe -target medusa -input shopify.csv -output products.csv
```

默认在 CSV 旁生成 `<名称>_店小秘.xlsx`。在 `config.dianxiaomi.json` 顶层设置 `"output_medusa": true` 可同时生成 `<名称>_medusa.csv`；默认 `false`。命令行显式 `-target dianxiaomi / medusa / all` 优先于此配置。同名已存在时依次使用 `_1`、`_2`……，从不覆盖已有文件。每种输出生成 `<输出>.report.json` 问题报告（含 `target` 和 `image_filter`）。`-strict` 对存在问题的目标只生成报告并返回失败。单独指定目标时输出后缀必须匹配（`.xlsx` / `.csv`）。显式输出文件但未指定目标时，按 `.xlsx` / `.csv` 推断；`-target all` 不能搭配单个 `-output`。启用两个目标时，某一目标不支持数据会报告失败并继续生成另一目标。

输入必须是 UTF-8（可带 BOM）。Excel 另存的 GBK CSV 会明确报错，请另存为“CSV UTF-8”。

未指定 `-config` 时，会自动读取当前目录、程序旁边或 `bin` 上一级的 `config.dianxiaomi.json`。其顶层 `image_filter` 对两个目标生效，`dianxiaomi` 块只影响店小秘。来源字段仍按表头自动选择传统 Shopify 配置或采集配置（`URL handle / SKU / Product image URL`）。显式传入 `-config` 时使用指定配置覆盖自动识别结果。找不到外部配置时使用 exe 内置默认值。

## 图片过滤与去重

- 图片按完整 URL 去重后检测，同一个 URL 在多个商品、SKU、描述或配置中出现也只请求一次。保留查询参数，不把不同尺寸或版本擅自当成同一图片。
- 使用 GET 检查（HEAD 的结果可能不同），收到响应头后关闭响应体。**仅移除确定返回 403 的链接**；200、404、429 等其他状态保留；网络错误、超时不当作 403，会保留原链接并在报告中说明。
- 检查范围包括商品图、SKU 图、描述内的 `img src/data-src/srcset`、`picture source`，以及配置中的预览图、轮播图、素材图、包装图、图片/描述默认值和覆盖值。描述中的标签含 403 图片候选地址时移除整个对应图片标签，保留文字。
- 每个商品的轮播/图片列表只保留不同链接。店小秘最多 10 张，**只有 1 张就填 1 张，其他位置不补重复图**；Medusa 也不重复填入编号图片列。描述内重复 `img src` 只保留第一次。商品缩略图、SKU 预览图仍可引用同一张图，跨 SKU 共用商品图片属于正常关联，不会因此删掉商品或 SKU。
- 店小秘在过滤、默认配置、SKU 覆盖完成后补齐图片：预览图取有效轮播首图，素材图取最终预览图；空轮播可使用同商品剩余图片。若商品/SKU 图片全部为 403 且没有可用配置图片，保留按来源顺序第一张原图，各 SKU 的轮播、预览和素材图可共用这一个链接。不会重复填满 10 张，也不会恢复描述中的 403 图片。报告会提示该原图仍可能被店小秘拒绝抓取。Medusa 仍删除全部 403 图片。
- 店小秘所有星号必填列及产地在写入 XLSX 前强制校验，申报价、尺寸和重量须为正数。源文件完全没有图片、清理 Emoji 后标题/规格为空、配置覆盖清空必填字段等无法补齐的情况，只生成错误报告，不生成无效 XLSX，无需开启 `-strict`。描述图片不会自动充当轮播/素材图。
- 报告 `image_filter.results` 记录每个 URL 的状态、是否从共享数据中移除及错误；店小秘保留原图的例外另在 `issues` 中记录。状态以本电脑当次请求为准，可能与店小秘服务器请求结果不同。

可在 `config.dianxiaomi.json` 或显式配置中调整检查超时和并发：

```json
{"image_filter": {"timeout_seconds": 15, "concurrency": 6}}
```

超时允许 1–120 秒，并发允许 1–16。只有检查 HTTP 状态时联网，不调用浏览器补下载，也不上传文件。

## 架构

```
Shopify CSV ─→ internal/source/shopify（按 source_fields 解析）
             ─→ internal/model（统一 Product / Variant）
             ─→ internal/imagefilter（图片 URL 检查、403 剔除、去重）
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
| description（Body (HTML)） | 产品描述（保留 HTML）；GIF 图片 URL 稳定随机替换为最终轮播图中的图片 |
| handle（Handle） | 产品货号；默认只保留英文字母、数字、点、下划线和连字符 |
| sku（Variant SKU） | SKU 货号，保留前导零；默认删除中文汉字，其他字符保持不变 |
| option1/2（Option1/2 Name、Value） | 两组变种属性；Color/Colour→颜色，Size→尺寸；第三组报错停止 |
| price（Variant Price） | 申报价格换算为 CNY；建议售价写入原始价格对应的 USD 金额 |
| variant_image_url（Variant Image） | 预览图；缺失时用轮播图第一张 |
| image_url + image_position + variant_image_url | 轮播图：商品图优先，按 SKU 顺序补入不同的有效变种图，去重后最多 10 张，不重复补齐 |
| weight_grams（Variant Grams） | 重量（g）；0 视为缺失 |
| inventory_qty（Variant Inventory Qty） | 库存 |
| barcode（Variant Barcode） | 识别码；类型需显式配置 |

缺失申报价格默认 500，长宽高各 10 cm，重量 100 g，均为临时默认值；产品素材图使用最终预览图。dianxiaomi.defaults 优先于这些默认值，sku_overrides 优先级最高，但最终仍受图片兜底、去重、文案清理及必填校验约束。素材图是否 1:1 且大于 800×800px 需人工核验。默认会把 Variant Price 当作申报价格；若它是零售价，设置 `"source_fields": {"price": ""}` 关闭或用 `price_multiplier` 换算。

店小秘自动清理标题、描述正文、变种属性名/值、包装清单、敏感属性值及产地中的 Emoji（包括组合表情、肤色、旗帜、键帽及 HTML 编码表情）。保留普通文字、数字、标点、HTML 标签和非图片链接；SKU 仍遵循独立的去中文配置。产品货号默认只保留 `A-Z a-z 0-9 . _ -`，清理发生在 SKU 覆盖之后，防止覆盖值重新带入中文、Emoji、空格、斜杠或括号；设置 `"clean_product_code": false` 可关闭。

店小秘产品描述中 `img/source` 的 `src`、`data-src`、`srcset` 和 `data-srcset` 若指向 GIF，会从该商品最终输出的轮播图（最多 10 张）中稳定随机选图替换。优先选择非 GIF，替换结果对同一商品保持稳定；普通链接中的 `.gif` 不修改。即使原 GIF 返回 403，店小秘仍会用轮播图替换；其他 403 描述图片照常删除。Medusa 不执行产品货号清理、Emoji 清理或 GIF 替换，保持其原有规则。Unicode 数据许可见 `THIRD_PARTY_NOTICES.txt`。

店小秘默认值集中在 `config.dianxiaomi.json`。产地默认填写 `中国-广东省`（模板要求中国产地使用 `中国-省份`，其他国家/地区直接填写名称，如 `美国`），发货时效默认填写 `9` 天。两者都可在 `dianxiaomi.defaults` 中修改，按 SKU 的 `sku_overrides` 优先级更高：

```json
{
  "dianxiaomi": {
    "defaults": { "产地": "中国-浙江省", "发货时效（天）": "7" },
    "sku_overrides": { "SKU001": { "产地": "中国-江苏省" } }
  }
}
```

不提供配置时也会自动填写广东省和 9 天。当前官方 XLSX 中没有“运费模板”列，无法通过这个文件设置截图里的运费模板；需在店小秘账号或产品模板中维护。产地为空或仅含空白时会报告缺少必填字段并阻止生成 XLSX。

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

  `repeat_images_to_ten` 已停用，保留键名仅用于兼容旧配置；无论旧配置为 `true` 还是 `false`，现在始终只保留实际不同图片，不重复补齐。完全无图时会报告缺图并阻止生成 XLSX；全 403 时按上述规则保留一个原链接。SKU 显式图片覆盖也必须经过 403 过滤及轮播去重。

  `remove_chinese_in_sku` 默认为 `true`：导出前删除 SKU 中的中文汉字，数字、英文字母、空格、连字符、括号、Emoji 等其他字符保持不变。设置为 `false` 可完全保留来源 SKU。清理后为空或多个 SKU 变成相同值时会写入问题报告。

  `suggested_price` 单独控制模板的“建议售价（USD）”。默认开启且 `source_currency` 为 `USD`，因此直接写入爬取价格；可改为 `EUR` 或 `CNY`，程序会使用 `currency_conversion.rates` 交叉换算成 USD。设置 `enabled` 为 `false` 可不填写建议售价：

  ```json
  "suggested_price": {
    "enabled": true,
    "source_currency": "USD"
  }
  ```

  `currency_conversion` 控制来源价格换算为 CNY，默认开启，按 USD × 7 换算（例如 19.99 → 139.93 CNY），直接拖入 CSV 也生效。这里使用可修改的固定汇率，不是实时汇率。来源币种默认假定为 USD，不能从价格数字自动判断；欧元将 `source_currency` 改为 `EUR`，新增币种直接在 `rates` 添加汇率（键使用大写币种代码）。来源已经是 CNY 时将 `enabled` 设为 `false`：

  ```json
  "currency_conversion": {
    "enabled": true,
    "source_currency": "USD",
    "rates": { "USD": 7, "EUR": 10, "CNY": 1 }
  }
  ```

  最终申报价 = 来源价格 × `price_multiplier` × 对 CNY 汇率，保留两位小数。“建议售价（USD）”不使用 `price_multiplier`，其来源币种由 `suggested_price.source_currency` 单独决定。模板没有独立币种列，所以建议售价最终始终为 USD。关闭申报价换算时汇率按 1；默认价格和 SKU 覆盖价格视为目标币种，不再次换算。缺少所需汇率或汇率非正数会报错。使用外部配置无需重新编译：

  ```powershell
  .\main.exe -config config.shopify-collection.json -input "采集任务-25-20260923.csv"
  ```
- `medusa`：`price_currency`、`status_map`、`defaults`（键为 Medusa 列名，仅填空白）。
- 旧版顶层键继续兼容：`price_multiplier`、`defaults`、`sku_overrides` 作用于店小秘，与 `dianxiaomi` 块按键合并、同键以 `dianxiaomi` 块为准；`price_column` 等价于 `source_fields.price`，两者同时配置时 `price_column` 优先，且该列不存在会报错。
