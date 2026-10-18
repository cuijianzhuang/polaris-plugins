# 快速开始

[返回首页](../README.md)

十分钟写出第一个插件。插件就是一个**独立的 HTTP 服务**，语言不限；面板和插件之间只有四种往来：

| 方向 | 内容 | 认证 |
| --- | --- | --- |
| 插件 → 面板 | 调用 REST API（查用户、续期、加流量） | `Authorization: Bearer plt_…`，按 [scope](scopes.md) 授权 |
| 面板 → 插件 | 推送[事件](events.md)、[安装握手](install.md)、配置、健康检查 | `X-Polaris-Signature` HMAC 签名，见[签名校验](signatures.md) |
| 管理员 → 插件页面 | 面板在 iframe 里嵌入插件的后台页面 | URL 带 `?polaris_token=`，见[页面与身份](embedding.md) |
| 用户 → 插件页面 | 用户中心里的「购买 / 续期」入口 | 同上，`scope=user` |

## 1. 提供清单

在 `GET {base}/polaris-plugin.json` 返回 [清单](manifest.md)：

```json
{
  "id": "hello", "name": "Hello", "version": "1.0.0",
  "scopes": ["users:read", "users:renew"],
  "events": ["user.created", "user.expired"],
  "pages":  [{ "id": "events", "title": "事件", "path": "/admin/events" }],
  "portal": [{ "title": "续期 30 天", "path": "/shop" }],
  "settings": [{ "key": "greeting", "label": "问候语", "type": "string", "default": "你好" }]
}
```

## 2. 实现安装握手

管理员在面板「扩展 → 插件」填入插件地址后，面板拉取清单，请管理员确认权限，然后 `POST {base}/polaris/install`，把**令牌、插件密钥、Webhook 密钥**交给插件。插件保存它们即可。完整字段见[安装握手](install.md)。

## 3. 验签并处理事件

```js
const { verifySignature } = require('@polaris/plugin-sdk')
// raw 必须是未经解析的原始请求体
if (!verifySignature(req.headers['x-polaris-signature'], state.webhook_secret, raw)) return res.writeHead(401).end()
```

事件是**至少一次**投递：按 `id` 去重，2xx 尽快应答，否则面板会按 1 分钟 / 5 分钟 / 30 分钟 / 2 小时 / 12 小时重试。

## 4. 调用面板

```js
const panel = new PanelClient(state.api_base, state.token)
await panel.renew(userId, 30, { idempotencyKey: `order-${orderId}` })
```

支付回调可能重复到达，**务必**为每个订单传固定的 `Idempotency-Key`（24 小时内重复请求只生效一次，返回同一结果）。

## 5. 本地跑起来

[`examples/hello`](../examples/hello) 是一个零依赖、可运行的完整示例：

```bash
cd examples/hello && PORT=9100 node server.js
```

然后在面板「扩展 → 插件 → 安装插件」填 `http://127.0.0.1:9100`。内网和回环地址允许安装，但只有 sudo 管理员能装。

## 限制

- 社区版同时最多安装 **3** 个插件；激活专业版授权后按授权上限（0 = 不限）。
- 清单最大 64 KB；面板对插件的请求超时 6 秒，不跟随跨主机重定向。
- 插件拿不到面板的数据库、管理员 JWT 或主控密钥，只能使用被授予的 scope。
