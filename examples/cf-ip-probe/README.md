# cf-ip-probe：CDN 优选 IP 测速点（Go）

在**用户所在地区**的机器上运行（家里的路由器、树莓派、国内 VPS 都可以；按运营商各放一台更好），定时测 Cloudflare IP，把最优的几个上报给 Polaris 的 CDN 配置档。面板据此生成订阅里的优选 IP 节点。

为什么要放在用户那边测：面板和节点通常在海外，从那里测出来的「最快 IP」对国内用户没有意义。

测速方法参考 [CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)，常用参数也取了同样的名字；已经在用它的，可以直接让本程序上报它的 `result.csv`（见下文）。

## 一键安装（Linux）

1. 打开面板「链路与路由 → CDN 配置档」里的配置档，在「测速点」卡片底部展开「添加测速点」，填测速点名称（例如「上海电信」），点「生成安装命令」。面板会创建一个专用令牌（只能给这个配置档、以这个名称上报），并给出填好面板地址、配置档 ID、域名、令牌和名称的完整命令（只显示一次）。
2. 在测速机器上以 root 运行这条命令。在面板里移除测速点时，它的令牌一起作废。

旧版面板没有「生成安装命令」时：在「扩展 → API 令牌」创建只有「上报 CDN 优选 IP」（`cdn:ips:write`）权限的令牌，填进下面的命令，在测速机器上以 root 运行：

```sh
curl -fsSL https://github.com/cuijianzhuang/polaris-plugins/releases/latest/download/install.sh | sudo sh -s -- \
  --api https://panel.example.com/<后台路径>/api/v1 --profile <配置档 ID> --host cdn.example.com \
  --token plt_… --source 上海电信
```

脚本会识别架构（x86、ARM、MIPS 路由器、RISC-V 等），下载对应版本并校验 sha256，写入配置 `/etc/cf-ip-probe.env`（权限 600），再注册为服务：systemd、OpenWrt（procd）、Alpine（OpenRC）都支持。

| 脚本参数 | 说明 |
| --- | --- |
| `--carrier ct\|cu\|cm` | 测速点运营商，不填时面板从测速点名称识别 |
| `--args "…"` | 额外的程序参数，例如 `--args "-interval 2h -colo HKG,NRT -sl 5"` |
| `--test` | 安装后先试跑一轮（只打印结果，不上报） |
| `--version 0.2.0` | 安装指定版本，默认最新；指定版本时默认不自动更新 |
| `--auto-update on\|off` | 每天检查一次新版本，有就自动安装（沿用原来的设置），默认开启 |
| `--mirror <前缀>` | github.com 访问慢时，在每个下载地址前加上这个前缀（也可用环境变量 `GH_PROXY`） |
| `--dir` / `--no-start` | 安装目录（默认 `/usr/local/bin`，OpenWrt 为 `/usr/bin`）/ 只安装不启动 |
| `--uninstall` | 停止服务并删除程序和配置 |

再次运行脚本就是升级，没有给出的参数沿用上次的设置。不带参数在终端里运行时，会逐项询问。

**自动更新**：安装时会把同版本的安装脚本（已校验 sha256）留在程序旁边（`cf-ip-probe-install.sh`），并每天在随机时间检查一次 GitHub 上的最新版本，有新版本就下载、校验并重启服务，没有就什么都不做。systemd 用定时器 `cf-ip-probe-update.timer`，OpenWrt / Alpine 用 `/etc/crontabs/root` 里的一行。也可以手动执行 `sudo cf-ip-probe-update` 立即检查。用 `--mirror` 安装的，自动更新也走同一个加速前缀。旧版本装的测速点需要重新运行一次安装命令才会开启。

查看日志：systemd 用 `journalctl -u cf-ip-probe -f`，OpenWrt 用 `logread -f -e cf-ip-probe`，Alpine 看 `/var/log/cf-ip-probe.log`。

