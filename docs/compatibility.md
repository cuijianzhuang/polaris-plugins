# 协议版本与设计差异

[返回首页](../README.md) · [设计文档](design.md)

`docs/design.md` 是立项时的完整设计（包含尚未实现的加固项）；本目录下的其他文档描述**面板当前实际行为**（Polaris 插件管理器，面板版本 ≥ 含 `internal/plugin` 的发行版）。两者差异：

| 设计文档 | 当前实现 |
| --- | --- |
| `manifestVersion`、`requires.hostApi`、`runtime` | 暂未使用（被忽略）；用 `minPanel` 声明最低面板版本。协议出现不兼容变更时再引入 `manifestVersion` |
| 安装 ID / nonce 双向确认配对 | 单次 `POST /polaris/install`，可选 `X-Polaris-Install-Key` 安装码；插件 ID 即安装 ID |
| 身份令牌含 `role`、`install_id` | 使用 `scope`（`admin` / `user`）；`aud` 为插件 ID |
| 一次性引导码 | 目前直接用 `?polaris_token=` 短期 JWT（5 / 10 分钟） |
| outbox 事务投递、租约 | 事件落库后异步投递，进程崩溃时可能丢失尚未持久化的事件；以 `id` 去重 |
| `subscription.*`、`/polaris/render`、`/nodes/:id/metrics`、`nodes:metrics:write` | 计划中（下一阶段），清单里写了也会被忽略 |
| 启用 / 停用 / 升级独立接口 | 通过「停用」开关与「刷新清单」实现 |

兼容性承诺（`0.x` 期间）：新增字段 / 事件 / scope 属于兼容变更，请忽略未知字段；删除或改变含义会在本仓库 CHANGELOG 和宿主发行说明中标出。
