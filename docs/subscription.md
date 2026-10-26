# 订阅扩展与节点指标

[返回首页](../README.md) · 宿主侧说明：[Polaris docs/plugins/subscription.md](https://github.com/cuijianzhuang/Polaris/blob/main/docs/plugins/subscription.md)

协议第 4 阶段给插件三种扩展能力：

| 能力 | 插件做什么 | 清单 | 面板版本 |
| --- | --- | --- | --- |
| 自定义订阅格式 | 实现 `POST /polaris/render`，按自己的格式输出订阅 | `subscription.formats` | ≥ 0.1.148 |
| 改写订阅节点 | 实现 `POST /polaris/transform`，筛选、改名、排序 | `subscription.transform: true` | ≥ 0.1.149 |
| 上报节点外部指标 | 调用 `POST {api_base}/nodes/{id}/metrics` | scope `nodes:metrics:write` | ≥ 0.1.149 |

三种能力都要在安装或升级时由管理员批准。面板发往插件的请求都带 `X-Polaris-Signature`（见[签名校验](signatures.md)），密钥是安装时下发的 `plugin_secret`。SDK 的 `parseSigned` / `parse_signed` / `ParseSigned` 一步完成验签和 JSON 解析。

## 自定义订阅格式

```json
"subscription": {
  "formats": [{ "name": "csv", "title": "CSV 节点清单", "contentType": "text/plain" }]
}
```

- `name`：`?format=` 的值，小写字母开头，可以有数字和 `-`，2–24 个字符。不能用内置格式名（`links`、`clash`、`singbox`、`surge`、`loon`、`quanx`、`html`、`auto`、`raw` 等），也不能与已安装的其他插件重名。每个插件最多 5 个格式。
- `title`：后台显示名，最长 40 个字符。
- `contentType`：响应只能用这个类型（可带 `charset`）。可选值见 SDK 常量 `SUB_CONTENT_TYPES`：`text/plain`、`text/yaml`、`application/yaml`、`application/x-yaml`、`application/json`、`application/xml`、`text/xml`。不允许 HTML 和脚本。

用户访问 `/sub/<令牌>?format=csv` 时，面板照常检查账号、算出该用户可用的节点，然后：

```
POST {base}/polaris/render
X-Polaris-Signature: t=…,v1=…
```

```json
{
  "format": "csv",
  "user": { "username": "alice", "title": "Polaris", "upload": 0, "download": 0, "total": 0, "expire": null, "updateHours": 1 },
  "nodes": [{ "Name": "东京 VLESS", "Region": "JP", "Server": "203.0.113.9", "Port": 443, "Protocol": "vless", "UUID": "…", "Transport": {}, "TLS": {} }]
}
```

- `nodes` 的字段名与 Polaris 内部的节点结构相同（大写开头），**包含 UUID、密码等连接凭据**。不要记录日志、落盘或转发。
- `nodes` 里可能有面板生成的信息节点（套餐、到期提示等），按普通节点处理即可。
- `total` 为 0 表示不限流量；`expire` 为空表示不限期。

响应要求：状态码 200，`Content-Type` 与声明一致，3 秒内返回，正文不超过 4 MiB。否则用户收到 503「插件订阅格式暂不可用」，其他格式不受影响。成功结果缓存 60 秒（按插件版本、用户、订阅令牌和节点数据区分），流量数字最多滞后 1 分钟。

## 改写订阅节点

```json
"subscription": { "transform": true }
```

每次生成订阅前（所有格式，包括内置格式），面板发送：

```
POST {base}/polaris/transform
X-Polaris-Signature: t=…,v1=…
```

```json
{
  "format": "clash",
  "user": "alice",
  "nodes": [
    { "id": "0", "name": "东京 VLESS", "region": "JP", "source": "panel", "protocol": "vless", "server": "203.0.113.9", "port": 443 },
    { "id": "1", "name": "香港 Trojan", "region": "HK", "protocol": "trojan", "server": "198.51.100.7", "port": 443 }
  ]
}
```

这里**没有**凭据。答复：

```json
{ "nodes": [{ "id": "1", "name": "🇭🇰 香港" }, { "id": "0" }] }
```

规则（SDK 的 `checkTransform` / `check_transform` / `CheckTransform` 按同样的规则检查，返回不通过的原因）：

- 答复的顺序就是新顺序；没列出的节点被去掉；`{"nodes": []}` 表示全部去掉。
- 只能引用请求里的 `id`（字符串），每个最多一次；不能新增节点，地址、端口和凭据插件改不了。
- `name` 可省略或为空白，表示不改名；改名最长 100 个字符，不能有控制字符。

失败处理：每个插件限时 2 秒，所有改写插件合计 4 秒。超时、非 200、格式不对或违反上面的规则时，面板跳过这个插件，节点保持它处理前的样子。多个改写插件按管理员在「扩展 → 插件」设置的顺序依次处理。结果缓存 60 秒。

## 上报节点外部指标

令牌需要 `nodes:metrics:write`。这个权限只能写指标，不能读节点、不能改节点。要列出节点，另外申请 `nodes:read`。

```
POST {api_base}/nodes/{id}/metrics
Authorization: Bearer plt_…
```

```json
{
  "metrics": [
    { "name": "latency.sh-ct", "label": "上海电信", "unit": "ms", "value": 38.5 },
    { "name": "speed", "unit": "Mbps", "value": 920, "at": "2026-10-26T08:00:00Z" }
  ]
}
```

| 字段 | 规则 |
| --- | --- |
| `name` | 必填。小写字母开头，可以有数字和 `_` `.` `-`，最长 40 个字符。`cpu`、`mem`、`memory`、`rx`、`tx`、`load`、`online`、`status`、`heartbeat`、`traffic`、`up`、`down` 保留给节点自身数据 |
| `label` | 可选，显示名，最长 40 个字符，不能有控制字符 |
| `unit` | 必填：`ms`、`s`、`Mbps`、`kbps`、`%`、`count`、`bool`。`bool` 的值只能是 0 或 1 |
| `value` | 必填，有限数字，绝对值不超过 10¹² |
| `at` | 可选，RFC 3339，最近 24 小时内（最多比现在晚 5 分钟）；不填为当前时间 |

- 每次 1–20 个，同一请求里名称不能重复。**有一个不合规，整批都不保存**（400）。上报前用 SDK 的 `checkMetric` / `check_metric` / `CheckMetric` 自查。
- 按「来源 + 名称」保存最新值：插件令牌的来源是插件 ID，其他令牌是 `token:<令牌名>`。每个节点最多 100 个外部指标，30 天不更新自动清理。
- 指标显示在节点详情的「外部指标」卡片，标明来源；不会影响节点的在线状态、心跳、CPU、内存或流量。
- 读取：`GET {api_base}/nodes/{id}/metrics/external`（需要 `nodes:read`）。

SDK：`client.reportMetrics(id, metrics)` / `report_metrics` / `ReportMetrics`，以及 `externalMetrics` / `external_metrics` / `ExternalMetrics`、`nodes()` / `Nodes`。

## 示例

- [`examples/sub-format`](../examples/sub-format)（Python）：CSV 格式 + 国旗改名、按地区排序、关键字过滤。
- [`examples/tcp-ping`](../examples/tcp-ping)（Go）：定时 TCP 连接各节点入站端口，上报延迟和可达性。

两个示例都已在真实面板上跑通：安装握手、`?format=csv`、Clash 订阅里的改写结果、节点外部指标的写入与读取。
