# 采集任务 → 店小秘 Temu 模板

Go 命令行项目，无第三方依赖。针对提供的 Shopify 风格 UTF-8 CSV 和 `import_created_product_popTemu.xlsx` 开发。使用原模板写入第一张工作表，保留列顺序、列宽及填写示例页。不访问网络、不执行源文件内容。

## 运行（PowerShell）

```powershell
cd C:\workspace\dianxiaomi-converter
go test ./...
go build -o bin/dxm-converter.exe .
.\bin\dxm-converter.exe -input "C:\Users\Administrator\Downloads\采集任务-11-20260920.csv" -output "output\dianxiaomi.xlsx"
```

输出 XLSX 及同路径 `.report.json` 问题报告。已有输出或报告不会覆盖，重复运行请换输出名称。默认允许输出待补全文件；`-strict` 在有任何问题时仅生成报告并返回失败。这是本地格式转换，不代表已通过店小秘在线导入验证。

## 映射规则

| CSV | 店小秘 |
| --- | --- |
| Title | 产品标题、英文标题（沿用源文，不自动翻译） |
| Description | 产品描述（保留原始 HTML 文本） |
| URL handle | 产品货号、商品归组依据 |
| SKU | SKU 货号，按文本保留前导零 |
| Option1/2 name、value | 两组变种属性；Color/Colour→颜色，Size→尺寸 |
| Variant image URL | 预览图；缺失时使用第一张商品图 |
| Product image URL | 按 Image position 排序、去重、换行拼接，最多 10 张 |
| Weight value (grams) | 重量（g），空缺不填 |
| Inventory quantity | 库存 |
| Barcode | 识别码；类型需显式配置 |

按 handle 汇总商品，补齐变种的标题和描述；纯图片行不生成 SKU。第三变种属性会报错停止，防止静默丢失。不同 handle 的相同标题及重复 SKU 会进入问题报告。

按用户要求，缺失申报价格默认 500（店铺币种），长宽高各 10 cm，重量 100 g，均为临时默认值；产品素材图使用变种图 URL，缺失时使用商品首图 URL。配置 defaults 优先于这些默认值，sku_overrides 优先级最高。来源网址仍留空。产品素材图不能仅凭 URL 确定符合 1:1 且大于 800×800px；填写后仍需人工核验。不会把 Shopify handle 猜成来源网址，也不会将零售价自动当成申报价。

## 配置

复制 `config.example.json`，通过 `-config config.json` 使用。`defaults` 仅填空白；`sku_overrides` 覆盖指定 SKU。键必须与模板标题完全一致（包括星号、全角括号）。例如，经确认源 Price 就是所需申报价格且币种一致后，可以使用：

```json
{
  "price_column": "Price",
  "price_multiplier": 1,
  "defaults": {
    "*长（cm）": "填写实测值",
    "*宽（cm）": "填写实测值",
    "*高（cm）": "填写实测值",
    "*重量（g）": "填写实测值"
  },
  "sku_overrides": {
    "实际SKU": { "*产品素材图": "https://example.com/verified-square-image.jpg" }
  }
}
```

以上尺寸占位文字必须换成真实正数。价格乘数由用户明确指定，可用于已确认的换算，不自动查询汇率。申报价的精确字段名是 `*申报价格\n(店铺币种)`，JSON 中 `\n` 为换行。

## 当前样例

57 条源记录组成 1 个商品、48 个 SKU、10 张商品图。默认生成的文件已补齐申报价格、长宽高、重量及产品素材图 URL；尺寸和重量是临时默认值，后续应更新为实测值。图片尺寸未联网验证，报告仍保留图片规格核验提示。输入支持 UTF-8/BOM、引号及多行字段；其他编码会明确报错。模板校验针对本次提供的 51 列 inline-string 模板；其他平台或变更后的模板需另行适配。

