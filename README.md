# Porch 门廊 · 内网服务门面

[![release](https://img.shields.io/github/v/release/likz0421/porch?color=4cb8f0)](https://github.com/likz0421/porch/releases)
[![license](https://img.shields.io/badge/license-MIT-6d8bf5)](LICENSE)
[![platforms](https://img.shields.io/badge/platforms-19%20targets-7b5cf0)](#安装一条命令)
[![go](https://img.shields.io/badge/go-1.22%2B-00ADD8)](https://go.dev)

> **DDNS + HTTPS 证书 + 反向代理**，一个可执行文件搞定。
> 给家里（或公司内网）的 NAS、路由器后台、摄像头、自建服务开一道**有门牌、有锁、看得见**的门。

Go 语言编写，零第三方依赖，内置浅色毛玻璃 Web 管理界面，并提供 OpenWrt LUCI 界面包。
当前版本 **1.1.0**。

---

## 安装（一条命令）

```sh
# Linux / OpenWrt / macOS / FreeBSD
curl -fsSL https://raw.githubusercontent.com/likz0421/porch/main/install.sh | sh
```

```powershell
# Windows（PowerShell）
irm https://raw.githubusercontent.com/likz0421/porch/main/install.ps1 | iex
```

脚本会自己完成这些事：**识别系统与 CPU 架构 → 选对应的包 → 多镜像重试下载 → 校验 SHA256 →
装好实跑一次自检 → 配好开机自启**。可以反复执行，同版本会跳过，不同版本原地升级且配置不动。

```sh
sh install.sh --check        # 只体检，不改任何东西
sh install.sh -v 1.1.0       # 装指定版本
sh install.sh --file ./porch_1.1.0_linux_arm64.tar.gz   # 离线装（不联网）
sh install.sh --uninstall    # 卸载（--purge 连配置一起删）
sh install.sh -h             # 全部参数
```

<details>
<summary><b>覆盖哪些平台</b>（与上游 Lucky 的发布范围对齐，另加 mips64le）</summary>

| 类别 | 架构 |
|---|---|
| Linux（12） | x86_64 · i386 · arm64 · armv7 · armv6 · armv5 · mips_hardfloat · mips_softfloat · mipsle_hardfloat · mipsle_softfloat · riscv64 · mips64le |
| macOS（2） | arm64 · x86_64 |
| FreeBSD（3） | x86_64 · i386 · arm64 |
| Windows（2） | x86_64 · i386（另附免安装 zip）|
| OpenWrt（11） | 与 Linux 同架构的 `.ipk`，外加 `luci-app-porch` 界面包 |

明细与"该选哪个包"的对照表见 **[docs/PLATFORMS.md](docs/PLATFORMS.md)**；
各平台安装步骤、镜像、离线安装与排错见 **[docs/INSTALL.md](docs/INSTALL.md)**。
</details>

> 国内网络拉不动 GitHub 时：把 `https://raw.githubusercontent.com/...` 换成
> `https://ghfast.top/https://raw.githubusercontent.com/...`，或给脚本加 `--mirror ",https://ghfast.top/"`。

---

## 0. 为什么叫 Porch

英文 **porch** 是「门廊」——房子外面那小块有顶的台阶：**它不属于屋内，但你能站在这里按门铃、看到门口有没有人、把该进来的请进来。**

这三件事恰好是本程序做的三件事：

| 门廊的比喻 | 对应功能 |
|---|---|
| 门牌号会变，但邮差总能找到你家 | **DDNS**：域名始终指向你当前的公网 IP |
| 门上要有锁，锁要是正规厂家出的 | **HTTPS 证书**：ACME 自动签发与续期，浏览器不再报警 |
| 客人只到门廊，不进客厅 | **反向代理**：外部只看到一道门，内网服务不直接暴露 |

中译「门廊」同时保留了原文的意象。名字短、好念、全小写、无歧义，Linux 包名/服务名/配置目录一律 `porch`。

> **关于"参考 lucky 源码"的说明**
> lucky 3.3.x 未开源，且 `3.3.3_xiaojv` 分发包被魔改 UPX 加壳，静态无法还原（详见 `../lucky-re/逆向分析报告.md`）。
> 本项目**只对齐了使用习惯**（16601 端口、666/666 账号、`-cd` 配置目录、DDNS/证书/反代的功能边界与 OpenWrt 集成方式），
> 代码为自研实现：**零第三方依赖**，仅使用 Go 标准库。版本号使用自研编号 `1.1.0`，不冒用 3.3.3。

### 0.1 Logo

一枚**门形**徽标，48×48 圆角矩形底 + 蓝紫渐变（`#4cb8f0 → #6d8bf5 → #7b5cf0`），
内部是白色拱形门框 + 一道门槛线，门下有一点会呼吸的光。

- **门框**＝你要放出去的那个服务；**门槛**＝反向代理的分界；**光点**＝一次成功的转发/一次续期
- 三处同源：登录页 56px、侧栏 38px、浏览器 favicon（data-URI，无外部请求）
- 全部为**内联 SVG**，随主题渐变，不额外加载图片，不产生任何 HTTP 请求
- 微动效：进入页面时门框描边淡入（`logoIn` 0.7s），之后脚下光点以 3.4s 周期呼吸（`logoPulse`）

---

## 1. 功能

### 动态域名（DDNS）
- 服务商：Cloudflare、阿里云 DNS、DNSPod(腾讯云)、GoDaddy、DuckDNS、自定义 Callback URL、空操作(仅日志)
- IP 来源：公网接口探测（多源自动回退）、指定网卡、手动指定
- 支持 IPv4(A) 与 IPv6(AAAA)，自定义 TTL（默认 600）
- 定时巡检（默认 300 秒），IP 未变化时不请求 API；可设"强制更新间隔"
- Webhook 通知（支持 `#{ip}` `#{domain}` `#{subDomain}` 占位符）
- 手动立即执行、每次更新记录时间与次数

### SSL 证书（ACME v2）
- 完整 ACME v2 客户端（RFC 8555），**标准库实现**，无 lego/legolog 依赖
- HTTP-01 挑战（内置临时校验服务，默认 80 端口）
- 支持 Let's Encrypt（含测试环境）、Buypass、Google Trust Services
- 密钥类型 EC P-256 / RSA 2048
- 每小时巡检，剩余天数小于阈值（默认 20 天）自动续期
- 证书落盘为 `fullchain.pem` / `privkey.pem`，可被反代规则直接引用
- **续期失败不覆盖已签发状态**：若原证书仍在有效期内，一次网络抖动只会把原因写入 `lastError`（鼠标悬停状态列/看板剩余天数可见），看板仍按真实到期天数倒计时，不会误报「0 张已签发」

### 反向代理
- 按监听端口 + 域名(Host) + 路径前缀匹配
- 多后端按顺序轮询，自动剥离/补全 URL Scheme
- 支持 HTTPS 对外（引用已签发证书）、gzip 压缩、X-Real-IP / X-Forwarded-For 透传
- 规则增删改即时生效，无需重启

### 管理界面
- 浅色毛玻璃（glassmorphism）风格单页应用，通过 `go:embed` 内嵌进二进制，无需额外部署
- 页面：**概览（监控看板）** / 动态域名 / SSL 证书 / 反向代理 / 运行日志（SSE 实时流）/ 系统设置
- 登录会话（默认 666/666）、未开启"允许外网访问"时拒绝公网来源
- 运行日志环形缓冲 2000 条，支持实时推送
- 列表搜索、一键启用/停用、配置导出、顶栏健康徽标

### 监控看板（概览页）
- CPU / 内存使用率**实时曲线**（2 秒采样，保留最近 3 分钟 / 90 个点）
- 用量环形：CPU、内存、Goroutine、Go 堆内存
- 证书到期倒计时（≤7 天红色、≤20 天黄色），代理请求总数、DDNS 更新次数
- 健康徽标：任一任务失败 → 红色"N 项异常"；证书临期 → 黄色"N 项关注"
- 曲线由原生 Canvas 绘制（含 devicePixelRatio 适配），**不依赖任何图表库**，离线可用

### 傻瓜式设计（不需要看文档就能用）

功能都在，但**第一眼不用学**：

| 设计 | 具体做法 |
|---|---|
| 首屏就是向导 | 概览页顶部「三步上手」：① 加一个域名 ② 申请一张证书 ③ 建一条反代；做一步划掉一步（`0/3 → 3/3`），全部完成后自动收成一行，不占地方 |
| 空列表不给空白页 | 三个列表无数据时给一句话解释 **+ 一个主按钮**（"添加第一个域名"），点了就开弹窗 |
| 表单顶部一句话 | 新增/编辑弹窗最上方有一句白话说明「这一步在做什么、填错了会怎样」，不用翻文档 |
| 按钮说人话 | "重载服务" → **"应用更改"**，悬停提示"让最新配置立即生效（随时点都安全）"；"删除"弹自定义确认框，写明后果（"该证书仍会保留在磁盘上，但不再自动续期"），而不是系统原生的 OK/Cancel |
| 错误有出口 | 失败的任务在状态列直接给原因（`失败: xxx`）+ 悬停展示完整 `lastError`，不是干瞪眼一个红字 |
| 状态一眼分类 | 顶栏健康徽标：全好是绿色"一切正常"，有问题才变红并计数，出现即知道"哪里不对，先来这里看" |
| 不丢操作 | 搜索框、当前页签、显示密度都记在 `localStorage`，刷新回到原地 |

---

## 1.1 从 Lucky 迁移（旧数据不用重填）

Lucky 的 `.lkcf` 配置是**自加密**的（密钥随文件保存），本程序内置解密与转换，可把 Lucky 的
**动态域名任务**与**SSL 证书**直接搬过来，无需手工重填。

> 注意：本节出现的 `lucky` 均指**上游 Lucky 产品**（真实的第三方软件），不是本项目的旧名残留。

格式说明（依据公开样本推导）：

| 版本 | 结构 |
|---|---|
| V1（2.x） | `[0]=0x01` + AES-256 密钥(32) + IV(16) + AES-256-CBC 密文，可选 gzip |
| V2（3.x） | `"LKCF"` + 版本 + 标志位(bit0=zstd) + 载荷长度 + CRC16/CRC32 + AES-256-GCM 密文 |

映射的字段：

| Lucky 配置 | 本程序 |
|---|---|
| `DDNSTaskList[].Records[].SyncRecordData.{DomainName,SubDomainName,type,ttl}` | 一条 DDNS 任务 |
| `DNS.{Name,ID,Secret,ForceInterval}` | 服务商与凭据 |
| `V4QueryIPType/V6QueryIPType = netInterface` | IP 来源 → 指定网卡 |
| `WebhookEnable/WebhookURL/Method` | Webhook 通知 |
| `SSLCertficateList[].{CertBase64,KeyBase64}` | 证书落盘到 `certs/<名称>/` |
| `ExtParams.{acmeEmail,acmeCADirURL,acmeKeyType}` | 邮箱、CA、密钥类型 |

使用方式（Web 界面：**系统设置 → 从 Lucky 迁移**）：

```bash
curl -X POST http://127.0.0.1:16601/api/import/lucky \
  -H "X-Token: $TOKEN" -H 'Content-Type: application/json' \
  -d '{"dir":"/etc/lucky/luckyconf","apply":true,"keepEnabled":false}'
```

- `apply:false` 只预览（返回每个模块发现多少条、可导入多少条、有哪些提示）
- `keepEnabled:false`（默认）导入后一律**停用**，避免与仍在运行的 Lucky 争用端口或把域名解析到错误地址
- 证书会按原签发内容原样落盘，未到期不会重新申请
- 支持的服务商：cloudflare / alidns / dnspod / godaddy / duckdns / porkbun / callback；
  其他服务商按原值保留并给出提示
- 若 Lucky 配置使用 zstd 压缩，需先在设备上安装 `zstd`（`opkg install zstd`）

### 1.2 从旧版 Porch（原 `lucky` 目录）升级

改名对老用户是**无感**的：

- **配置目录**：启动时若默认 `porchconf` 不存在、而同级的 `luckyconf` 存在，会自动沿用旧目录并打印一行日志，不需要手动搬数据
- **登录状态**：浏览器 `localStorage` 同时认 `porch_token` 与旧的 `lucky_token`，升级后不用重新登录
- **命令行**：`-cd` 依旧是配置目录参数，环境变量由 `LUCKY_CONF_DIR` 改为 `PORCH_CONF_DIR`

---

## 2. 快速开始

```bash
# 编译（任意 Go 1.22+ 环境）
go build -ldflags="-s -w" -o porch .

# 运行（配置目录不存在会自动创建）
./porch -cd /etc/porch/porchconf

# 临时指定端口
./porch -cd ./porchconf -p 16601

# 浏览器打开 http://<设备IP>:16601  账号 666 / 密码 666
```

交叉编译（路由器常用架构）：

```bash
GOOS=linux GOARCH=amd64   go build -ldflags="-s -w" -o porch_amd64 .
GOOS=linux GOARCH=arm64   go build -ldflags="-s -w" -o porch_arm64 .
GOOS=linux GOARCH=arm   GOARM=7 go build -ldflags="-s -w" -o porch_armv7 .
GOOS=linux GOARCH=arm   GOARM=5 go build -ldflags="-s -w" -o porch_armv5 .
GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags="-s -w" -o porch_mipsle .
```

或者直接用 `./build.sh [版本号]`，一次产出全部架构 + 打包。

---

## 3. 配置文件

配置目录（默认 `./porchconf`）下的 JSON 文件，均为原子写入（临时文件 + rename），掉电不易损坏：

| 文件 | 内容 |
|---|---|
| `base.json` | 管理端口、账号密码、是否允许外网访问、HTTPS 设置、日志级别 |
| `ddns.json` | 动态域名任务列表 |
| `cert.json` | 证书任务列表 |
| `reverseproxy.json` | 反代规则列表 |
| `certs/<任务名>/` | `fullchain.pem`、`privkey.pem` |

修改配置后可在 Web 界面点 **"应用更改"**，或调用 `POST /api/service/reload`。

---

## 4. HTTP API

登录：`POST /api/login {"account":"666","password":"666"}` → `{"data":{"token":"..."}}`
后续请求在 Header 带 `X-Token: <token>`（或 URL 参数 `?token=`）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/ping` | 免登录探活（LUCI 使用） |
| GET | `/api/state` | 全部配置 + 系统信息 |
| GET | `/api/meta` | 服务商列表、CA 列表、网卡列表 |
| GET | `/api/system` | 运行信息 + 公网 IPv4/IPv6 探测 |
| PUT | `/api/base` | 保存基础设置 |
| POST | `/api/service/reload` | 重载全部模块 |
| GET/POST | `/api/ddns` | 任务列表 / 新增 |
| PUT/DELETE | `/api/ddns/{key}` | 修改 / 删除 |
| POST | `/api/ddns/{key}/run` | 立即执行一次 |
| POST | `/api/ddns/{key}/toggle` | 启用 / 停用 |
| GET/POST | `/api/cert` | 证书列表 / 新增 |
| PUT/DELETE | `/api/cert/{key}` | 修改 / 删除 |
| POST | `/api/cert/{key}/apply` | 立即申请/续期 |
| POST | `/api/cert/{key}/toggle` | 启用 / 停用 |
| GET/POST | `/api/proxy` | 反代规则列表 / 新增 |
| PUT/DELETE | `/api/proxy/{key}` | 修改 / 删除 |
| POST | `/api/proxy/{key}/toggle` | 启用 / 停用 |
| GET | `/api/metrics` | 实时指标：CPU/内存历史、证书到期、模块计数 |
| GET | `/api/export` | 导出全部配置 JSON（文件名 `porch-backup-<时间>.json`） |
| GET | `/api/logs?since=0` | 拉取日志 |
| GET | `/api/logs/stream` | SSE 实时日志流 |

示例（创建一条 Cloudflare 的 DDNS）：

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:16601/api/login \
  -H 'Content-Type: application/json' -d '{"account":"666","password":"666"}' \
  | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')

curl -s -X POST http://127.0.0.1:16601/api/ddns \
  -H "X-Token: $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"家里宽带","enable":true,"provider":"cloudflare","accessKey":"<CF Token>",
       "domain":"example.com","subDomain":"home","recordType":"A","ipType":"ipv4",
       "ipSource":"api","checkInterval":300,"ttl":600}'
```

---

## 5. OpenWrt（LUCI 界面）

源码位于 `openwrt/`，两个包：

```
openwrt/porch/            核心服务包（二进制 + init.d + UCI 配置）
openwrt/luci-app-porch/   LUCI 界面（menu.d + rpcd 插件 + JS view + 中文翻译）
```

安装方式一：放入 OpenWrt 源码树编译

```bash
cp -r openwrt/porch            package/porch
cp -r openwrt/luci-app-porch   package/luci-app-porch
make menuconfig     # LuCI -> Applications -> luci-app-porch
make package/porch/compile V=s
```

安装方式二：直接装 IPK

```bash
opkg install porch_*.ipk luci-app-porch_*.ipk
uci set porch.main.config_dir=/etc/porch/porchconf
uci commit porch
/etc/init.d/porch enable && /etc/init.d/porch restart
```

LUCI 菜单：**服务 → Porch 门廊**，可查看运行状态、启停服务、打开 Web 管理界面；
子页 **基础设置** 修改配置目录、端口、是否允许外网访问（写入 `/etc/config/porch`）。

> 打包前需把交叉编译产物按 `porch/<arch>/porch` 结构放到发布目录，并修改 `openwrt/porch/Makefile` 中的 `PKG_SOURCE_URL`。

---

## 5.1 Windows 版本

`dist/` 下提供解压即用的 Windows 包：

```
porch_windows_x86_64.zip   (64 位)
porch_windows_i386.zip     (32 位)

包内:
  porch.exe             主程序（单文件，无依赖）
  start.bat             前台启动，自动打开浏览器，关闭窗口即停止
  autostart-install.bat 开机自启（写入"启动"目录的 vbs）
  autostart-remove.bat  取消开机自启
  使用说明.txt          三步上手说明
```

- **默认配置目录为程序同目录下的 `porchconf`**（不必记命令行参数），也可用 `-cd` 指定
- 启动时自动把控制台切到 UTF-8 代码页，中文日志不会乱码
- 非管理员运行时会提示：ACME 的 HTTP-01 需要监听 80 端口，需右键"以管理员身份运行"
- 局域网其他设备访问需放行防火墙：
  ```
  netsh advfirewall firewall add rule name="porch" dir=in action=allow protocol=TCP localport=16601
  ```

Windows 版实测（本机 Windows 10/11 环境）：

- 双击 exe 不带任何参数 → 自动在 exe 同目录创建 `porchconf`
- 冒烟测试 12/12 通过（默认端口 16601）
- 中文日志正常：`[web] 账号 666 登录成功，来源 127.0.0.1`
- 反向代理实测：`18085 → 后端 18080` 正确返回后端页面
- 内存读取改用 `GlobalMemoryStatusEx`（此前 Windows 下显示 `x / 0 MB`）

**真实浏览器全流程验证**（playwright + 系统 Edge，headless，1440x900）：

```
[PASS] 登录页渲染          [PASS] 登录成功并进入主界面
[PASS] 概览统计卡片渲染     [PASS] 页面切换 ddns/cert/proxy/settings
[PASS] 新增 DDNS 任务       [PASS] 执行后状态"运行中"
[PASS] 编辑任务改名生效     [PASS] 新增反代规则
[PASS] 日志页 SSE 加载      [PASS] Lucky 迁移预览输出
[PASS] 删除任务
结果: 14 项通过, 0 项失败
```

浏览器验证抓到并修复的 UI 缺陷：表单弹窗超高时"保存"按钮被遮罩挡住无法点击
（modal 改为限高 + 底部固定、内容区滚动）。

## 5.2 其他平台（路由器 / NAS / macOS / FreeBSD）

Release 里为每个平台都准备了独立压缩包，包内是 **`porch` 单文件 + README.md**，解压即用：

```bash
tar xzf porch_1.1.0_linux_mipsle_softfloat.tar.gz
./porch_1.1.0_linux_mipsle_softfloat/porch -cd /etc/porch/porchconf -p 16601
```

完整清单（19 个 tar.gz + 2 个 Windows zip + 12 个 OpenWrt ipk）与"该选哪个"的对照表在
**[docs/PLATFORMS.md](docs/PLATFORMS.md)**。常用的几个：

```
porch_1.1.0_linux_x86_64.tar.gz        软路由 / x86 小主机 / 云服务器
porch_1.1.0_linux_arm64.tar.gz         树莓派 3/4/5、ARM64 路由与 NAS
porch_1.1.0_linux_armv7.tar.gz         多数硬路由（Cortex-A7/A9/A15）
porch_1.1.0_linux_mipsle_hardfloat.tar.gz  小端 MIPS + FPU（多数 MTK 路由）
porch_1.1.0_freebsd_x86_64.tar.gz      FreeBSD
porch_1.1.0_darwin_arm64.tar.gz        Apple Silicon Mac
porch_1.1.0_windows_x86_64.zip         Windows 免安装包（exe + 启动脚本）
```

> 不带扩展名的 `porch` 就是完整程序（静态链接，不依赖 libc）；Windows 包内是 `porch.exe`。
> 自己编译全部平台：`sh build.sh 1.1.0`；连 zip/ipk/校验和一起打：`sh scripts/package-release.sh 1.1.0`。

---

## 6. 目录结构

```
main.go                     入口（CLI 参数、模块装配、信号处理、旧配置目录回退）
internal/config/            配置结构与原子持久化
internal/logger/            环形日志缓冲 + 订阅推送
internal/netutil/           IP 名单、公网 IP 探测、网卡枚举
internal/ddns/              服务商实现 + 巡检引擎
internal/acme/              ACME v2 客户端（JWS / nonce / HTTP-01 / CSR）
internal/cert/              证书任务调度、落盘、到期巡检
internal/reverseproxy/      反代监听与转发
internal/sysinfo/           主机运行信息
internal/metrics/           CPU/内存采样与历史
internal/lkcf/              Lucky `.lkcf` 解密与导入（原 `internal/luckycfg`）
internal/webapi/            REST API、会话、访问策略
web/dist/                   毛玻璃 Web 界面（embed 进二进制）
openwrt/                    OpenWrt / LUCI 包
dist/                       Windows 免安装包
```

---

## 7. 注意事项

- ACME HTTP-01 要求 **80 端口能从公网访问**，且域名已解析到本机；路由器上需先配置端口转发。
- 管理端口（默认 16601）修改后需重启进程生效。
- 未开启"允许外网访问"时，来自公网 IP 的请求会被拒绝（返回 403），本机与局域网不受影响。
- 证书私钥文件权限为 0600，请妥善备份配置目录。

## 7.1 代码审查修复清单（第二轮）

| # | 问题 | 影响 | 处理 |
|---|---|---|---|
| 1 | ACME 挑战 token 用普通 map，HTTP 服务协程并发读 | **数据竞争，可能 panic** | 改为 RWMutex 保护的读写方法 |
| 2 | DDNS 运行任务持有启动时的配置副本 | 改服务商凭据/域名后必须重启才生效 | 每次执行前从配置存储取最新内容 |
| 3 | HTTPS 反代在启动时一次性加载证书 | 证书自动续期后**反代仍用旧证书** | 改用 `GetCertificate` + 文件 mtime 缓存，热更新 |
| 4 | 重复导入 Lucky 配置 | 产生重复任务条目 | 按名称去重，跳过并提示 |
| 5 | 登录无失败限制 | 默认账号可被暴力枚举 | 同 IP 失败 10 次锁定 10 分钟 |
| 6 | 用 `X-Forwarded-For` 判断来源 | 伪造该头即可绕过"仅内网访问" | 只取真实连接地址 |
| 7 | `gzipWriter` 未实现 `Flush` | SSE/大文件等流式响应被卡住 | 实现 `Flush` |
| 8 | 反代每次 Reload 都重建监听 | 无故断开正在传输的连接 | 规则指纹未变则不重建 |
| 9 | ACME 响应体未读完未关闭 | 连接泄漏 | 统一读取并关闭 |
| 10 | 证书申请失败后每小时重试 | 可能触发 ACME 限流封禁 | 失败后退避 6 小时 |
| 11 | 公网 IP 探测无缓存 | 多任务同时轮询放大外部请求 | 30 秒结果缓存（失败 10 秒） |
| 12 | 非 Linux 下内存统计为 0 | 界面显示 `x / 0 MB` | 前端按是否有总量分别显示 |

## 7.1.1 缺陷清单（全部由测试或浏览器探针实测发现）

不是"看代码猜"，每条都有复现手段：

| # | 缺陷 | 发现方式 | 处理 |
|---|---|---|---|
| 1 | `IsGlobalIP("127.0.0.1")` 判为公网 | `TestIsGlobalIP` 失败 | `isPrivate` 补 `127/8`、`169.254/16`，IPv6 补 loopback |
| 2 | `isPrivateIP("127.0.0.2")` 返回 false，内网放行被拒 | `TestAccessGuardLogic` 失败 | IPv4 首段等于 127 即视为回环 |
| 3 | 登录表单进入后看板全为 `--`（指标轮询未启动） | 浏览器端到端断言数值为空 | `doLogin` 与 `render()` 兜底启动 `startMetrics()` |
| 4 | 手机端顶栏盖住页面标题 | 390px 截图肉眼可见 | 620px 断点 `.main` 保留 `padding-top:74px`，`.topbar{top:70px}` |
| 5 | 页面横向溢出 92~291px | 布局探针测 `scrollWidth-clientWidth` | 证书状态长文本折叠 + `.cert-days` 限宽省略 |
| 6 | 图标模式导航项宽度参差 | 390px 截图 | 420px 断点下 `flex:1 1 0` 等分 |
| 7 | **移动端 390px 整页空白**（15 秒后页面变白） | 移动端截图探针 | 入场动画原来无条件挂在 `#content > *`（含 `opacity:0` 起始帧），定时刷新时正好落在动画窗口 → 改为只在 `#content.entering` 时播放 |
| 8 | **续期失败把「已签发」覆盖成「失败」**，看板误报 0 张已签发 | 重启实例后接口返回 `失败: 获取 ACME 目录失败`，而同批另一张证书仍是「已签发」（同一份配置、时序不同结果不同 → 竞态） | 双修复：`issue()` 开头从存储重取最新任务（避免用启动快照的空 `ExpireTime` 判定"从未签发"）+ `failStatus()` 用存储里的最新任务判定，证书仍有效则保留「已签发」只写 `lastError`；补 `TestFailStatusUsesLatestStoredTask` 等 3 个用例 |
| 9 | **空闲状态下每 6 秒约 87 次重排**（首跑空数据页最明显） | `perf-bisect` 逐项关动画定位：关掉健康徽标的 `box-shadow` 动画后 87 → 39 | 光环改由伪元素 `transform + opacity` 播放（合成层，零重排）；数字滚动改用 Web Animations，并去掉 `void offsetWidth` 这个"为重启 CSS 动画而强制一次同步布局"的写法；文本/属性只在真的变化时写 |
| 10 | 手机上四张统计卡竖排一列，首屏看不到曲线 | 390px 截图 | ≤620px 下 `.stat-grid` 改 2×2（每张约 179px），统计卡内边距与字号同步收紧 |
| 11 | 刚重启时运行时长显示「0 分 29 秒」 | 截图 + 单测 | `formatDuration` 只输出有意义的单位：`29 秒` / `5 分 9 秒` / `1 天 2 小时 3 分` |

### 7.1.2 关于第 8 条的实测证据

修复前，同一份含 3 张证书的配置重启后结果不一致（说明是并发时序问题，不是逻辑恒错）：

```
cert-23b740   失败: 获取 ACME 目录失败     ← 被覆盖
cert-038db0   已签发                        ← 未被覆盖
```

修复后（重启 + 触发一次会失败的续期，再查接口）：

```
3 张证书中 2 张状态恢复为「已签发」（剩余 91 天 / 4 天）
失败那张保留「已签发」，lastError 有值，看板仍按真实到期天数倒计时
```

---

## 7.2 实测记录（本机 Windows 环境）

编译产物（`-trimpath -ldflags="-s -w"`，零依赖单文件）：

| 架构 | 大小 | 字节 |
|---|---|---|
| linux/amd64 | 6.39 MB | 6 705 304 |
| linux/arm64 | 6.19 MB | 6 488 216 |
| linux/armv7 (GOARM=7) | 6.19 MB | 6 488 216 |
| linux/mipsle (softfloat) | 7.06 MB | 7 405 751 |
| windows/amd64 | 6.60 MB | 6 919 680 |
| windows/386 | 6.32 MB | 6 625 280 |

冒烟测试 `sh smoke.sh 16601 <配置目录>`：

```
[PASS] 探活接口 /api/ping          [PASS] 登录获取 token
[PASS] 错误密码被拒绝              [PASS] 未登录访问 /api/state 返回 401
[PASS] 状态接口 /api/state         [PASS] Web 首页返回 200
[PASS] 新建 DDNS 任务              [PASS] 触发 DDNS 执行
[PASS] DDNS 记录到解析 IP          [PASS] Lucky 配置导入预览
[PASS] 日志接口 /api/logs          [PASS] 删除测试任务
通过 12 项，失败 0 项
```

第 10 项需要有样本才测得出：目录里存在 `lucky_*.lkcf` 时才算这一项，
否则打印 `[SKIP]` 而不是报失败（实测：给了含 `lucky_ddns.lkcf` 的目录 → 12/12；
空目录 → 11 通过 + 1 跳过，退出码 0）。

其他实测：

- **反向代理**：后端 `127.0.0.1:18080` → 规则监听 `18081`，`curl http://127.0.0.1:18081/` 正确返回后端页面，规则状态"运行中"、请求计数 +1
- **Lucky 配置导入**：构造含 1 条 DDNS 任务 + 1 张证书的 `.lkcf`(V2)，解密并转换成功，落盘结果：
  ```json
  {"name":"家里宽带 · home.example.com","provider":"cloudflare","domain":"example.com",
   "subDomain":"home","ipSource":"interface","interfaceName":"pppoe-wan",
   "checkInterval":600,"forceInterval":24,"ttl":600,"webhookURL":"POST https://hook.example.com/notify"}
  ```
  证书按 base64 还原并写入 `certs/家庭证书/{fullchain.pem,privkey.pem}`，解析出 SAN 域名与过期时间
- **真实样本**：gately 仓库中的 `lucky3_ddns_default.lkcf`（Lucky 3.x，1951 字节）解密通过（该文件为空任务列表）

修复记录（实测发现的两个真实缺陷）：

1. JWK 中 EC 公钥坐标未补齐 32 字节 → ACME 返回 `Unable to validate JWS :: JWS verification error`
2. Go 的 `ecdsa.PrivateKey.Sign` 返回 ASN.1 DER（71 字节），而 JWS 的 ES256 要求 raw r‖s（64 字节）→ 已做 DER→raw 转换

**第二轮补测**：

- **证书热更新**：HTTPS 反代运行中替换证书文件，未重启进程，新连接立即使用新证书
  ```
  替换前: 证书 CN=renewed.example.com 有效期至 2027-01-22
  替换后: 证书 CN=final.example.com   有效期至 2027-01-22
  ```
- **导入去重**：同一份配置导入两次，第二次 `imported=0` 并提示"已存在同名条目，跳过"，任务总数保持 1+1 不翻倍
- **防爆破**：连续错误密码 10 次后锁定，锁定期间正确密码也被拒绝（提示剩余秒数）
- **OpenWrt 侧静态校验**：init 脚本与 rpcd 脚本 `sh -n` 通过，LUCI 两个 JS view `node --check` 通过，menu/acl 两个 JSON 解析通过
- **代码规范**：`gofmt -l` 无输出，`go vet ./...` 无告警

> 未在真机 OpenWrt/路由器上做过部署验证（本机无该环境），LUCI 页面与 init 脚本仅做了语法与结构校验。
> ACME 端到端签发需要 80 端口可从公网访问 + 域名解析到本机，本环境不满足，未能完整走通；
> 账号注册/订单创建链路已能与 Let's Encrypt 真实通信（返回过其错误响应）。

---

## 7.3 是否需要安装 Go 环境？

**结论：编译需要 Go，运行不需要。**

| 阶段 | 需要什么 | 说明 |
|---|---|---|
| 给自己/别人**运行** | 无需任何环境 | 单个可执行文件，双击或 `./porch` 即跑 |
| **交叉编译**新版本 | Go 1.22+（仅开发机） | 开发机装一次即可，目标设备不需要 |

要点：

- 本项目**零第三方依赖**，只用 Go 标准库，因此编译产物是**独立的静态二进制**（Windows 下 `porch.exe` 约 6.6 MB）。
- 前端 HTML/CSS/JS 通过 `//go:embed all:dist` 编进二进制，**没有 npm 构建步骤**，也没有 `node_modules`。
- 因此目标机器上**不需要**安装 Go、Node、数据库、VC++ 运行库或任何服务框架；
  复制一个 exe 到 Windows，或把 tar.gz 解压到路由器，就能直接运行。
- 交叉编译示例（在 Linux 上产出 Windows exe）：

```bash
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o porch.exe .
GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o porch   .   # 路由器
```

- 修改前端（`web/dist/*.js|css|html`）后**直接 `go build` 即可**，因为资源是内嵌的，不存在独立打包流程。

---

## 7.4 测试与覆盖率

```bash
go test ./...                  # 全量
go test ./... -cover           # 看覆盖率
go test ./internal/webapi -v   # 单独看某个包
```

当前结果（本机 Windows + Go 1.22.5）：

```
89 个用例，11 个包全部通过，合计语句覆盖率 42.6%
```

| 包 | 覆盖率 | 覆盖内容 |
|---|---|---|
| logger | 86.2% | 环形缓冲、级别过滤、订阅/取消、序号递增 |
| metrics | 94.0% | 采样历史长度、单调时间、数值范围、幂等启动 |
| config | 81.2% | 默认值、原子写入、损坏回退、变更通知、并发读写 |
| sysinfo | 82.7% | 时长格式化（只输出有意义单位）、CPU/内存/运行时长采集 |
| webapi | 60.0% | 登录鉴权、失败锁定、CRUD、启停、导出、指标、导入失败分支 |
| netutil | 41.9% | 白/黑名单与 CIDR、全局地址判定、网卡枚举 |
| cert | 39.5% | 目录安全命名、续期判定、状态写入、**续期失败不覆盖已签发**、**用存储最新任务判定**、队列上限 |
| reverseproxy | 38.7% | URL 归一化、指纹、轮询、Host/路径匹配、502、X-Real-IP |
| lkcf | 35.9% | `.lkcf` 解析与导入 |
| acme | 35.6% | ACME 报文、并发、证书过期解析 |
| ddns | 15.5% | 域名拼接、记录类型、IP 来源、状态写入、服务商注册表 |

> DDNS 的 `providers.go` 是各云厂商的 HTTP 实现，占该包大部分语句；未做 HTTP mock 的部分会拉低比例，
> 其余逻辑（IP 解析、状态机、服务商路由）已覆盖。

**浏览器端到端回归**（playwright + 系统 Edge，headless）：

- 5 档分辨率（1440 / 1280 / 1024 / 768 / 390）截图，**横向溢出均为 0px**
- 监控看板数据校验：CPU/内存百分比、环形、KV 列表、Canvas 实际像素尺寸
- 页面切换、CRUD、搜索、启停、导出（`porch-backup-2026-10-23.json`）、日志 SSE 全流程
- **34 项断言全通过，控制台错误 0 条**

---

## 7.5 界面动效与流畅性（实测数据）

### 动效清单（17 组 keyframes，全部只用 `transform` / `opacity` / `clip-path`）

| 场景 | 动效 | 参数 |
|---|---|---|
| 切换页面 | `rise` 上浮淡入，逐项 40ms 错开 | 0.46s cubic-bezier |
| 表格新行 | 行级淡入 | 每行 +26ms 延迟 |
| 登录成功 | `logoIn` 门框描边淡入 + 卡片 `popIn` | 0.7s |
| Logo 光点 | `logoPulse` 呼吸 | 3.4s 无限循环 |
| 数字变化 | `animateNum()` 数字滚动（cubic ease-out，40ms 节流）+ `bumpValue()` 轻微放大（Web Animations） | 420ms / 340ms |
| 打开弹窗 | `.mask` 透明度过渡 + `.modal` 缩放淡入 | 0.18s |
| 提示条 | `toastIn` / `toastOut` 滑入滑出 | 0.28s |
| 健康徽标 | 健康时 `ringPulse` 光环扩散（伪元素 transform+opacity）；异常时 `dotBlink` 红点闪烁 | 2.6s / 1.4s |
| 导航选中 | `::before` 指示条从中间生长 | 0.32s |
| 接口请求中 | 顶部 `.top-progress` 渐变进度条扫光（引用计数驱动） | 连续 |
| 曲线绘制 | `clip-path` 从左向右揭开 | 0.6s |
| 环形进度 | `stroke-dasharray` 过渡 | 0.6s |
| 骨架屏 | `shimmer` 微光扫过 | 1.4s |
| 按钮 | hover 上移 1px / active 回落 | 0.15s |

**关键取舍**：入场动画只在**真正切页**时播放。15 秒的静默刷新不会重播——
实现上由 `#content.entering` 类名做闸门，`render()` 只在 `page` 变化时调用 `playEnter()`，
否则页面会每 15 秒"闪一下"，这正是下面第 7 条缺陷的成因。

### 流畅性实测（CDP `Performance.getMetrics`，headless Edge）

| 指标 | 实测值 | 判据 |
|---|---|---|
| 静置 6 秒 LayoutCount | **38 ~ 43**（连测 3 次；首次使用的空数据页 37） | < 90 |
| 静置 6 秒 RecalcStyleCount | **38 ~ 43** | < 90 |
| 连切 6 个页面总耗时 | **309 ~ 481 ms** | < 1500 ms |
| Long Task（>50ms 卡顿） | **0 个** | 0 |
| 控制台错误 | **0 条** | 0 |

调优过程中的三个真实数字，说明每个数字都是量出来的而不是拍出来的：

| 阶段 | 6 秒重排 | 做了什么 |
|---|---|---|
| 最初 | 300+ | 每 2 秒整卡 `innerHTML` 重建 |
| 差异渲染 + 数字滚动节流 | 103 | 内容不变不重建、32ms 帧节流、`contain: layout paint`、`tabular-nums` |
| 去掉 `offsetWidth` 强制布局 + 徽标改用 transform 光环 | **38 ~ 43** | 数字放大改 Web Animations；文本与 `stroke-dashoffset` 仅在变化时写 |

> 关键的最后一个瓶颈是用二分法定位的：`perf-bisect` 逐项注入 `animation:none` 后测同一段空闲时间，
> 关掉健康徽标的 `box-shadow` 动画把 87 直接降到 39——原来那个"呼吸光晕"每帧都会拖一次布局。

### 间距与排版

- 卡片间距 `--gap: clamp(16px, 1.5vw, 22px)`（原 12px），卡片内边距 `--pad-card: clamp(18px, 1.6vw, 22px)`（原 14px）
- 统计卡 `.stat` 内边距 18/20，卡片之间留出呼吸区，不再挤成一片
- 三档响应式：≥1180px 全宽侧栏 / 860–1180px 图标栏 / ≤860px 顶部横向导航 / ≤420px 仅图标
- 网格 `auto-fit + minmax()` 流式排布，字号 `clamp()`，移动端 `100dvh`，表格横向滚动
- 手机上四张统计卡排 2×2（每张约 179px），首屏就能看到 CPU 曲线，不用先滚四屏
- 顶栏可切换"紧凑/舒适"密度，记忆到 `localStorage`

---

## 8. 许可与贡献

本项目为自研实现，采用 **MIT** 许可（见 [LICENSE](LICENSE)）。与上游 lucky 项目（`gdy666/lucky`）**无代码继承关系**，
只在"使用习惯"上做了对齐（16601 端口、`-cd` 配置目录、`.lkcf` 配置可迁移）。

- 仓库：<https://github.com/likz0421/porch>
- 问题反馈：<https://github.com/likz0421/porch/issues>（附 `porch -v`、`uname -a`、安装日志 `/var/log/porch-install.log`）
- 发布流程：打 `v*` tag 触发 `.github/workflows/release.yml`，自动跑测试 → 编 19 个平台 →
  打 zip/ipk → 生成 SHA256SUMS → 建 Release → 在 Ubuntu/macOS 上真装一遍做冒烟

### 仓库结构

```
install.sh                    一键在线安装（Linux/OpenWrt/macOS/FreeBSD）
install.ps1                   Windows 一键安装
build.sh                      交叉编译 19 个平台
scripts/package-release.sh    组装 zip / ipk / SHA256SUMS
scripts/make_ipk.py           ipk 打包（纯 stdlib，可复现）
scripts/make_assets.py        Release 资产组装
scripts/verify_ipk.py         ipk 结构自检（control 字段 / 权限位 / 文件树）
docs/INSTALL.md               各平台安装、升级、卸载、离线与排错
docs/PLATFORMS.md             平台矩阵与架构选型对照
.github/workflows/release.yml 打 tag 自动构建并发布
```
