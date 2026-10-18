# 权限范围（scope）

[返回首页](../README.md)

插件令牌 `plt_…` 只能访问「路由 → scope」白名单里的接口，**未登记的路由一律 403**（含所有管理类接口：令牌、插件、设置写入等）。令牌不能调用需要 sudo 的操作。

| scope | 允许的接口 |
| --- | --- |
| `users:read` | `GET /users`（返回 `{items, …}`）、`/users/export`、`/users/:id`、`/users/:id/devices`、`/users/:id/node-quotas`、`GET /groups` |
| `users:write` | 创建 / 修改 / 删除用户，批量，重置流量 / 订阅令牌，设备与节点配额 |
| `users:renew` | `POST /users/:id/renew {days, resetTraffic}` |
| `users:traffic:add` | `POST /users/:id/add-traffic {gb}` |
| `subs:read` | `GET /users/:id/links`、`/users/:id/sub-check`（含代理链接和凭据，**敏感**） |
| `nodes:read` | `GET /nodes`、`/nodes/:id`、`/inbounds` |
| `traffic:read` | `GET /stats/overview`、`/stats/traffic`、`/stats/nodes`、`/stats/tags` |
| `settings:read` | `GET /settings` |

始终可用（任意有效令牌）：`GET /tokens/self`、`GET /openapi.json`；`GET /plugin/config` 只对插件令牌有效。

**最小权限**：支付插件只申请 `users:renew` + `users:traffic:add`——这两个接口只返回权益摘要（到期、流量），不返回订阅令牌和代理密码；通知类插件通常只需要 `events` 不需要任何 API scope。

## 幂等

写接口支持 `Idempotency-Key` 请求头：

- 24 小时内同令牌、同路由、同 key 重复请求只生效一次，返回首次结果。
- `renew` / `add-traffic` 的去重记录、变更与审计在同一事务里提交。
- 24 小时后同 key 会重新执行——商店必须自己保存订单账本，不要只依赖缓存。
- 网络超时不能断言订单未执行：用同一个 key 重试即可。

## 审计

插件的所有写操作在审计日志里记为 `plugin:<id>`。
