# Shopify CSV → 多目标格式转换
> Open-source companion utility for the [Sweet Potato Head Product Collection Tool](https://www.sweetphotohead.com/tools/collection-jobs).

Go 命令行项目（源码构建需要 Go 1.27.1+，EXE 无需安装 Go）。输入为 Shopify 官方商品导出 UTF-8 CSV（列名可通过配置适配其他来源），当前支持两个输出目标：

| target | 输出 | 说明 |
| --- | --- | --- |
| `all` | 同时输出以下两种格式 | 解析和图片检查仅执行一次，店小秘单独执行全 403 图片兜底 |
| `dianxiaomi`（默认/直接拖入） | 店小秘 Temu 模板 XLSX | 使用内置 `import_created_product_popTemu.xlsx`，保留列顺序、列宽及填写示例页 |
| `medusa` | Medusa Product Import CSV | 字段顺序与 `product-import-template` 一致，Option / 图片列按数据动态扩展 |

转换器按完整 URL 去重并检查图片。确认普通 GET 返回 HTTP 403 后，从模板内容中按规则过滤该 URL，同时自动调用同目录的 `image403-downloader.exe`，使用 Chrome 或 Edge 会话补下载到本地。下载器仍可独立接收 CSV、`urls.txt` 或单个 URL。工具不会上传图片或把本地路径写入店小秘模板。

## 运行（PowerShell）

```powershell
go test ./...
go build -o bin/dianxiaomi-converter.exe .
go build -o bin/image403-downloader.exe ./cmd/image403-downloader
# 默认：只生成店小秘（也可直接把 CSV 拖到 exe 上）
.\bin\dianxiaomi-converter.exe "采集任务-412-20260923.csv"
# 只生成店小秘
.\bin\dianxiaomi-converter.exe -target dianxiaomi "采集任务-412-20260923.csv"
# Medusa
.\bin\dianxiaomi-converter.exe -target medusa "采集任务-412-20260923.csv"
.\bin\dianxiaomi-converter.exe -target medusa -input shopify.csv -output products.csv
```

两个 EXE 必须放在同一目录。直接拖入时在 CSV 旁生成 `<名称>_转换结果` 目录；已有同名目录时依次使用 `_1`、`_2`，从不覆盖旧结果：

```text
<名称>_转换结果/
├─ <名称>_店小秘.xlsx
├─ <名称>_店小秘.xlsx.report.json
├─ <名称>_medusa.csv                 # output_medusa=true 时生成
├─ conversion-report.json
└─ 403-images/                       # 本批确认存在 403 时生成
   ├─ confirmed-403-urls.txt
   ├─ report.csv
   ├─ summary.json
   ├─ failed_urls.txt
   └─ images/*.png
```

在 `config.dianxiaomi.json` 顶层设置 `"output_medusa": true` 可同时生成 Medusa；默认 `false`。命令行显式 `-target dianxiaomi / medusa / all` 优先于此配置。每种输出生成 `<输出>.report.json`，整批另有 `conversion-report.json`。`-strict` 对存在问题的目标只生成报告并返回失败。显式 `-output` 时按指定路径输出，并把图片放在 `<输出文件名>_403-images`；`-target all` 不能搭配单个 `-output`。启用两个目标时，某一目标不支持数据会报告失败并继续生成另一目标。

输入必须是 UTF-8（可带 BOM）。Excel 另存的 GBK CSV 会明确报错，请另存为“CSV UTF-8”。

未指定 `-config` 时，会自动读取当前目录、程序旁边或 `bin` 上一级的 `config.dianxiaomi.json`。其顶层 `image_filter` 对两个目标生效，`dianxiaomi` 块只影响店小秘。来源字段仍按表头自动选择传统 Shopify 配置或采集配置（`URL handle / SKU / Product image URL`）。显式传入 `-config` 时使用指定配置覆盖自动识别结果。找不到外部配置时使用 exe 内置默认值。

## 直接拖入时的默认规则

以下规则是当前工具的最终行为。店小秘规则只修改店小秘 XLSX，不修改同批数据的 Medusa 内容。

| 项目 | 默认行为 | 可配置项 |
| --- | --- | --- |
| 入口与输出 | 把一个 Shopify/采集 CSV 拖到 `dianxiaomi-converter.exe`，生成独立结果目录；默认只输出店小秘 XLSX | 顶层 `output_medusa: true` 同时生成 Medusa；也可使用 `-target` |
| 店小秘模板 | 使用内置 `templates/import_created_product_popTemu.xlsx`，每次生成前核对表头、列数和顺序 | `-template` 可指定同结构模板 |
| 图片检测 | 完整 URL 去重后只检查一次；仅确认 HTTP 403 才过滤，超时或其他状态保留并报告 | `image_filter.timeout_seconds`、`image_filter.concurrency` |
| 403 补下载 | 主程序把已确认 403 的清单交给同目录下载器，不重复检测；浏览器下载后默认真正转码为 PNG | `download_403.enabled`、`download_403.output_format`、浏览器和超时配置 |
| 全部图片 403 | 店小秘保留来源顺序第一张原链接，补齐轮播图、预览图和产品素材图；不会重复填满 10 张 | 固定规则；报告会提示该图仍可能被店小秘拒绝 |
| 轮播图 | 商品图优先，再补不同的 SKU 图；去空、去重、最多 10 张；只有 1 张就只填 1 张 | `repeat_images_to_ten` 已停用 |
| 详情 GIF | `img/source` 中的 GIF 从最终轮播图最多 10 张里稳定随机替换，优先使用非 GIF；普通链接不改 | 店小秘专用规则 |
| 产品货号 | 只保留英文字母、数字、点、下划线和连字符 | `dianxiaomi.clean_product_code`，默认 `true` |
| SKU 货号 | Emoji 始终删除；中文汉字默认删除；数字、英文、空格、普通标点和括号保留 | `dianxiaomi.remove_chinese_in_sku` 只控制中文清理，默认 `true` |
| 店小秘文案 | 标题、描述正文、规格名/值等删除 Emoji；保留中文、英文、数字、普通标点、HTML 和普通链接 | 固定规则；Medusa 保留原文 |
| 申报价格 | 来源价 × `price_multiplier` × CNY 汇率；默认把来源价视为 USD，按 `USD × 7` 输出 CNY | `dianxiaomi.currency_conversion`、`price_multiplier` |
| 建议售价 | 默认把爬取价格直接填入“建议售价（USD）”，不乘 `price_multiplier` | `dianxiaomi.suggested_price.source_currency` 支持 USD、EUR、CNY 或 rates 中新增币种 |
| 店小秘库存 | 所有 SKU 统一填写 `200`，完全忽略 Shopify CSV 中的库存数量 | `dianxiaomi.inventory_quantity`，允许配置为 0 或其他非负整数 |
| 店小秘默认值 | 申报价 500；长宽高各 10 cm；重量 100 g；发货时效 9 天；产地 `中国-广东省` | `dianxiaomi.defaults` 或 `sku_overrides` |
| 必填保护 | 所有星号列和产地不能为空；申报价、尺寸、重量必须为正数；无法补齐时只写报告，不生成无效 XLSX | 固定规则，无需开启 `-strict` |

店小秘模板没有“运费模板”列，因此截图中的运费模板仍需在店小秘账号或产品模板中设置，不能通过当前 XLSX 写入。

## 图片过滤与去重

- 图片按完整 URL 去重后检测，同一个 URL 在多个商品、SKU、描述或配置中出现也只请求一次。保留查询参数，不把不同尺寸或版本擅自当成同一图片。
- 使用 GET 检查（HEAD 的结果可能不同），收到响应头后关闭响应体。**仅移除确定返回 403 的链接**；200、404、429 等其他状态保留；网络错误、超时不当作 403，会保留原链接并在报告中说明。
- 检查范围包括商品图、SKU 图、描述内的 `img src/data-src/srcset`、`picture source`，以及配置中的预览图、轮播图、素材图、包装图、图片/描述默认值和覆盖值。非 GIF 的描述标签含 403 图片候选地址时移除整个标签并保留文字；店小秘来源描述中的 403 GIF 标签暂时保留，随后替换为最终轮播图。Medusa 仍删除该 403 GIF 标签。
- 每个商品的轮播/图片列表只保留不同链接。店小秘最多 10 张，**只有 1 张就填 1 张，其他位置不补重复图**；Medusa 也不重复填入编号图片列。描述内重复 `img src` 只保留第一次。商品缩略图、SKU 预览图仍可引用同一张图，跨 SKU 共用商品图片属于正常关联，不会因此删掉商品或 SKU。
- 店小秘在过滤、默认配置、SKU 覆盖完成后补齐图片：预览图取有效轮播首图，素材图取最终预览图；空轮播可使用同商品剩余图片。若商品/SKU 图片全部为 403 且没有可用配置图片，保留按来源顺序第一张原图，各 SKU 的轮播、预览和素材图可共用这一个链接。不会重复填满 10 张，也不会恢复非 GIF 的 403 描述图片；403 GIF 按详情替换规则处理。报告会提示兜底原图仍可能被店小秘拒绝抓取。Medusa 仍删除全部 403 图片。
- 店小秘所有星号必填列及产地在写入 XLSX 前强制校验，申报价、尺寸和重量须为正数。源文件完全没有图片、清理 Emoji 后标题/规格为空、配置覆盖清空必填字段等无法补齐的情况，只生成错误报告，不生成无效 XLSX，无需开启 `-strict`。描述图片不会自动充当轮播/素材图。
- 报告 `image_filter.results` 记录每个 URL 的状态、是否从共享数据中移除及错误；店小秘保留原图的例外另在 `issues` 中记录。状态以本电脑当次请求为准，可能与店小秘服务器请求结果不同。
- 已确认 403 的 URL 写入 `403-images/confirmed-403-urls.txt`，随后由独立下载器使用 Chrome/Edge 补下载。主程序等待下载器结束再生成模板。某张图补下载失败时仍生成转换文件，并把失败写入目标报告和 `403-images/report.csv`；本地图片用于后续人工上传，不会替换成无效的本地路径。

可在 `config.dianxiaomi.json` 或显式配置中调整检查超时和并发：

```json
{"image_filter": {"timeout_seconds": 15, "concurrency": 6}}
```

超时允许 1–120 秒，并发允许 1–16。普通检查不读取或保存图片正文；只有确认 403 后才调用浏览器补下载。

## 独立使用 403 下载器

`image403-downloader.exe` 是同一仓库的第二个入口，可以继续单独拖入 Shopify/采集 CSV 或 `urls.txt`。独立模式先检查 URL，只对确认返回 403 的图片启动浏览器：

```powershell
.\bin\image403-downloader.exe shopify.csv
.\bin\image403-downloader.exe urls.txt
.\bin\image403-downloader.exe -url "https://example.com/image.jpg"
```

CSV 支持 `Image Src / Variant Image / Body (HTML)` 和 `Product image URL / Variant image URL / Description`。下载结果按 URL 的 SHA-256 命名，查询参数会参与去重和命名。默认输出 PNG；GIF 使用第一帧，JPEG/PNG/WebP 会真实解码后重新编码，不能只改扩展名。主转换器使用内部参数 `-confirmed-403` 将已检测清单交给下载器，用户通常无需手动使用该参数。

## 架构

```
Shopify CSV ─→ internal/source/shopify（按 source_fields 解析）
             ─→ internal/model（统一 Product / Variant）
             ─→ internal/imagefilter（图片 URL 检查、403 剔除、去重）
             ─→ image403-downloader.exe（浏览器补下载已确认 403，默认转 PNG）
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
| inventory_qty（Variant Inventory Qty） | 店小秘不使用该值；库存统一取 `dianxiaomi.inventory_quantity`（默认 200） |
| barcode（Variant Barcode） | 识别码；类型需显式配置 |

缺失申报价格默认 500，长宽高各 10 cm，重量 100 g，均为临时默认值；产品素材图使用最终预览图。库存不读取 Shopify 的 `Variant Inventory Qty`，所有 SKU 在最终写入时统一使用 `dianxiaomi.inventory_quantity`，默认 200；该专用配置优先于 `defaults` 和 `sku_overrides` 中可能存在的“库存”值。其他字段由 `dianxiaomi.defaults` 优先覆盖临时默认值，`sku_overrides` 优先级更高，但最终仍受图片兜底、去重、文案清理及必填校验约束。素材图是否 1:1 且大于 800×800px 需人工核验。默认会把 Variant Price 当作申报价格；若它是零售价，设置 `"source_fields": {"price": ""}` 关闭或用 `price_multiplier` 换算。

店小秘自动清理标题、描述正文、变种属性名/值、包装清单、敏感属性值、产地和 SKU 货号中的 Emoji（包括组合表情、肤色、旗帜、键帽及 HTML 编码表情）。文案保留普通文字、数字、标点、HTML 标签和非图片链接；SKU 的 Emoji 清理是店小秘固定规则，`remove_chinese_in_sku` 只决定是否额外删除汉字。产品货号默认只保留 `A-Z a-z 0-9 . _ -`，清理发生在 SKU 覆盖之后，防止覆盖值重新带入中文、Emoji、空格、斜杠或括号；设置 `"clean_product_code": false` 可关闭。

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

- `output_medusa`：顶层开关，默认 `false`。直接拖入 CSV 时只生成店小秘 XLSX；设为 `true` 后同时生成 Medusa CSV。命令行显式 `-target` 优先。

- `download_403`：默认启用，由主程序自动调用同目录的 `image403-downloader.exe`。常用配置如下：

  ```json
  {
    "download_403": {
      "enabled": true,
      "browser_timeout_seconds": 60,
      "max_image_mb": 25,
      "output_format": "png",
      "jpeg_quality": 90,
      "headless": true,
      "browser_path": "",
      "profile_dir": ""
    }
  }
  ```

  `output_format` 支持 `png`、`jpg`/`jpeg` 和 `original`；默认 `png`。`browser_path` 留空时自动查找 Chrome 或 Edge。`profile_dir` 留空时每次建立隔离临时浏览器配置并在任务结束后清理，避免并发任务争用；指定固定目录时不得同时运行多个使用相同目录的任务。设置 `enabled: false` 可只过滤 403 而不下载。

- `source_fields`：来源 CSV 哪一列是什么。未写的键保持 Shopify 默认。例如另一种来源：

  ```json
  { "source_fields": { "handle": "URL handle", "sku": "SKU", "image_url": "Product image URL", "image_position": "Image position" } }
  ```

- `dianxiaomi`：`price_multiplier`、`defaults`（仅填空白）、`sku_overrides`（按 SKU 覆盖）。键为模板列名（含星号、全角括号）；申报价列名为 `*申报价格\n(店铺币种)`，JSON 中 `\n` 表示换行。

  `inventory_quantity` 控制店小秘模板的统一库存，默认 `200`。程序忽略 Shopify CSV 中每个 SKU 的库存数量，并在所有 `defaults` 和 `sku_overrides` 处理完成后写入该值，因此不会出现同批 SKU 库存不一致。可配置为 0 或其他非负整数，负数会直接报错：

  ```json
  { "dianxiaomi": { "inventory_quantity": 200 } }
  ```

  `clean_product_code` 默认为 `true`：清理产品货号，只保留 `A-Z a-z 0-9 . _ -`。它在 `defaults` 和 `sku_overrides` 之后执行，因此覆盖值也不能重新带入中文、Emoji、空格、斜杠或括号。设置为 `false` 可保留来源或覆盖的原始产品货号。

  `repeat_images_to_ten` 已停用，保留键名仅用于兼容旧配置；无论旧配置为 `true` 还是 `false`，现在始终只保留实际不同图片，不重复补齐。完全无图时会报告缺图并阻止生成 XLSX；全 403 时按上述规则保留一个原链接。SKU 显式图片覆盖也必须经过 403 过滤及轮播去重。

  店小秘 SKU 货号始终删除 Emoji，因为目标平台不支持 Emoji。`remove_chinese_in_sku` 默认为 `true`，用于额外删除 SKU 中的中文汉字；数字、英文字母、空格、普通标点和括号保持不变。设置为 `false` 只会保留中文，仍会删除 Emoji。所有清理都在 `sku_overrides` 之后执行，覆盖值也不能重新带入 Emoji。清理后为空或多个 SKU 变成相同值时会写入问题报告。Medusa SKU 保留原值。

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
