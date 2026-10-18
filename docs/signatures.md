# 签名校验

[返回首页](../README.md)

面板发给插件的**每一个请求**（事件、安装、配置、卸载、健康检查）都带：

```text
X-Polaris-Signature: t=<Unix 秒>,v1=<hex HMAC-SHA256(secret, "<t>.<原始请求体>")>
```

| 请求 | 用哪个密钥 |
| --- | --- |
| `POST /polaris/install` | 随请求体一起交付的 `plugin_secret`（首次配对，见[安装握手](install.md)） |
| `/polaris/config`、`/polaris/uninstall`、`/polaris/health` | `plugin_secret` |
| 事件（`webhookPath`） | `webhook_secret` |

## 校验步骤

1. 用**原始字节**，不要把解析后的 JSON 重新序列化。
2. 用正则解析 `t` 和 `v1`；格式不对 → 401。
3. `|now - t| ≤ 300` 秒，防重放。
4. 自己算一遍 HMAC，**常量时间**比较。
5. 按事件 `id` 去重。

各语言的实现见 [`sdk/`](../sdk)；三个 SDK 共用 [`testdata/vectors.json`](../testdata/vectors.json) 里的向量，你自己的实现也可以拿它验证：

```text
secret     = plsec_test_secret
timestamp  = 1700000000
body       = {"id":"evt_1","type":"user.created","created_at":"2026-10-18T00:00:00Z","data":{"user":{"id":"u1","username":"alice"}}}
signature  = ad7d92f6db748c0f8bebab25712c640894f62af7d5e503f033dc4fd492294715
```

## 面板→插件方向的安全提示

- **首次安装请求之前双方没有共享密钥**，请求里的签名只能证明「载荷自洽」，不能证明发送者是你的面板。生产环境请在插件里设置**安装码**（例如环境变量 `INSTALL_KEY`），管理员安装时填入，面板以 `X-Polaris-Install-Key` 头带上；插件已配对后拒绝任何重新安装。
- 公网插件务必使用 HTTPS。
