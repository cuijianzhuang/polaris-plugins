# 外部插件开发与安装 MVP

本实现位于开发分支，需要配套 Polaris 安装管理 PR。已实现外部服务清单校验、人工批准 API scope、带安装码的配对、签名连接检测和受限 API 客户端；事件投递、嵌入页面、用户入口及订阅处理仍在设计阶段。相关能力非空的清单会被拒绝。

## 运行示例

需要 Go 1.25。在仓库根目录执行：

```bash
# 自行生成安装码，仅在本机终端保存，不写进 Git。
export POLARIS_PLUGIN_PAIRING_CODE="$(openssl rand -hex 24)"
export POLARIS_PLUGIN_LISTEN=127.0.0.1:9070
export POLARIS_PLUGIN_STATE=./data/installation.json
go run ./examples/traffic-summary
```

另一个终端可访问 `http://127.0.0.1:9070/polaris-plugin.json` 确认清单。面板的一级「扩展」中填入 `http://127.0.0.1:9070/`，显式允许此插件连接本机／私网；预览名称、版本、设置和实际权限，填入 `label`，批准 `traffic:read` 与 `plugin:config:read`，输入与环境变量相同的安装码后确认安装。面板和插件分处容器时，127.0.0.1 各自指向自身，应使用可互访的私网地址。

插件需要访问面板的部署地址。Polaris 的 `POLARIS_PUBLIC_URL` 和后台路径应在插件网络中可达。公开插件服务使用经过正常证书验证的 HTTPS，示例的 HTTP 仅用于明确批准的本机／私网部署。

安装完成后可在面板检测连接。示例只提供受到安装码保护的 HTTP 查询接口，没有自动消息推送或嵌入式管理页面：

```bash
curl http://127.0.0.1:9070/summary \
  -H "Authorization: Bearer $POLARIS_PLUGIN_PAIRING_CODE"
```

响应来自本安装获批的面板流量汇总 API。未配对、令牌被吊销、插件停用或缺少 `traffic:read` 时查询失败，不会扩大权限。

## 用 Go 编写插件

`plugin.Manifest` 描述协议，`DecodeManifest` 拒绝未知字段、重复 JSON 键、过深 / 过大文档及未实现能力。当前 `requires.hostApi` 支持 `^1.0.0` 或 `1.0.0`；协议只覆盖本文的安装 MVP 子集，不表示完整设计中所有扩展点已经实现。

```go
service, err := plugin.NewService(manifest, pairingCode, statePath)
// 检查 err 后，将 service.Handler() 接入你自己的 HTTP 服务。
// service.Get(ctx, "/plugin/config", &config) 读取本安装的配置；
// 需要显式批准 plugin:config:read，这也允许读取本插件声明的密钥。
```

主控不上传或执行此程序。开发者自行部署服务、处理自身业务与存储，并在需要时实现幂等订单账本。`users:read`、`users:write`、`subs:read` 等范围可能包含用户代理凭据，安装时必须按实际需求批准；订单处理优先申请细分写权限。

## 语言无关的 HTTP 合同

- `GET {base}/polaris-plugin.json`：返回清单 JSON，最大 256 KiB。
- `POST {base}/polaris/install`：`Authorization: Bearer <安装码>`；正文为 `protocolVersion`、`panel_url`、`api_base`、`install_id`、`nonce`、`token`、`control_secret`。回复相同的 `install_id` 和 `nonce`，成功前必须持久化凭据。
- `POST {base}/polaris/health`：正文为 `install_id` 与随机 `nonce`。用 `control_secret` 验证 `X-Polaris-Signature: t=<秒>,v1=<hex HMAC-SHA256(secret, timestamp + "." + raw-body)>`，允许 5 分钟偏差；回复相同字段和 `status: "ok"`。
- 插件调用宿主：`Authorization: Bearer <token>`，API base 已包含后台路径与 `/api/v1`，不自行猜测面板路由。

清单中路径均相对于管理员批准的 BaseURL 前缀。本 MVP 没有外部页面路径，面板固定调用以上三个 endpoint；可在反代保留同一路径前缀后部署。

安装码是首次配对的独立凭据，不能用随请求下发的新密钥证明首次请求身份。插件应防止未授权覆盖配对，且只在持久化成功后应答。相同安装 ID + nonce 的重试须幂等；同 nonce 不同凭据拒绝。停用后重新启用会重新配对并替换旧令牌，需要再次输入安装码。

SDK 将凭据写入权限 0600 的本地文件，持久化配置目录应仅允许服务账号读取；文件包含真实令牌，必须纳入你自己的秘密管理策略，不提交 Git。卸载宿主安装后，SDK 保留本地配对身份；要作为全新安装重新添加，先停止示例并显式删除其本地状态文件，避免匿名替换既有安装。

## 验证

```bash
go vet ./...
go test -race ./...
```

旧的 `examples/external-notify` 是完整事件插件的设计清单，因申请 `events` 和页面能力，不能用于当前安装 MVP。可运行示例使用 `examples/traffic-summary`。
