# 调用面板 API

[返回首页](../README.md)

- `api_base`（安装时给出）= `https://<面板>/<后台路径>/api/v1`。
- 认证：`Authorization: Bearer plt_…`。错误：401 令牌无效 / 过期 / 吊销；403 缺少 scope（响应里有 `scope` 字段）或接口不对令牌开放。
- 自描述：`GET {api_base}/openapi.json`（任意有效令牌可读）。
- `GET {api_base}/tokens/self` 返回当前令牌的 scope 和有效期。

## 常用调用

```bash
# 续期 30 天（订单号做幂等键）
curl -X POST "$API/users/$UID/renew" -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: order-123" -H 'Content-Type: application/json' \
  -d '{"days":30,"resetTraffic":false}'

# 增加 50 GB
curl -X POST "$API/users/$UID/add-traffic" -H "Authorization: Bearer $TOKEN" \
  -H "Idempotency-Key: order-123-traffic" -H 'Content-Type: application/json' -d '{"gb":50}'
```

- `renew`：`days` 1–3650；已过期的账号从现在起算；「首次使用后生效」的账号累加有效天数；不限期账号保持不限期。
- `add-traffic`：`gb` > 0；不限流量的账号不改动。
- 持有 `users:read`/`users:write` 的令牌拿到完整用户对象；只持有窄权限（`users:renew` / `users:traffic:add`）时只返回权益摘要（`id`、`username`、`status`、`trafficLimit`、`usedUp`、`usedDown`、`expireAt`、`expireAfterDays`）。
- 完整 scope 清单见 [scopes.md](scopes.md)。
