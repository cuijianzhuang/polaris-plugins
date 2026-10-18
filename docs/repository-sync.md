# 个人账号与组织双向同步

[返回首页](../README.md)

保留 `cuijianzhuang` 的四个原仓库，在 `PolarisPanelOrg` 中各保留一份副本。默认分支的差异由自动任务生成同步 PR，审阅并合入后完成同步。不转移个人仓库，不直接推送默认分支，也不自动合并或发布。

## 同步范围

| 仓库 | 默认分支 | 两边可见性 |
| --- | --- | --- |
| Polaris | main | 公开 |
| polaris-license | main | 私有 |
| polaris-pro | main | 私有 |
| polaris-plugins | develop | 公开 |

脚本每次读取双方实际默认分支，表格描述初始化约定。两边必须同为公开或同为私有，否则拒绝复制。现有分支、标签和各自 Issues / PR 都继续保留；定期任务只同步默认分支的 Git 提交，不复制 Issues、PR、Secrets、Actions 配置值、其他分支或标签。

## 1. 创建组织副本

当前 Codex 集成调用创建 Fork API 返回 403，因此副本需要先由组织管理员创建。

在四个个人仓库分别选择 **Fork → Owner: PolarisPanelOrg**。取消“仅复制默认分支”，保留仓库名称。两个私有项目必须保持私有；如果组织策略或套餐不允许私有 Fork，先解决该限制，不要改为公开。Fork 是有关联的独立副本，创建后不会自动双向同步。

如果选择独立 Git 镜像副本而不是 Fork，初始化时必须从原仓库复制历史，不要创建无关的初始 README。同步器支持具有共同提交历史的独立副本，但不会覆盖无关历史。

## 2. 配置两个同步凭据

在个人仓库 `cuijianzhuang/polaris-plugins` 的 **Settings → Secrets and variables → Actions** 添加 Repository secrets：

| Secret | 资源范围 |
| --- | --- |
| `PERSONAL_REPO_SYNC_TOKEN` | 仅个人账号下这四个仓库 |
| `ORG_REPO_SYNC_TOKEN` | 仅 PolarisPanelOrg 下这四个仓库 |

可以分别使用两个 Fine-grained PAT：每个 token 选择对应 Resource owner 与四个仓库，授予 **Contents: Read and write**、**Pull requests: Read and write**，以及 **Workflows: Read and write**（同步提交可能包含工作流变更）。Metadata 读取权限自动附带。组织端如要求审批，应完成审批后再运行。

Fine-grained PAT 每个只绑定一个资源所有者，所以这里使用两个凭据，而不是假设一个 token 能跨个人和组织访问全部私有仓库。无需授予删除仓库、管理 Secrets 或转移所有权的权限。凭据只能填入 GitHub Secrets，不提交到仓库或发送到聊天。

## 3. 先检查，再启用

本配置 PR 合入 `develop` 后，在个人插件仓库 **Actions → Repository synchronization → Run workflow**：

1. 保持 `apply=false`，检查两边仓库可见性、读取 / 写入权限、默认分支和共同历史；本次不会创建分支或 PR。
2. 检查成功后，选择 `apply=true`，为有差异的方向生成同步 PR。
3. 如需定时同步，在 Actions 的 **Variables** 中设置 `ENABLE_ORG_SYNC=true`。此后每小时第 23 分钟触发；GitHub 可能延迟执行，不承诺实时同步。删除变量或改为 false 可暂停定时同步。

只有 `cuijianzhuang/polaris-plugins` 运行同步调度；组织副本的同名工作流不会重复启动同步。普通 PR 只运行测试，不读取同步凭据。尚未创建组织副本或设置凭据时，不能将这套配置描述为“已启用”。

## 工作方式与冲突

每次读取双方默认分支，先抓取完整的对应分支历史，再分别判断两个方向：

- 目标已包含来源提交，或两边文件完全一致：不创建 PR，避免仅因合并提交不同而反复同步。
- 有新增差异：推送到目标的 `sync/from-cuijianzhuang` 或 `sync/from-PolarisPanelOrg` 开发分支，为该方向创建 / 更新一个 PR。
- 两边各有修改：可产生两个方向的 PR，由维护者决定合并顺序并解决冲突；脚本不自动选择其中一边覆盖另一边。
- 来源、目标或同步分支发生历史改写：普通推送被拒绝，任务报错，不强制推送。如果手动在同步分支上解决了冲突，应先完成该 PR；确认提交已进入默认分支后，可删除已完成的同步分支，让下次任务重新创建。

同步 PR 需照常通过 CI 并由维护者合入。主分支上的原有构建 / 发布流程仍按自己的触发规则运行；自动任务本身不会合并或创建发布。同步工作流不复制 Secrets，组织仓库需要的构建凭据由组织管理员单独配置。

## 验证

使用 Python 标准库及本地 Git，无额外依赖：

```bash
python3 -m unittest discover -s tests -v
```

测试使用临时本地裸仓库，覆盖双向分叉、重复运行更新同一 PR、默认分支不变、同内容不循环、权限 / 可见性不符、无共同历史、人工修改同步分支不被强制覆盖，以及缺少凭据时在联网前失败。

真实 GitHub 端的创建 PR、组织权限和凭据仍须在上述手动 dry run / apply 中验证；本地测试通过不代表线上同步已经运行。
