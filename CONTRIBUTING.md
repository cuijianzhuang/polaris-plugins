# 贡献指南

当前仓库以 `main` 为默认分支。请从最新的 `main` 创建 `feat/<topic>`、`fix/<topic>` 或 `docs/<topic>`，通过 PR 合入，不直接推送 `main`。

协议调整请说明触发场景、字段约束、失败与重试行为、版本兼容和宿主配套改动。示例不得包含真实 token、支付密钥或用户代理凭据。对尚未完成的功能标注计划状态。

涉及宿主 scope、数据库事务、事件持久化、管理页面或订阅链路的改动，在 Polaris 仓库提交单独 PR，并在两边互相引用。

提交前运行：

```bash
(cd sdk/node && npm test); (cd sdk/python && python3 -m unittest); (cd sdk/go && go vet ./... && go test ./...)
```

按 Schema 检查所有示例清单，并编译示例：

```bash
npm i --no-save ajv@8 && node scripts/check-manifests.js
(cd examples/tcp-ping && go vet ./...); python3 -m py_compile examples/sub-format/server.py
```

这只验证清单和语法，不代表宿主能安装示例；示例的 README 写明了在真实面板上的验证方式。后续新增校验器、SDK 或服务时，应提供对应语言的构建、行为测试和实际 HTTP 验证步骤。
