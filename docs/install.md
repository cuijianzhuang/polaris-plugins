# 安装握手

[返回首页](../README.md)

管理员在「扩展 → 插件 → 安装插件」填入插件地址（和可选的安装码）：

1. 面板 `GET {base}/polaris-plugin.json`，校验清单，展示申请的 scope / 事件让管理员确认（需要 sudo）。
2. 面板为该插件创建**专用 API 令牌**（权限 = 清单 `scopes`），生成**插件密钥**（`plsec_…`），如声明了 `events` 再创建一个**Webhook**（目标 = `{base}{webhookPath}`）及其密钥。
3. 面板 `POST {base}/polaris/install`：

```json
{
  "panel_url": "https://panel.example.com",
  "api_base": "https://panel.example.com/<后台路径>/api/v1",
  "token": "plt_…",
  "plugin_secret": "plsec_…",
  "webhook_secret": "…",
  "panel_version": "0.1.50",
  "settings": { "greeting": "你好" }
}
```

头：`X-Polaris-Signature`（用 `plugin_secret` 签名）、可选 `X-Polaris-Install-Key`。**2xx = 握手成功**，插件状态变为「在线」。

4. 其他情况（插件不支持自动安装、没应答、非 2xx）：安装仍然完成，状态为「待配置」，面板把上述凭据**只显示一次**让管理员手动粘贴到插件里；插件开始应答 `/polaris/health` 后自动变为在线。

## 插件需要实现的端点（相对 base）

| 端点 | 何时调用 | 要求 |
| --- | --- | --- |
| `GET /polaris-plugin.json` | 预览、安装、刷新 | 返回[清单](manifest.md) |
| `POST /polaris/install` | 安装 | 保存凭据，2xx。无需自动安装可不实现 |
| `POST /polaris/config` | 管理员保存设置 | 体 `{"settings":{…}}`，2xx |
| `GET /polaris/health` | 每分钟左右 | 2xx = 在线；状态变化时面板发 `system.plugin_offline/online` 事件 |
| `POST /polaris/uninstall` | 卸载 | 尽力通知，失败不阻止卸载；插件应清除凭据 |
| `POST {webhookPath}` | 事件 | 见[事件](events.md) |

所有面板→插件请求超时 6 秒；跨主机重定向不会被跟随。

## 卸载 / 停用

卸载：吊销令牌、删除关联 Webhook、通知插件。停用：不再投递事件，页面和入口隐藏，令牌保持但可由管理员吊销。

## 备份与恢复

面板备份含插件元数据和加密后的密钥；恢复后请检查插件是否仍在线、必要时「刷新」。
