# 插件目录

`index.json` 是插件目录，Polaris 面板的「扩展 → 插件市场」会读取它：管理员可以在面板里浏览插件、查看要申请的权限和部署方法，部署好之后一键填入地址安装。

**目录只是一份清单，面板不会下载或运行其中的任何东西。** 插件是独立部署的服务；安装时面板会重新读取插件自己的清单，管理员确认权限后才生效。目录收录不等于官方审核或背书，安装前请自行审查代码和申请的权限。

面板默认读取：

```
https://raw.githubusercontent.com/cuijianzhuang/polaris-plugins/main/registry/index.json
```

可以在插件市场里改成自己的目录地址（例如公司内部的插件清单），格式相同即可。设置了 GitHub 加速前缀时，默认地址也走加速。

## 字段

格式由 [`schema/registry.schema.json`](../schema/registry.schema.json) 定义，CI 会校验。

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `id` | 是 | 与插件清单的 `id` 相同，小写字母、数字和 `-` |
| `name` / `description` | 是 | 名称（≤ 60 字）和一两句话的介绍（≤ 300 字） |
| `author` | 是 | 作者或组织 |
| `repo` | 是 | 源码地址，必须是 `https://` |
| `homepage` | 否 | 主页或文档 |
| `language` | 否 | 实现语言 |
| `type` | 是 | `plugin`：有 `polaris-plugin.json`，在面板里安装；`tool`：独立程序，用面板创建的 API 令牌对接 |
| `kind` | 是 | `official` 官方维护、`example` 示例、`community` 社区 |
| `category` | 是 | `notify` 通知、`billing` 支付与计费、`subscription` 订阅、`monitor` 监控、`cdn` CDN、`users` 用户、`other` 其他 |
| `scopes` | 否 | 申请的 API 权限；`plugin` 类型必须与清单一致，`tool` 类型是它需要的令牌权限 |
| `events` | 否 | 订阅的事件，必须与清单一致 |
| `minPanel` | 否 | 最低面板版本，必须与清单一致 |
| `deploy.command` | 否 | 部署命令，面板里可以一键复制；用 `<…>` 标出要替换的部分 |
| `deploy.docker` | 否 | Docker 镜像 |
| `deploy.port` | 否 | 默认端口；面板会把安装地址预填为 `http://127.0.0.1:端口` |

## 提交你的插件

发 PR 在 `plugins` 里追加一项，CI 会检查格式。本仓库 `examples/` 下的插件还会检查 `scopes`、`events`、`minPanel` 是否与清单一致；其他仓库的插件请自己保证一致：面板安装时以插件清单为准，目录里写少了权限，管理员会在安装时看到更多的权限申请。
