# cf-ip-probe：CDN 优选 IP 测速点（Go）

在**用户所在地区**的机器上运行（家里的路由器、树莓派、国内 VPS 都可以；按运营商各放一台更好），定时测 Cloudflare IP，把最优的几个上报给 Polaris 的 CDN 配置档。面板据此生成订阅里的优选 IP 节点。

为什么要放在用户那边测：面板和节点通常在海外，从那里测出来的「最快 IP」对国内用户没有意义。

## 每一轮做什么

1. 从 Cloudflare 的 IPv4 段里按大小抽样（默认 600 个 IP，每段至少 1 个）。
2. 对每个 IP 做 4 次 TCP 连接（默认 443 端口），算平均延迟和丢包率，按丢包率、再按延迟排序。
3. 对排名靠前的 IP 访问 `https://<host>/cdn-cgi/trace`：能返回 Cloudflare 机房代码（`colo=`）才算可用。有些网络会让任何 IP 的 TCP 都「连得上」，这一步把这类假结果去掉。
4. 把前 10 个上报给面板：`POST {api}/cdn-profiles/{id}/preferred-ips`。

没有可用 IP 时不上报，面板继续用上一次的列表。

## 使用

1. 面板「扩展 → API 令牌」创建只有「上报 CDN 优选 IP」（`cdn:ips:write`）权限的令牌。
2. 在「链路与路由 → CDN 配置档」找到配置档 ID（详情卡片里，或接口 `GET /cdn-profiles`）。
3. 在测速机器上：

```bash
go build -o cf-ip-probe .
./cf-ip-probe -source 上海电信 -dry-run -once          # 先试一轮，只打印结果
./cf-ip-probe -api https://panel.example.com/<后台路径>/api/v1 \
  -token plt_… -profile <配置档 ID> -source 上海电信 -host cdn.example.com
```

也可以用环境变量 `POLARIS_API`、`POLARIS_TOKEN`、`POLARIS_CDN_PROFILE`、`PROBE_SOURCE`、`PROBE_HOST`。

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-source` | 必填 | 测速点名称，面板按它区分测速点 |
| `-profile` | 必填 | 配置档 ID，多个用逗号分隔 |
| `-host` | `www.cloudflare.com` | 用来验证 IP 的站点；填你自己的 CDN 域名最准 |
| `-port` | 443 | 测试端口，和配置档使用的端口一致 |
| `-sample` / `-tries` / `-top` | 600 / 4 / 10 | 每轮抽样数 / 每个 IP 连接次数 / 上报个数 |
| `-interval` | 1h | 每轮间隔，最少 10 分钟 |
| `-ranges-url` | 内置列表 | 例如 `https://www.cloudflare.com/ips-v4` |
| `-verify` | true | 关掉后不做 HTTPS 验证（不推荐） |

用 systemd 常驻：

```ini
[Service]
Environment=POLARIS_API=https://panel.example.com/<后台路径>/api/v1 POLARIS_TOKEN=plt_… POLARIS_CDN_PROFILE=<ID> PROBE_SOURCE=上海电信 PROBE_HOST=cdn.example.com
ExecStart=/usr/local/bin/cf-ip-probe
Restart=always
```

面板侧：配置档详情的「测速点」卡片显示每个测速点的上报时间和最优 IP；超过设定时长没有上报会发通知。说明见 Polaris 用户手册「CDN 优选 IP」一节。