其他系统（Windows、macOS、FreeBSD）在 [Releases](https://github.com/cuijianzhuang/polaris-plugins/releases) 下载对应压缩包，手动运行。参数和一键安装命令里的一样，只是写成 `-api`、`-profile`、`-host`、`-token`、`-source`（单个短横线）。

## macOS

一键脚本只支持 Linux，macOS 手动安装（M 系列芯片用 `darwin-arm64`，Intel 用 `darwin-amd64`）：

```sh
mkdir -p ~/cf-ip-probe && cd ~/cf-ip-probe
curl -fL -o p.tgz https://github.com/cuijianzhuang/polaris-plugins/releases/latest/download/cf-ip-probe-darwin-arm64.tar.gz
tar xzf p.tgz && chmod +x cf-ip-probe
# 先试跑一轮，只打印不上报
./cf-ip-probe -once -dry-run -api 'https://panel.example.com/<后台路径>/api/v1' -profile <配置档 ID> \
  -host cdn.example.com -token 'plt_…' -source 上海电信
```

令牌等参数从面板「添加测速点 → 生成安装命令」生成的命令里抄。能列出 IP 和延迟后，用 launchd 开机自启（`~/Library/LaunchAgents/com.polaris.cf-ip-probe.plist`）：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.polaris.cf-ip-probe</string>
  <key>ProgramArguments</key><array>
    <string>/Users/你的用户名/cf-ip-probe/cf-ip-probe</string>
    <string>-api</string><string>https://panel.example.com/后台路径/api/v1</string>
    <string>-profile</string><string>配置档 ID</string>
    <string>-host</string><string>cdn.example.com</string>
    <string>-token</string><string>plt_…</string>
    <string>-source</string><string>上海电信</string>
    <string>-interval</string><string>2h</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/Users/你的用户名/cf-ip-probe/probe.log</string>
  <key>StandardErrorPath</key><string>/Users/你的用户名/cf-ip-probe/probe.log</string>
</dict></plist>
```

```sh
launchctl load ~/Library/LaunchAgents/com.polaris.cf-ip-probe.plist     # 启动并开机自启
tail -f ~/cf-ip-probe/probe.log                                        # 看日志
launchctl unload ~/Library/LaunchAgents/com.polaris.cf-ip-probe.plist   # 停止
```

注意：

- 测速要走直连：关掉代理软件，或在 ClashX / Surge 等里让 Cloudflare IP 段直连，否则测的是代理线路。
- Mac 合盖休眠时不会测；按流量计费的网络把 `-interval` 调大或加 `-dd`。
- 升级：重新下载压缩包覆盖 `cf-ip-probe`，再 `launchctl unload` / `load` 一次（macOS 版没有自动更新）。

## 每一轮做什么

1. **延迟**：Cloudflare 的每个 /24 网段随机取一个 IP（约 6000 个），各做 4 次 TCP 连接，算平均延迟和丢包率。面板开启了「第三方反代 IP（Shodan）」时，再加上面板给的候选（`GET {api}/cdn-profiles/{id}/ip-candidates`，最多 500 个），和官方 IP 走同样的验证和测速，只有能用且更快的才会上报。旧版面板没有这个接口时自动跳过。`-tl` / `-tll` / `-tlr` 过滤延迟和丢包。
2. **验证**：按丢包、延迟从好到差，访问 `https://<host>/cdn-cgi/trace`，能返回 Cloudflare 机房代码才算可用，同时得到机房（`-colo` 只保留指定机房）。有些网络会让任何 IP 的 TCP 都「连得上」，这一步把这类假结果去掉。
3. **下载测速**：对通过验证的前 `-dn` 个 IP 下载测速 `-dt` 秒；低于 `-sl` 的丢掉，换下一个。下载失败的 IP 照样上报（只是没有速度）；连续失败 3 次就放弃本轮下载测速，日志里会写原因。
4. **上报**：前 `-top` 个按丢包、下载速度、延迟排序，上报到 `POST {api}/cdn-profiles/{id}/preferred-ips`（带速度和机房，面板卡片里能看到）。

没有可用 IP 时不上报，面板继续用上一次的列表。

## 参数

也可以用环境变量 `POLARIS_API`、`POLARIS_TOKEN`、`POLARIS_CDN_PROFILE`、`PROBE_SOURCE`、`PROBE_CARRIER`、`PROBE_HOST`、`PROBE_URL`。

| 参数 | 默认 | 说明 | CloudflareSpeedTest |
| --- | --- | --- | --- |
| `-source` | 必填 | 测速点名称，面板按它区分测速点 | |
| `-profile` | 必填 | 配置档 ID，多个用逗号分隔 | |
| `-carrier` | 从 `-source` 识别 | 测速点运营商：`ct` 电信、`cu` 联通、`cm` 移动；面板开启「按运营商分配」时，这个运营商的用户拿到本测速点上报的 IP | |
| `-host` | `www.cloudflare.com` | 用来验证 IP 的站点；填你自己的 CDN 域名最准 | |
| `-port` | 443 | 测试端口，和配置档使用的端口一致 | `-tp` |
| `-sample` | 0 | 每轮测多少个 /24 网段，0 为全部 | `-allip` 相反 |
| `-tries` / `-workers` | 4 / 200 | 每个 IP 连接次数 / 并发数（路由器上调低） | `-t` / `-n` |
| `-tl` / `-tll` / `-tlr` | 9999 / 0 / 1 | 平均延迟上限、下限（ms）/ 丢包率上限（0–1） | 同名 |
| `-colo` | 全部 | 只保留这些机房，例如 `HKG,NRT,LAX` | `-cfcolo` |
| `-dn` / `-dt` | 5 / 5s | 下载测速的 IP 数 / 每个 IP 的时长 | 同名 |
| `-sl` | 0 | 下载速度下限（MB/s）；设置后只上报测过速的 IP | 同名 |
| `-dd` | 关 | 不做下载测速，只按延迟排序 | 同名 |
| `-url` | `https://speed.cloudflare.com/__down?bytes=50000000` | 下载测速用的文件；最好换成你自己 Cloudflare 站点上的大文件 | 同名 |
| `-f` / `-ranges-url` | 内置列表 | 从文件 / URL 读取 IP 段（每行一个 CIDR 或 IP） | `-f` |
| `-csv` | | 不自己测，上报 CloudflareSpeedTest 的 `result.csv` | |
| `-top` | 10 | 上报个数（最多 200） | |
| `-interval` | 1h | 每轮间隔，最少 10 分钟 | |
| `-verify` | true | 关掉后不做 HTTPS 验证（不推荐） | |
| `-debug` | 关 | 日志里写出每个 IP 没通过 HTTPS 验证或下载测速的原因 | 同名 |
| `-candidates` | 开 | 每轮从面板取额外候选 IP（面板开启「第三方反代 IP（Shodan）」时）一起测；关掉只测 Cloudflare 官方 IP 段 | |
| `-once` / `-dry-run` / `-version` | | 只跑一轮 / 只打印不上报 / 显示版本 | |

**流量**：默认每轮下载测速约 5 个 IP × 5 秒，以 10 MB/s 计约 250 MB；每小时一轮一天约 6 GB。按流量计费的线路请加大 `-interval`、减小 `-dn` / `-dt`，或用 `-dd` 关掉下载测速。

## 排查

每轮结束有一行汇总，例如 `HTTPS check: 12 of 200 passed, most common failure: …; download test: 0 of 3 succeeded, most common failure: …`：

- **HTTPS 验证大多失败**：超时的 IP 会自动重试一次。`-host` 要填已经开了 Cloudflare 代理（橙色云朵）的域名；超时多说明这条线路到 Cloudflare 很差，可以用 `-tl` 只留低延迟 IP，或换端口（`-port 2053` / `8443` 等，配置档也要用同一个端口）。
- **下载测速失败**：默认测速文件在 speed.cloudflare.com，部分国内网络访问不了。换成你自己 Cloudflare 站点上的大文件（`-url https://cdn.example.com/100mb.bin`），或用 `-dd` 关掉下载测速。
- 加 `-debug` 能看到每个 IP 的失败原因。

修改参数：编辑 `/etc/cf-ip-probe.env` 里的 `PROBE_ARGS`（例如 `PROBE_ARGS='-dd -debug'`）后重启服务，或带 `--args "…"` 重新运行安装脚本。

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

`scripts/build.sh <版本> <目录>` 交叉编译全部平台并打包。

**自动发布**：每次有 cf-ip-probe 的改动合并进 main，GitHub Actions 会在上一个版本号的基础上把补丁位加 1（例如 0.2.0 → 0.2.1），打标签 `cf-ip-probe-vX.Y.Z`，编译全部平台并发布 Release（含安装脚本和 sha256sums.txt）。要升次版本号或主版本号，在 Actions 里手动运行 cf-ip-probe 流水线并选择 minor / major。

面板侧：配置档详情的「测速点」卡片显示每个测速点的上报时间、最优 IP、机房和速度；超过设定时长没有上报会发通知。说明见 Polaris 用户手册「CDN 优选 IP」一节。

### 测速端口与上报

`-port` 控制 TCP 延迟、所选协议验证和下载测试使用的端口，取值 1–65535，HTTPS 默认 443，HTTP 默认 80。上报包含实际 `scheme` 和 `port`，面板可分别维护 443、8443 等端口的结果。使用非默认端口时，例如 `-port 8443`，请确认目标 CDN 支持该 HTTPS 端口。

导入 CSV 时，`-port` 应与生成文件时实际测试的端口一致；CSV 本身无法可靠推断端口。每个测速进程测一个端口，需要覆盖多个端口时使用不同名称的独立进程或机器。新面板生成的安装命令通过 `--args '-scheme https -port 8443'` 设置协议与端口；同名测速点重新配对会撤销原令牌。

旧 HTTPS 面板会忽略新增的上报字段。启用新面板生成的 HTTP / 多实例命令时，应先发布本版程序与安装脚本，再更新面板；旧安装脚本无法识别 `--instance`。


### HTTP 和多实例

`-scheme http` 测量 HTTP（默认端口 80），`-scheme https` 测量 HTTPS（默认端口 443）。`-port` 指定实际测量端口；验证请求、下载和上报保持同一协议和端口。显式 `-url` / `PROBE_URL` 必须使用所选协议；HTTP 端口被强制重定向到 HTTPS 时会拒绝把它当作 HTTP 验证成功。CSV 模式同样必须声明原测量使用的协议和端口，不会自动推断。

安装脚本新增 `--instance NAME`，为不同测速点建立独立二进制、配置、服务、日志和自动更新任务。名称使用最多 40 个字母、数字、下划线或连字符；`update` 和 `-update` 后缀为自动更新保留，不能用作实例名。省略时保留旧的默认单实例行为。示例：

```sh
# 将示例令牌和配置档替换为面板配对生成的值；两个测速点使用独立令牌。
sh install.sh --instance http80 --source point-http80 --api https://panel.example.com/api/v1 --profile HTTP_PROFILE --token HTTP_TOKEN --host cdn.example.com --args '-scheme http -port 80'
sh install.sh --instance https8443 --source point-https8443 --api https://panel.example.com/api/v1 --profile HTTPS_PROFILE --token HTTPS_TOKEN --host cdn.example.com --args '-scheme https -port 8443'
sh install.sh --instance http80 --uninstall
```

第三条只删除 `http80`，不会删除默认实例或 `https8443`。面板配对生成稳定实例名，同名测速点重新配对后在原实例更新配置；不同端口需使用不同测速点名称。迁移旧默认实例时，确认新实例上报后，可另行卸载旧默认实例，避免其继续使用已撤销的旧令牌。
