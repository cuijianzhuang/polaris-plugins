# 页面与用户身份

[返回首页](../README.md)

## 后台页面（`pages`）

面板在侧边栏「扩展」里列出插件声明的页面，并用 iframe 加载：

```text
{base}{path}?polaris_token=<JWT>&panel=<api_base>&lang=<zh|en>
```

iframe 的 sandbox 为 `allow-scripts allow-forms allow-same-origin allow-popups allow-downloads`，`referrerPolicy=no-referrer`。**强烈建议把插件部署在与面板不同的 origin。** 插件页面要设置 `Content-Security-Policy: frame-ancestors <面板 origin>`。

## 用户中心入口（`portal`）

用户在 `/u` 看到入口，点击跳转 `{base}{path}?polaris_token=<JWT>`。

## 身份令牌

HS256 JWT，用**插件密钥**（`plugin_secret`）签名：

| claim | 含义 |
| --- | --- |
| `iss` | 固定 `polaris` |
| `aud` | 插件 `id`（必须核对，防止别的插件的令牌拿来用） |
| `sub` | `scope=admin`：管理员用户名；`scope=user`：用户 ID（用它调 `/users/:id/renew`） |
| `name` | 展示名（管理员名 / 用户名） |
| `scope` | `admin`（后台页面，5 分钟有效）或 `user`（用户入口，10 分钟有效） |
| `jti` `iat` `exp` | 唯一 ID、签发时间、过期时间 |

校验：算法必须是 HS256、签名正确、`iss=polaris`、`aud` = 自己、`scope` 符合预期（**后台页面必须要求 `admin`**，否则普通用户的令牌能进后台）、未过期。SDK 的 `verifyIdentity` 都做了。

**重要**：

- 这个令牌只用来向**插件自己的服务**证明「现在是谁」，**不能**拿去调用面板 API；插件调面板始终用 `plt_` 令牌。
- 令牌在 URL 里，会进入访问日志。页面验证后建议换成插件自己的 Cookie 会话，并设置 `Referrer-Policy: no-referrer`、`Cache-Control: no-store`。
- 用户身份令牌不是付款凭证：支付要验证支付平台的签名、金额和订单状态，再调用续期接口。
- 面板能阻止新令牌签发（停用 / 卸载 / 用户被禁），但无法收回插件已建立的会话，请自己设置合理的会话时长。
