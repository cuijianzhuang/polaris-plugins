# cf-ip-probe：CDN 优选 IP 测速点（Go）

在**用户所在地区**的机器上运行（家里的路由器、树莓派、国内 VPS 都可以；按运营商各放一台更好），定时测 Cloudflare IP，把最优的几个上报给 Polaris 的 CDN 配置档。面板据此生成订阅里的优选 IP 节点。

为什么要放在用户那边测：面板和节点通常在海外，从那里测出来的「最快 IP」对国内用户没有意义。

测速方法参考 [CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)，常用参数也取了同样的名字；已经在用它的，可以直接让本程序上报它的 `result.csv`（见下文）。

## 一键安装（Linux）

1. 面板「扩展 → API 令牌」创建只有「上报 CDN 优选 IP」（`cdn:ips:write`）权限的令牌。
2. 打开「链路与路由 → CDN 配置档」里的配置档，「测速点」卡片底部「添加测速点」有现成的安装命令（面板地址、配置档 ID、域名已填好），补上令牌和测速点名称，在测速机器上以 root 运行：

```sh
curl -fsSL https://github.com/cuijianzhuang/polaris-plugins/releases/latest/download/install.sh | sudo sh -s -- \
  --api https://panel.example.com/<后台路径>/api/v1 --profile <配置档 ID> --host cdn.example.com \
  --token plt_… --source 上海电信
```

脚本会识别架构（x86、ARM、MIPS 路由器、RISC-V 等），下载对应版本并校验 sha256，写入配置 `/etc/cf-ip-probe.env`（权限 600），再注册为服务：systemd、OpenWrt（procd）、Alpine（OpenRC）都支持。

| 脚本参数 | 说明 |
| --- | --- |
| `--args "…"` | 额外的程序参数，例如 `--args "-interval 2h -colo HKG,NRT -sl 5"` |
| `--test` | 安装后先试跑一轮（只打印结果，不上报） |
| `--version 0.2.0` | 安装指定版本，默认最新 |
| `--mirror <前缀>` | github.com 访问慢时，在每个下载地址前加上这个前缀（也可用环境变量 `GH_PROXY`） |
| `--dir` / `--no-start` | 安装目录（默认 `/usr/local/bin`，OpenWrt 为 `/usr/bin`）/ 只安装不启动 |
| `--uninstall` | 停止服务并删除程序和配置 |

再次运行脚本就是升级，没有给出的参数沿用上次的设置。不带参数在终端里运行时，会逐项询问。

查看日志：systemd 用 `journalctl -u cf-ip-probe -f`，OpenWrt 用 `logread -f -e cf-ip-probe`，Alpine 看 `/var/log/cf-ip-probe.log`。

其他系统（Windows、macOS、FreeBSD）在 [Releases](https://github.com/cuijianzhuang/polaris-plugins/releases) 下载对应压缩包，手动运行。

## 每一轮做什么

1. **延迟**：Cloudflare 的每个 /24 网段随机取一个 IP（约 6000 个），各做 4 次 TCP 连接，算平均延迟和丢包率。`-tl` / `-tll` / `-tlr` 过滤延迟和丢包。
2. **验证**：按丢包、延迟从好到差，访问 `https://<host>/cdn-cgi/trace`，能返回 Cloudflare 机房代码才算可用，同时得到机房（`-colo` 只保留指定机房）。有些网络会让任何 IP 的 TCP 都「连得上」，这一步把这类假结果去掉。
3. **下载测速**：对通过验证的前 `-dn` 个 IP 下载测速 `-dt` 秒；低于 `-sl` 的丢掉，换下一个。
4. **上报**：前 `-top` 个按丢包、下载速度、延迟排序，上报到 `POST {api}/cdn-profiles/{id}/preferred-ips`（带速度和机房，面板卡片里能看到）。

没有可用 IP 时不上报，面板继续用上一次的列表。

## 参数

也可以用环境变量 `POLARIS_API`、`POLARIS_TOKEN`、`POLARIS_CDN_PROFILE`、`PROBE_SOURCE`、`PROBE_HOST`、`PROBE_URL`。

| 参数 | 默认 | 说明 | CloudflareSpeedTest |
| --- | --- | --- | --- |
| `-source` | 必填 | 测速点名称，面板按它区分测速点 | |
| `-profile` | 必填 | 配置档 ID，多个用逗号分隔 | |
| `-host` | `www.cloudflare.com` | 用来验证 IP 的站点；填你自己的 CDN 域名最准 | |
| `-port` | 443 | 测试端口，和配置档使用的端口一致 | `-tp` |
| `-sample` | 0 | 每轮测多少个 /24 网段，0 为全部 | `-allip` 相反 |
| `-tries` / `-workers` | 4 / 200 | 每个 IP 连接次数 / 并发数（路由器上调低） | `-t` / `-n` |
| `-tl` / `-tll` / `-tlr` | 9999 / 0 / 1 | 平均延迟上限、下限（ms）/ 丢包率上限（0–1） | 同名 |
| `-colo` | 全部 | 只保留这些机房，例如 `HKG,NRT,LAX` | `-cfcolo` |
| `-dn` / `-dt` | 5 / 5s | 下载测速的 IP 数 / 每个 IP 的时长 | 同名 |
| `-sl` | 0 | 下载速度下限（MB/s）；设置后只上报测过速的 IP | 同名 |
| `-dd` | 关 | 不做下载测速，只按延迟排序 | 同名 |
| `-url` | `https://speed.cloudflare.com/__down?bytes=200000000` | 下载测速用的文件；最好换成你自己 Cloudflare 站点上的大文件 | 同名 |
| `-f` / `-ranges-url` | 内置列表 | 从文件 / URL 读取 IP 段（每行一个 CIDR 或 IP） | `-f` |
| `-csv` | | 不自己测，上报 CloudflareSpeedTest 的 `result.csv` | |
| `-top` | 10 | 上报个数（最多 200） | |
| `-interval` | 1h | 每轮间隔，最少 10 分钟 | |
| `-verify` | true | 关掉后不做 HTTPS 验证（不推荐） | |
| `-once` / `-dry-run` / `-version` | | 只跑一轮 / 只打印不上报 / 显示版本 | |

**流量**：默认每轮下载测速约 5 个 IP × 5 秒，以 10 MB/s 计约 250 MB；每小时一轮一天约 6 GB。按流量计费的线路请加大 `-interval`、减小 `-dn` / `-dt`，或用 `-dd` 关掉下载测速。

## 搭配 CloudflareSpeedTest 使用

已经在路由器上定时跑 CloudflareSpeedTest 的，不用换工具，让本程序读它的结果文件上报：

```sh
cf-ip-probe -csv /root/cfst/result.csv -source 上海电信 -api … -token … -profile …
```

或者安装时 `--args "-csv /root/cfst/result.csv"`。每轮读一次文件，文件没有更新就不上报（避免把旧结果当成新的）。`-tl`、`-tlr`、`-sl`、`-colo` 同样适用。

## 自己编译

```sh
go build -o cf-ip-probe .
./cf-ip-probe -source 上海电信 -dry-run -once          # 先试一轮，只打印结果
```

`scripts/build.sh <版本> <目录>` 交叉编译全部平台并打包。推送标签 `cf-ip-probe-vX.Y.Z` 后，GitHub Actions 会自动编译并发布 Release（含安装脚本和 sha256sums.txt）。

面板侧：配置档详情的「测速点」卡片显示每个测速点的上报时间、最优 IP、机房和速度；超过设定时长没有上报会发通知。说明见 Polaris 用户手册「CDN 优选 IP」一节。
