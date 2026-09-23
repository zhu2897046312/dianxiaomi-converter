# 采集任务 → 店小秘 Temu 模板

Go 命令行项目，无第三方依赖。针对 Shopify 官方商品导出 UTF-8 CSV（列名可通过配置适配其他来源） 和 `import_created_product_popTemu.xlsx` 开发。使用原模板写入第一张工作表，保留列顺序、列宽及填写示例页。不访问网络、不执行源文件内容。

## 运行（PowerShell）

```powershell
cd C:\workspace\dianxiaomi-converter
go test ./...
go build -o bin/dxm-converter.exe .
.\bin\dxm-converter.exe -input "C:\Users\Administrator\Downloads\采集任务-11-20260920.csv" -output "output\dianxiaomi.xlsx"
```

输出 XLSX 及同路径 `.report.json` 问题报告。已有输出或报告不会覆盖，重复运行请换输出名称。默认允许输出待补全文件；`-strict` 在有任何问题时仅生成报告并返回失败。这是本地格式转换，不代表已通过店小秘在线导入验证。

## 映射规则

默认按 Shopify 官方商品导出 CSV 的表头读取，**不提供配置也能直接转换**。下表左列是逻辑字段（`source_fields` 的键）及其 Shopify 默认列名：

| 逻辑字段（默认列名） | 店小秘 |
| --- | --- |
| title（Title） | 产品标题、英文标题（沿用源文，不自动翻译） |
| description（Body (HTML)） | 产品描述（保留原始 HTML 文本） |
| handle（Handle） | 产品货号、商品归组依据 |
| sku（Variant SKU） | SKU 货号，按文本保留前导零 |
| option1/2_name、value（Option1/2 Name、Value） | 两组变种属性；Color/Colour→颜色，Size→尺寸；后续变种行名称为空时沿用商品第一行 |
| price（Variant Price） | 申报价格 × `price_multiplier` |
| variant_image_url（Variant Image） | 预览图；缺失时使用轮播图第一张 |
| image_url + image_position（Image Src、Image Position） | 轮播图：按位置排序、去空、去重、换行拼接，最多 10 张，超出写入报告 |
| weight_grams（Variant Grams） | 重量（g）；0 视为缺失 |
| inventory_qty（Variant Inventory Qty） | 库存 |
| barcode（Variant Barcode） | 识别码；类型需显式配置 |

按 handle 汇总商品，补齐变种的标题和描述。“SKU 行”（SKU、Option1 值、价格任一非空）与“图片行”（图片 URL 非空）分开判断：纯图片行不生成 SKU，但其图片计入该商品所有 SKU 的轮播图。第三变种属性会报错停止，防止静默丢失。不同 handle 的相同标题及重复 SKU 会进入问题报告。

必需列：handle、title、sku、option1_value、image_url，缺失时报错；其余列缺失时对应字段留空或由默认值补齐。

缺失申报价格默认 500（店铺币种），长宽高各 10 cm，重量 100 g，均为临时默认值；产品素材图使用预览图（即变种图，缺失时为商品首图）。配置 defaults 优先于这些默认值，sku_overrides 优先级最高。来源网址仍留空。产品素材图不能仅凭 URL 确定符合 1:1 且大于 800×800px；填写后仍需人工核验。

注意：默认会把 Shopify 的 Variant Price 当作申报价格。如果它是零售价而不是申报价，请在配置中设置 `"source_fields": {"price": ""}` 关闭，或用 `price_multiplier` 换算。

## 配置

通过 `-config config.json` 使用，所有键都可省略。`config.example.json` 列出了完整 `source_fields`（即 Shopify 默认值，正常使用 Shopify CSV 不需要写）。

- `source_fields`：只需写与默认值不同的列，未写的键保持 Shopify 默认。例如另一种来源 CSV：

  ```json
  {
    "source_fields": {
      "handle": "URL handle",
      "sku": "SKU",
      "option1_value": "Option1 value",
      "image_url": "Product image URL",
      "image_position": "Image position"
    }
  }
  ```

- `price_multiplier`：价格乘数，由用户明确指定，不自动查询汇率。
- `price_column`：旧版配置，等价于 `source_fields.price`；两者同时配置时 `price_column` 优先，且该列不存在会报错。
- `defaults` 仅填空白；`sku_overrides` 覆盖指定 SKU。键必须与模板标题完全一致（包括星号、全角括号）。申报价的精确字段名是 `*申报价格
(店铺币种)`，JSON 中 `
` 为换行。

```json
{
  "defaults": {
    "*长（cm）": "填写实测值",
    "*重量（g）": "填写实测值"
  },
  "sku_overrides": {
    "实际SKU": { "*产品素材图": "https://example.com/verified-square-image.jpg" }
  }
}
```

以上占位文字必须换成真实正数。

## 当前样例

57 条源记录组成 1 个商品、48 个 SKU、10 张商品图。默认生成的文件已补齐申报价格、长宽高、重量及产品素材图 URL；尺寸和重量是临时默认值，后续应更新为实测值。图片尺寸未联网验证，报告仍保留图片规格核验提示。输入支持 UTF-8/BOM、引号及多行字段；其他编码会明确报错。模板校验针对本次提供的 51 列 inline-string 模板；其他平台或变更后的模板需另行适配。

