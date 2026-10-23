# 安装指南

Porch 提供**一条命令的在线安装**，也保留了手工安装 / 离线安装的方式。
支持范围见 [PLATFORMS.md](PLATFORMS.md)（19 个 tar.gz + 2 个 Windows zip + OpenWrt 两套包：
12 个 `.ipk`（≤24.10 的 opkg）与 12 个 `.apk`（≥25.12 的 apk））。

---

## 1. 一条命令装好

### Linux / OpenWrt / macOS / FreeBSD

```sh
curl -fsSL https://raw.githubusercontent.com/likz0421/porch/main/install.sh | sh
```

没有 curl 的机器（常见于精简版 OpenWrt）：

```sh
wget -qO- https://raw.githubusercontent.com/likz0421/porch/main/install.sh | sh
```

### Windows（PowerShell 5.1+）

```powershell
irm https://raw.githubusercontent.com/likz0421/porch/main/install.ps1 | iex
```

> 国内网络如果拉不到脚本本身，把上面的 URL 换成镜像前缀：
> `https://ghfast.top/https://raw.githubusercontent.com/likz0421/porch/main/install.sh`

### 装完是什么样

```
==> 环境检测
  系统: Linux (openwrt 23.05.3)  架构: mipsel → mipsle_hardfloat
  ...
==> 下载
  下载 porch_1.1.2_linux_mipsle_hardfloat.tar.gz  ← github.com（第 1 次）
  ✓ 下载完成 2.9M
  ✓ 校验和一致 3f9c1a2b…
==> 解包与自检
  ✓ 自检通过: porch 1.1.2 (release) linux/mipsle
==> 安装
  ✓ 已安装: 1.1.2  →  /usr/bin/porch
  ✓ procd 服务已启用: /etc/init.d/porch
```

---

## 2. 安装脚本具体做了什么

按顺序执行，任何一步失败都会**停下来并给出下一步怎么做**，日志留在磁盘：

1. **环境检测**：操作系统、CPU 架构、发行版、init 系统（systemd / procd / OpenRC / rc.d / launchd）、
   包管理器、可用的下载工具（curl / wget / uclient-fetch）、校验工具（sha256sum / shasum / openssl）
