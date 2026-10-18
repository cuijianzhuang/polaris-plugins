# 清单（polaris-plugin.json）

[返回首页](../README.md) · JSON Schema：[schema/polaris-plugin.schema.json](../schema/polaris-plugin.schema.json)

插件在 `GET {base}/polaris-plugin.json` 返回。`base` 是管理员填写的地址（可带路径前缀，如 `https://x.com/epay`）；清单里所有 `path` 都相对 `base`。面板忽略未知字段，所以新插件仍可装在旧面板上。

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | ✓ | 小写字母 / 数字 / `-`，2–41 个字符，安装后不可变；同一面板只装一次 |
| `name` | ✓ | 最长 64 个字符 |
| `version` | ✓ | `1.2.3`（可带 `-beta.1`） |
| `description` / `author` | | 最长 500 / 100 |
| `homepage` | | http(s) 网址 |
| `minPanel` | | 要求的最低面板版本，如 `0.1.40`；开发版视为足够新 |
| `scopes` | | 申请的 [API 权限](scopes.md)，不能重复，必须是面板认识的 |
| `events` | | 要接收的[事件](events.md)；支持 `user.*`、`*`；留空 = 不订阅 |
| `webhookPath` | | 事件投递路径，默认 `/polaris/events` |
| `pages` | | 后台页面，最多 10 个：`id`、`title`（≤40）、`icon`、`path` |
| `portal` | | 用户中心入口，最多 5 个：`title`、`path` |
| `settings` | | 管理员要填的配置，最多 40 项 |

`path` 规则：以单个 `/` 开头，不含 `?`、`#`、空白、`\`、`.`、`..`，最长 200 字符。

## settings

面板据此生成配置表单。`type`：`string` / `text` / `secret` / `number` / `bool` / `select` / `url`。

```json
{ "key": "merchant_id", "label": "商户号", "type": "string", "required": true },
{ "key": "api_key", "label": "密钥", "type": "secret", "hint": "保存后只能覆盖，不能查看" },
{ "key": "mode", "label": "模式", "type": "select", "options": ["sandbox", "live"], "default": "sandbox" }
```

- `key`：`[a-z][a-z0-9_]{0,39}`；`select` 必须有 `options`；`secret` 不能有 `default`。
- `secret` 字段加密保存，面板读取时只回显「已设置」标记；留空保存 = 保持原值。
- 保存后面板会 `POST {base}/polaris/config` 推送最新配置，插件也可用自己的令牌 `GET {api_base}/plugin/config` 随时读取（含明文 secret，**仅限该插件自己的令牌**）。

## 升级

管理员点「刷新清单」时，面板重新拉取：新增 `scopes` 或 `events` 需要管理员重新确认，权限收窄则自动生效。
