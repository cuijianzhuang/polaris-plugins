# 协议版本与设计差异

[返回首页](../README.md) · [设计文档](design.md)

`docs/design.md` 是立项时的完整设计（包含尚未实现的加固项）；本目录下的其他文档描述**面板当前实际行为**（Polaris 插件管理器，面板版本 ≥ 含 `internal/plugin` 的发行版；第 4 阶段的各项能力标注了最低面板版本）。两者差异：

| 设计文档 | 当前实现 |
| --- | --- |
| `manifestVersion`、`requires.hostApi`、`runtime` | 暂未使用（被忽略）；用 `minPanel` 声明最低面板版本。协议出现不兼容变更时再引入 `manifestVersion` |
| 安装 ID / nonce 双向确认配对 | 单次 `POST /polaris/install`，可选 `X-Polaris-Install-Key` 安装码；插件 ID 即安装 ID |
| 身份令牌含 `role`、`install_id` | 使用 `scope`（`admin` / `user`）；`aud` 为插件 ID |
| 一次性引导码 | 目前直接用 `?polaris_token=` 短期 JWT（5 / 10 分钟） |
| outbox 事务投递、租约 | 事件落库后异步投递，进程崩溃时可能丢失尚未持久化的事件；以 `id` 去重 |
| `subscription.formats`、`/polaris/render` | 已实现（面板 ≥ 0.1.148）。缓存 60 秒；失败返回 503，不影响其他格式 |
| `subscription.transform`、`/polaris/transform` | 已实现（面板 ≥ 0.1.149）。插件看不到凭据；失败时跳过该插件 |
| `/nodes/:id/metrics`、`nodes:metrics:write` | 已实现（面板 ≥ 0.1.149）。来源记为插件 ID，与 Agent 数据分开显示 |
| 设计中的渲染缓存「账号禁用后命中前重新校验」 | 面板每次请求先检查账号状态再查缓存，效果相同 |
| 启用 / 停用 / 升级独立接口 | 通过「停用」开关与「刷新清单」实现 |

兼容性承诺（`0.x` 期间）：新增字段 / 事件 / scope 属于兼容变更，请忽略未知字段；删除或改变含义会在本仓库 CHANGELOG 和宿主发行说明中标出。
