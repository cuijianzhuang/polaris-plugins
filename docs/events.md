# 事件

[返回首页](../README.md)

面板把事件作为 JSON `POST` 到插件的 `webhookPath`（默认 `/polaris/events`）：

```json
{
  "id": "evt_01h…",
  "type": "user.expired",
  "created_at": "2026-10-18T00:00:00Z",
  "data": { "user_id": "…", "user": { "id": "…", "username": "alice", "status": "expired" } }
}
```

请求头：`X-Polaris-Signature`（见[签名校验](signatures.md)）、`X-Polaris-Event`（事件类型）、`X-Polaris-Delivery`（投递 ID，重试不变）。

## 投递语义

- **至少一次**：用 `id` 去重（同一事件重试时 `id` 不变）。
- 2xx = 成功；网络错误、非 2xx 都会重试：1 分钟、5 分钟、30 分钟、2 小时、12 小时，共最多 6 次，之后标记失败（可在面板里重新投递）。
- 请求超时 10 秒。尽快应答，耗时的工作放到后台。
- 投递记录在面板保留 7 天。

## data 约定

- 用户相关事件带 `user_id` 和 `user`（`id`、`username`、`status`、`expire_at`、`traffic_limit`、`used_up`、`used_down`、`telegram_id`、`remark`）；**不含**订阅令牌、UUID、密码。
- 节点相关事件带 `node_id` 和 `node{id,name}`。
- 通知类事件带 `message`（中文文案，仅供展示，不要解析）。
- `user.deleted` 的 `user` 是删除前的快照。
- 不同事件还会附带额外字段（如 `user.traffic_added.detail`、`user.bulk.action/user_ids`、`system.plugin_*.plugin`）。**依赖具体字段前请用「Webhook → 测试」或本仓库的 hello 示例抓一份真实载荷**；新增字段属于兼容变更，请忽略未知字段。

## 事件类型

| 类别 | 类型 |
| --- | --- |
| 用户生命周期 | `user.created` `user.updated` `user.deleted` `user.renewed` `user.traffic_added` `user.traffic_reset` `user.token_reset` `user.bulk` |
| 用户状态 | `user.expired` `user.traffic_exceeded` `user.reactivated` `user.traffic_warn` `user.expire_warn` `user.ip_limit` `user.traffic_spike` |
| 节点 | `node.offline` `node.online` `node.apply_failed` `node.traffic_alert` `node.renewal_due` |
| 入站 / 路由 | `inbound.down` `inbound.up` `routing.health` |
| 管理员 | `admin.login_banned` `admin.login_new_ip` `admin.recovery_code_used` |
| 系统 | `system.backup_failed` `system.cert_failed` `system.license` `system.panel_update` `system.plugin_offline` `system.plugin_online` |

订阅时可用通配：`user.*`、`*`。清单里的 `events` 必须是上表中的类型或通配。
