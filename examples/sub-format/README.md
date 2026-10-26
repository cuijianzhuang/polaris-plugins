# sub-format：订阅扩展示例（Python）

演示协议第 4 阶段的两个订阅扩展，只用 Python 标准库和本仓库的 [Python SDK](../../sdk/python)。

- **自定义格式**：用户访问 `/sub/<令牌>?format=csv`，得到一份 CSV 节点清单（名称、地区、协议、地址、端口）。示例故意不输出 UUID 和密码。
- **改写节点**：所有格式生成前，给节点名加国旗（按地区代码），按地区排序；设置了 `DROP_KEYWORD` 时，去掉名称含该关键字的节点。

```bash
PORT=9200 python3 server.py
# 面板：扩展 → 插件 → 安装插件 → http://127.0.0.1:9200
# 安装时确认「订阅格式 csv」和「改写订阅节点」
```

可选环境变量：`INSTALL_KEY`（要求面板带安装码）、`DROP_KEYWORD`、`STATE_FILE`（默认 `state.json`，保存面板下发的凭据）。

要点：

- 每个请求都先用 `parse_signed` 验签；`/polaris/install` 用正文里的 `plugin_secret` 验签，之后的请求用保存的密钥。
- 渲染请求里的节点**包含连接凭据**。示例不写日志、不落盘；你的插件也不要保存或转发它们。
- 改写答复先用 `check_transform` 自测：不合规的答复会被面板跳过，用户拿到的是改写前的节点。

协议细节见 [docs/subscription.md](../../docs/subscription.md)。
