# 贡献指南

当前仓库以 `develop` 为默认分支。请从最新默认分支创建 `feat/<topic>`、`fix/<topic>` 或 `docs/<topic>`，通过 PR 合入，不直接推送 `main`。

协议调整请说明触发场景、字段约束、失败与重试行为、版本兼容和宿主配套改动。示例不得包含真实 token、支付密钥或用户代理凭据。对尚未完成的功能标注计划状态。

涉及宿主 scope、数据库事务、事件持久化、管理页面或订阅链路的改动，在 Polaris 仓库提交单独 PR，并在两边互相引用。

提交前运行：

```bash
(cd sdk/node && npm test); (cd sdk/python && python3 -m unittest); (cd sdk/go && go vet ./... && go test ./...)
```

检查示例 JSON 语法：

```bash
python3 -m json.tool examples/external-notify/polaris-plugin.json
```

这只验证 JSON 语法，不代表宿主能安装示例。后续新增校验器、SDK 或服务时，应提供对应语言的构建、行为测试和实际 HTTP 验证步骤。