2. **解析版本**：默认取 GitHub 最新 tag；`-v` 可指定任意已发布版本
3. **选包**：按 `uname -s` + `uname -m` 映射到对应资产名（映射表见 [PLATFORMS.md](PLATFORMS.md#4-各架构怎么选脚本会自动判断这里是查表用)）
4. **磁盘与权限检查**：剩余空间 ≥ 30 MB；安装目录可创建、可写入（不可写时提示用 sudo 或 `--user`）
5. **下载**：两轮策略——第 1 轮每个源只试 1 次、连接 10s 超时（死源快速跳过，
   不会在一个挂掉的加速站上白等 50 秒）；第 2 轮才对失败过的源重试。
   加 `--probe` 会先给每个源测速，按实际耗时排序
6. **校验**：用 Release 里的 `SHA256SUMS` 比对，不一致直接中止
7. **解包 + 自检**：解到安装目录下的临时目录（避开 `/tmp` 可能是 noexec），
   执行 `porch -v`；mips 平台若因浮点 ABI 起不来，自动改下 softfloat 版重试
8. **安装**：先写同目录临时文件再 `mv` 原子替换，升级瞬间不会出现半个二进制
9. **服务**：按 init 系统写 systemd unit / procd 脚本 / rc.d 脚本 / launchd plist 并启动
10. **收尾**：打印访问地址、默认账号、常用命令、升级与卸载方法

**幂等**：脚本可以反复执行。同版本会直接告诉你"已是最新"（`-f` 强制重装），
不同版本则原地升级、**配置目录不动**。

---

## 3. 参数

```sh
sh install.sh -h
```

| 参数 | 说明 |
|---|---|
| `-v, --version VER` | 安装指定版本，如 `1.1.2` 或 `v1.1.2`；默认最新 |
| `-d, --dir DIR` | 程序安装目录，默认 `/usr/local/bin`（OpenWrt 自动用 `/usr/bin`）|
| `-c, --conf DIR` | 配置目录，默认 `/etc/porch/porchconf` |
| `-p, --port PORT` | 管理端口，默认 `5200`。**不给会交互问一次**（回车即默认）|
| `--file PATH` | 从本地 `tar.gz` 安装，全程不联网（离线机 / 内网 / 已下好的包）|
| `--mirror LIST` | 下载镜像，逗号分隔。空串表示直连。例：`--mirror ",https://ghfast.top/"` |
| `--probe` | 先给每个镜像**实测测速**，按耗时排序再下载（大陆网络推荐）|
| `--luci` | OpenWrt 上强制安装 LuCI 界面包（默认「系统里有 LuCI 就自动装」）|
| `--no-luci` | 不装 LuCI 界面，只要后端 |
| `--user` | 装到 `~/.local/bin` + `~/.config/porch`，不装服务 |
| `--no-service` | 只放二进制，不创建开机自启 |
| `--insecure` | 跳过 TLS 校验（设备 CA 过期时用，有中间人风险）|
| `--check` | 只输出环境检测结果，不改任何东西 |
| `--dry-run` | 打印将执行的动作，不落盘 |
| `--uninstall` | 卸载：服务 + 二进制 + LuCI 界面包，**配置保留** |
| `--purge` | 卸载时连配置目录一起删 |
| `-f, --force` | 同版本也重装 |
| `-y, --yes` | 非交互 |
| `--debug` | 输出调试信息 |
| `http://…` 位置参数 | 自建下载源根地址，等同 `PORCH_BASE_URL`（兼容 Lucky 脚本的传参风格）|

### 3.1 端口为什么不算「写死」

`-p` 指定的端口不会写进服务启动参数，而是写进程序配置 `base.json` 的
`adminWebListenPort`。这样以后在网页「系统设置 → 管理端口」里改端口，
**重启后是生效的**；如果端口被压死在 `ExecStart` 里，网页里改了也白改。

三种改端口的方式都行：

```sh
sh install.sh -p 8080        # 安装时装；已装过就只改端口，不重新下载二进制
# 或者在网页「系统设置 → 管理端口」里改，重启服务生效
# OpenWrt 上也可以在 路由器后台「服务 → Porch 门廊 → 基础设置」里改
```

> 只有配置目录写不进去（权限异常）时，才退回把 `-p` 塞进服务参数兜底。

环境变量（适合写进 Ansible / 批量脚本）：

```sh
PORCH_VERSION=1.1.2 PORCH_DIR=/opt/porch/bin PORCH_MIRROR=",https://ghfast.top/" sh install.sh -y
```

| 变量 | 作用 |
|---|---|
| `PORCH_VERSION` | 版本 |
| `PORCH_DIR` | 程序目录 |
| `PORCH_CONF_DIR` | 配置目录 |
| `PORCH_PORT` | 端口 |
| `PORCH_MIRROR` | 镜像列表 |
| `PORCH_PROBE` | 设为 `1` 等同 `--probe` |
| `PORCH_REPO` | 仓库坐标，默认 `likz0421/porch`（自建 fork 时用）|
| `PORCH_BASE_URL` | 自建下载源根地址，可逗号分隔多个（内网分发用，天然按顺序回退）|
| `PORCH_INSTALL_LOG` | 日志文件路径 |

---

## 4. 分平台说明

### 4.1 Linux（Debian / Ubuntu / CentOS / Alpine / Arch…）

```sh
sudo sh install.sh -y
```

- 有 systemd 时装为 `porch.service`，并带最小权限加固：
  只保留 `CAP_NET_BIND_SERVICE`（监听 80 用）、`ProtectSystem=full`、`PrivateTmp`、
  只有配置目录可写
- 没有 systemd 时依次尝试 OpenRC / sysvinit
- 端口 80 与 5200 记得放行：`ufw allow 80,5200/tcp` 或 `firewall-cmd --add-port=80/tcp --add-port=5200/tcp`

### 4.2 OpenWrt

先说清楚一件事：**OpenWrt 现在有两个时代，包格式不通用。**

| OpenWrt 版本 | 包管理器 | 包格式 | 命令 |
|---|---|---|---|
| 24.10 及更早 | `opkg` | `.ipk` | `opkg install xxx.ipk` |
| **25.12 及更新** | **`apk`** | **`.apk`**（apk-tools v3 / ADB 容器） | `apk add --allow-untrusted xxx.apk` |

先确认自己是哪一种：

```sh
cat /etc/openwrt_release | grep -E 'DISTRIB_RELEASE|DISTRIB_ARCH'
command -v apk && echo "→ apk 时代（25.12+）" || echo "→ opkg 时代（≤24.10）"
```

**方式一：脚本（推荐，两个时代都适用）**

```sh
ssh root@192.168.1.1
wget -qO- https://raw.githubusercontent.com/likz0421/porch/main/install.sh | sh
```

脚本**完全不用包管理器**：下载对应架构的静态二进制 → 放 `/usr/bin/porch` →
写 `/etc/config/porch` 与 `/etc/init.d/porch`（procd）→ `enable && restart`。
所以 opkg 的老机器和 apk 的新机器用的是同一条命令。

**方式二：apk 装（OpenWrt 25.12+）**

```sh
apk add --repositories-file /dev/null --allow-untrusted /tmp/porch_1.1.2_openwrt_arm64.apk
apk add --repositories-file /dev/null --allow-untrusted /tmp/luci-app-porch_1.1.2_all.apk   # 想要 LuCI 界面
```

> 文件名里的架构后缀是 **Go 的架构名**（`arm64` / `armv7` / `mipsle_hardfloat`…），
> 不是 OpenWrt 的子目标名，**别去下 `..._aarch64_cortex-a53.apk`，没有这个文件**。
> 你该用哪个后缀，`uname -m` 一查便知，见下面最后一小节。

- `--allow-untrusted` 是必须的：我们的包没有 OpenWrt 官方签名，apk 默认拒绝未签名包（opkg 则不管）。
- `--repositories-file /dev/null` 让 apk 别先去联网更新官方源（离线/内网环境很有用，不加会先 `fetch ... packages.adb`）。
- 装完 `/etc/init.d/porch enable` 已在 `post-install` 里做过，`/etc/init.d/porch start` 即可。
- 卸载：`apk del porch`（配置 `/etc/config/porch` 按 conffiles 规则处理）。

**方式三：opkg 装（OpenWrt 24.10 及更早）**

```sh
opkg install porch_1.1.2_openwrt_mipsle_hardfloat.ipk
opkg install luci-app-porch_1.1.2_all.ipk     # 想要 LuCI 界面时再装这个
```

ipk 的 `Architecture`、apk 的 `arch` 都写的是 `all` / `noarch`（与上游 Lucky 一致），
所以任何板型都能装——代价是装错架构包管理器不会拦，因此两个包的安装后脚本都会
**实跑一次 `porch -v`**，跑不通会明确告诉你"很可能装错了架构包"。

**方式四：手工**

```sh
tar xzf porch_1.1.2_linux_mipsle_softfloat.tar.gz
scp porch_1.1.2_linux_mipsle_softfloat/porch root@192.168.1.1:/usr/bin/
scp openwrt/porch/files/porch.init root@192.168.1.1:/etc/init.d/porch   # 从仓库取
chmod +x /etc/init.d/porch && /etc/init.d/porch enable && /etc/init.d/porch restart
```

> OpenWrt 上**不要**把二进制放在 `/tmp` 或 `/var`（重启即丢），也不建议放 `/root`。
>
> 装完 LuCI 界面在 **服务 → Porch 门廊**。`.apk` 的 LuCI 包同样要 `--allow-untrusted`。

**该选哪个架构包？** OpenWrt 的架构名（`aarch64_cortex-a53`、`arm_cortex-a7_neon-vfpv4`、
`mipsel_24kc`…）比 Go 的架构名细得多，对照表见
[docs/PLATFORMS.md 的「OpenWrt 架构名怎么对上」](PLATFORMS.md#3-openwrt-架构名怎么对上)。
一句话：`uname -m` 就能定，`armv8` 一律当 `arm64`，`armv8l` 是 32 位用户态、选 `armv7`。

### 4.3 macOS

```sh
sh install.sh -y            # 有 sudo 时写 launchd 开机自启
sh install.sh -y --user     # 只想装到 ~/.local/bin、不开机自启
```

macOS 上监听 80 端口需要 root，因此证书申请建议用 root 运行，或在路由器上做 80 端口转发。

### 4.4 FreeBSD

```sh
sudo sh install.sh -y
```

装为 `/usr/local/etc/rc.d/porch`，并在 `/etc/rc.conf` 写入 `porch_enable="YES"`。
启停：`service porch start|stop|status`。

### 4.5 Windows

**方式一：PowerShell 脚本**

```powershell
irm https://raw.githubusercontent.com/likz0421/porch/main/install.ps1 | iex
```

默认装到 `%LOCALAPPDATA%\Porch`，配置在 `%APPDATA%\porch\porchconf`，并加入用户 PATH。
参数与 bash 版对齐：`-Version` `-Dir` `-Mirror` `-NoPath` `-Uninstall` `-Purge` `-Force` `-Check`。

**方式二：zip 免安装包**

下载 `porch_windows_x86_64.zip` 解压，双击 `start.bat`。
开机自启用 `autostart-install.bat`，取消用 `autostart-remove.bat`。

**以管理员身份运行**：申请证书要用 80 端口，Windows 上 1024 以下端口需要管理员权限。

### 4.6 ARM 开发板 / NAS

- **树莓派 3/4/5** → `arm64`（64 位系统）或 `armv7`（32 位系统），脚本自动判断
- **树莓派 1 / Zero** → `armv6`
- **群晖 / 威联通**：多为 x86_64，用 SSH 登录后跑脚本；没有 systemd 时脚本会跳过开机自启，
  用 NAS 自带的"计划任务/开机脚本"调用 `/usr/local/bin/porch -cd /etc/porch/porchconf`
- **硬路由（ARMv7/MIPS）**：优先用 OpenWrt 的 ipk 或脚本方式

---

## 5. 升级

重新执行同一条安装命令即可：

```sh
sh install.sh -y
```

- 会先读当前版本，同版本直接退出（加 `-f` 强制重装）
- 升级时**配置目录原样保留**，服务会自动重启
- 想装指定版本回退：`sh install.sh -v 1.1.2 -f`

---

## 6. 卸载

```sh
sh install.sh --uninstall          # 保留配置
sh install.sh --uninstall --purge  # 连配置和证书一起删
```

会停服务、取消开机自启、删二进制（systemd/procd/rc.d/launchd 的服务文件一并清掉）。
Windows：`.\install.ps1 -Uninstall [-Purge]`。

---

## 7. 离线 / 内网安装

**A. 指向本地包（最省事）** —— 把发布页的 `tar.gz` 拷到机器上，直接指给脚本：

```sh
sh install.sh --file ./porch_1.1.2_linux_arm64.tar.gz
```

版本和架构从文件名识别（文件名不认识时用 `-v` 补一下），同目录有 `SHA256SUMS`
就顺手校验；自检、写服务、PATH 提示等后续步骤与在线安装完全一致。

**B. 用自建源**（把 release 资产放到内网 HTTP 服务的 `<根>/<tag>/` 下）：

```sh
PORCH_BASE_URL=http://192.168.1.10/porch sh install.sh -y
```

多个源用逗号分隔，按顺序回退：

```sh
PORCH_BASE_URL=http://a.internal/porch,http://b.internal/porch sh install.sh -y
```

**C. 完全手工**：下载对应 tar.gz 拷进内网，解包得到单个 `porch` 文件：

```sh
tar xzf porch_1.1.2_linux_arm64.tar.gz
install -m 755 porch_1.1.2_linux_arm64/porch /usr/local/bin/porch
/usr/local/bin/porch -v
```

然后按需自己写 systemd unit（可参考脚本生成的版本）：
`porch -cd /etc/porch/porchconf`，`Restart=always`，需要 80 端口时给
`AmbientCapabilities=CAP_NET_BIND_SERVICE`。

---

## 8. 常见问题

| 现象 | 原因与处理 |
|---|---|
| `所有镜像都没能下载到 …` | 网络/DNS 受限。换镜像：`--mirror ",https://ghfast.top/"`；或 `-v` 指定已发布版本；或走 §7 离线安装 |
| `校验和不匹配` | 下载被截断或被中间人改动。重试一次；仍失败请提 issue |
| `下载的程序无法在本机运行` | 架构识别有误（少见）。把 `uname -a` 与资产名贴到 issue；MIPS 平台脚本会自动在 hard/softfloat 间回退 |
| `无法创建目录 /usr/local/bin` | 没用 root。`sudo sh install.sh -y`，或加 `--user` |
| OpenWrt 上 `porch: not found` | 装到了 `/usr/local/bin` 但 PATH 里没有。用 `--dir /usr/bin` 重装，或 `ln -s` |
| `Illegal instruction` | MIPS/ARM 浮点或指令集不匹配。脚本已自动回退 softfloat；手工安装时请核对 §2.3 表 |
| 装了但打不开管理界面 | `systemctl status porch` / `logread \| grep porch` 看原因；确认 5200 端口未被占用；确认防火墙放行 |
| 证书申请一直失败 | HTTP-01 需要 80 端口从公网可达、域名已解析到本机；路由器上要做 80 端口转发 |
| 旧设备报 `certificate verify failed` | 设备 CA 太旧。临时用 `--insecure` 装（不推荐长期使用），或先 `opkg install ca-bundle` |
| 重装后配置"丢了" | 配置在 `/etc/porch/porchconf`（旧版 `luckyconf` 会被自动沿用）。`--purge` 才会删 |

安装日志默认在 `/var/log/porch-install.log`（不可写时在 `/tmp/porch-install.log`），
报错时最后一行会打印日志路径。

---

## 9. 从源码构建

```sh
git clone https://github.com/likz0421/porch.git
cd porch
go build -ldflags="-s -w" -o porch .          # 只编当前平台
sh build.sh 1.1.2                              # 编全部 19 个平台
sh scripts/package-release.sh 1.1.2            # 再打 zip / ipk / SHA256SUMS
```

需要 Go 1.22+；打包 zip/ipk 需要 Python 3.7+。前端是 `go:embed` 进二进制的，
**没有 npm 构建步骤**。
