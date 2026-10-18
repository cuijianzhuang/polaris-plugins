# Polaris Plugins

[Polaris](https://github.com/cuijianzhuang/Polaris) 的插件开发框架：协议文档、三种语言的 SDK、JSON Schema 和可运行示例。插件是**独立部署的服务**（任意语言），通过受限 REST API 和签名 Webhook 与面板对接——支付 / 商城、通知渠道、用户同步 / 单点登录等都可以做成插件。

> 面板侧的插件管理器在 Polaris 的「扩展 → 插件」。社区版最多同时安装 3 个插件，激活专业版授权后按授权上限（0 = 不限）。

## 快速开始

```bash
cd examples/hello && PORT=9100 node server.js   # 一个零依赖的完整参考插件
# 面板：扩展 → 插件 → 安装插件 → http://127.0.0.1:9100
```

详见 **[快速开始](docs/getting-started.md)**。

## 文档

| | |
| --- | --- |
| [快速开始](docs/getting-started.md) | 插件与面板的四种往来，十分钟写出第一个插件 |
| [清单 polaris-plugin.json](docs/manifest.md) | 字段、限制、配置项类型；[JSON Schema](schema/polaris-plugin.schema.json) |
| [权限范围 scope](docs/scopes.md) | 令牌能访问哪些接口、幂等键、审计 |
| [事件](docs/events.md) | 事件列表、载荷、投递与重试语义 |
| [签名校验](docs/signatures.md) | `X-Polaris-Signature`，含测试向量 |
| [安装握手](docs/install.md) | 面板如何把凭据交给插件，插件需要实现的端点 |
| [页面与用户身份](docs/embedding.md) | 后台 iframe 页面、用户中心入口、身份令牌 |
| [调用面板 API](docs/host-api.md) | 续期 / 加流量等常用调用 |
| [协议版本与设计差异](docs/compatibility.md) | 当前实现与 [设计文档](docs/design.md) 的差异 |

## SDK

零依赖，做同样的事：验证面板的签名和身份令牌、封装常用面板 API（带幂等键）。三个 SDK 共用 [`testdata/vectors.json`](testdata/vectors.json) 测试向量。

| 语言 | 位置 | 测试 |
| --- | --- | --- |
| Node.js ≥ 18 | [`sdk/node`](sdk/node) | `cd sdk/node && npm test` |
| Python ≥ 3.8 | [`sdk/python`](sdk/python) | `cd sdk/python && python3 -m unittest` |
| Go ≥ 1.21 | [`sdk/go`](sdk/go) | `cd sdk/go && go test ./...` |

SDK 尚未发布到包仓库，请直接拷贝文件或用 git 依赖。

## 示例

- [`examples/hello`](examples/hello)：Node.js 参考插件，覆盖全流程，已用真实面板验证（安装握手、事件验签与去重、后台页面、用户续期幂等）。
- [`examples/external-notify`](examples/external-notify)：通知类插件的清单写法。
- 计划中：`webhook-discord`、`epay`（Go）、`sub-format`（Python）。

## 插件索引

[`registry/index.json`](registry/index.json) 是社区插件目录，欢迎 PR 添加你的插件。索引只是目录，不代表官方审核。

## 开发

默认分支 `main`；请从它开 `feat/*`、`fix/*`、`docs/*` 分支，通过 PR 合入。协议变更需同时更新文档、Schema、SDK 和测试，并在 [CONTRIBUTING.md](CONTRIBUTING.md) 里说明宿主配套改动。CI 会跑三个 SDK 的测试并按 Schema 校验示例清单。

仓库分工：本仓库负责协议、SDK、示例；面板侧的鉴权、事件投递、插件管理在 Polaris 仓库；授权服务在 polaris-license，插件框架不涉及授权签名私钥。
