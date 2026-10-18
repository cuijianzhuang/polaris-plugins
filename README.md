# Polaris Plugins

[Polaris](https://github.com/cuijianzhuang/Polaris) 的独立插件框架仓库，用于协议设计、公共工具、语言 SDK 和外部插件示例。社区开发者可以用任意语言部署插件，通过受限 REST API 和签名 Webhook 对接面板。

**当前阶段：外部插件安装 MVP 开发。** 本开发分支提供 Go 协议工具、配对服务和可运行的流量汇总示例，需要配套 Polaris 安装管理 PR。事件、嵌入页面和订阅扩展尚未实现；没有正式框架发行版。

## 从这里开始

- [运行示例与开发入门](docs/developing.md)
- [架构与完整协议设计](docs/design.md)
- [仓库边界与实施路线](docs/roadmap.md)
- [外部通知插件清单示例](examples/external-notify/polaris-plugin.json)
- [贡献与开发约定](CONTRIBUTING.md)
- [Polaris 宿主设计 PR #62](https://github.com/cuijianzhuang/Polaris/pull/62)

## 仓库分工

| 仓库 | 负责内容 |
| --- | --- |
| **polaris-plugins** | 插件协议、清单、公共校验与签名工具、SDK、独立服务示例及开发文档 |
| [Polaris](https://github.com/cuijianzhuang/Polaris) | scope 鉴权、订单事务、事件 outbox、Webhook 调度、插件安装与配置、一级「扩展」菜单、iframe 和订阅适配 |
| polaris-license | 产品授权服务；插件框架不获取授权签名私钥或绕过专业版授权 |

插件独立运行，拥有自己的进程和存储，通过明确授权的接口与宿主通信。第一阶段采用外部服务；WASM 和插件市场在后续单独设计。

## 协议草案

插件拟在 `GET {base}/polaris-plugin.json` 返回清单，声明版本、权限、事件、页面和设置。管理员批准权限后才生效。当前 `manifestVersion: 1` 和 `requires.hostApi: ^1.0.0` 是目标协议草案，不表示已有兼容的宿主发行版。

Webhook 拟使用 HMAC-SHA256 对 `timestamp + "." + raw-body` 签名，接收方检查时间窗并按事件 ID 去重。完整边界、配对流程和验证要求见[设计文档](docs/design.md)。

## 开发状态

初始化内容位于 `develop`，后续使用 `feat/*`、`docs/*` 等开发分支，通过 PR 审阅；不直接推送 `main`。宿主功能仍需在 Polaris 提交配套 PR，本仓库单独更新不会使面板自动支持插件安装。
