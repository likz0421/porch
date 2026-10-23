# 支持的平台与架构

Porch 的发布范围**对齐上游 Lucky 的全部平台**，并在其基础上补了两个方向：Windows 免安装 zip、
mips64le。所有二进制都是**纯静态链接**（`CGO_ENABLED=0`），目标机器不需要 libc、不需要 Go、
不需要 Node，复制过去就能跑。

---

## 1. 上游 Lucky 提供了什么

以 `gdy666/lucky` 最新版 **v2.27.2** 的 Release 资产为准，共 18 个 tar.gz：

| 类别 | 资产 |
|---|---|
| Linux（11） | `Linux_x86_64` `Linux_i386` `Linux_arm64` `Linux_armv7` `Linux_armv6` `Linux_armv5` `Linux_mips_hardfloat` `Linux_mips_softfloat` `Linux_mipsle_hardfloat` `Linux_mipsle_softfloat` `Linux_riscv64` |
| macOS（2） | `darwin_arm64` `darwin_x86_64` |
| FreeBSD（3） | `freebsd_x86_64` `freebsd_i386` `freebsd_arm64` |
| Windows（2） | `Windows_x86_64` `Windows_i386` |

> 合计 11 + 2 + 3 + 2 = **18 个**。

此外，Lucky 在 1.x 时期还发布过 OpenWrt 安装包（2.x 起只发 tar.gz）：

| 资产 | 覆盖架构 |
|---|---|
| `lucky_<ver>_Openwrt_<arch>.ipk` | x86_64 / i386 / arm64 / armv7 / armv6 / armv5 / mips_hardfloat / mips_softfloat / mipsle_hardfloat / mipsle_softfloat（共 10 个）|
| `luci-app-lucky_<ver>_all.ipk` | LuCI 界面（`Architecture: all`）|

我们把这两类也都补齐了，并且额外加了 riscv64 的 ipk。

---

## 2. Porch 的发布矩阵

### 2.1 通用包（tar.gz，19 个）

| 平台 | 架构 | 资产名 | 典型设备 |
|---|---|---|---|
| Linux | x86_64 | `porch_<ver>_linux_x86_64.tar.gz` | 软路由、x86 小主机、云服务器 |
| Linux | i386 | `porch_<ver>_linux_i386.tar.gz` | 老 32 位 x86 |
| Linux | arm64 | `porch_<ver>_linux_arm64.tar.gz` | 树莓派 3/4/5、ARM64 路由/NAS |
| Linux | armv7 | `porch_<ver>_linux_armv7.tar.gz` | 绝大多数硬路由（Cortex-A7/A9/A15）|
| Linux | armv6 | `porch_<ver>_linux_armv6.tar.gz` | 树莓派 1/Zero、老 ARM 路由 |
| Linux | armv5 | `porch_<ver>_linux_armv5.tar.gz` | 老款 Marvell/Kirkwood NAS、路由器 |
| Linux | mips_hardfloat | `porch_<ver>_linux_mips_hardfloat.tar.gz` | 大端 MIPS，带 FPU |
| Linux | mips_softfloat | `porch_<ver>_linux_mips_softfloat.tar.gz` | 大端 MIPS，无 FPU |
| Linux | mipsle_hardfloat | `porch_<ver>_linux_mipsle_hardfloat.tar.gz` | 小端 MIPS（多数 MTK 路由），带 FPU |
| Linux | mipsle_softfloat | `porch_<ver>_linux_mipsle_softfloat.tar.gz` | 小端 MIPS，无 FPU |
| Linux | riscv64 | `porch_<ver>_linux_riscv64.tar.gz` | RISC-V 开发板、新路由器 |
| Linux | mips64le（额外） | `porch_<ver>_linux_mips64le_softfloat.tar.gz` | 龙芯/君正等 MIPS64 小端 |
| macOS | arm64 | `porch_<ver>_darwin_arm64.tar.gz` | Apple Silicon |
| macOS | x86_64 | `porch_<ver>_darwin_x86_64.tar.gz` | Intel Mac |
| FreeBSD | x86_64 | `porch_<ver>_freebsd_x86_64.tar.gz` | FreeBSD 服务器/防火墙 |
| FreeBSD | i386 | `porch_<ver>_freebsd_i386.tar.gz` | 老 32 位 FreeBSD |
| FreeBSD | arm64 | `porch_<ver>_freebsd_arm64.tar.gz` | ARM64 FreeBSD |
| Windows | x86_64 | `porch_<ver>_windows_x86_64.tar.gz` | 命令行使用（GUI 请用 zip）|
| Windows | i386 | `porch_<ver>_windows_i386.tar.gz` | 32 位 Windows |

### 2.2 Windows 免安装包（zip，2 个）

```
porch_windows_x86_64.zip      # porch.exe + start.bat + autostart-*.bat + 使用说明.txt
porch_windows_i386.zip
```
解压即用；`start.bat` 前台启动并自动打开浏览器。

### 2.3 OpenWrt 包（ipk，11 + 1 个）

```
porch_<ver>_openwrt_<arch>.ipk       # arch ∈ x86_64 i386 arm64 armv7 armv6 armv5
                                     #        mips_hardfloat mips_softfloat
                                     #        mipsle_hardfloat mipsle_softfloat riscv64
luci-app-porch_<ver>_all.ipk         # LuCI 界面（服务 → Porch 门廊）
```

ipk 的 `Architecture` 字段是 `all`（和上游 Lucky 的做法一致）——这样同一个包能装在
各种具体板型（`arm_cortex-a7_neon-vfpv4`、`mipsel_24kc`…）上，不会被 opkg 的架构白名单挡住。
代价是**装错架构时 opkg 不会拦你**，所以安装脚本与 postinst 都会实跑一次 `porch -v` 做自检。

---

## 3. 各架构怎么选（脚本会自动判断，这里是查表用）

| `uname -m` 输出 | 选择的资产后缀 |
|---|---|
| `x86_64` / `amd64` | `x86_64` |
| `i386` `i486` `i586` `i686` | `i386` |
| `aarch64` / `arm64` | `arm64` |
| `armv8l`（64 位内核跑 32 位用户态） | `armv7` |
| `armv7l` / `armv7*` | `armv7` |
| `armv6l` / `armv6*` | `armv6` |
| `armv5l` / `armv5tel` | `armv5` |
| `mips` | `mips_hardfloat` → 跑不起来自动换 `mips_softfloat` |
| `mipsel` / `mipsle` | `mipsle_hardfloat` → 跑不起来自动换 `mipsle_softfloat` |
| `mips64le` | `mips64le_softfloat` |
| `riscv64` | `riscv64` |

**MIPS 浮点**这一项没法从 `uname` 看出来（硬浮点/软浮点是 ABI 差别，不是位数差别），
所以脚本的策略是：**先下硬浮点版，装好后真的执行一次 `porch -v`**——
如果内核报 `Illegal instruction` / `Exec format error`，自动改下软浮点版再试，
两次都不行才报错并把 `uname -a` 的输出提示给用户。

---

## 4. 运行环境要求

| 项目 | 要求 |
|---|---|
| 操作系统 | Linux ≥ 2.6.32（含 OpenWrt 18.06+）、macOS 11+、FreeBSD 12+、Windows 7+ |
| CPU | 见上表，无 SSE/NEON 等额外指令集要求 |
| 运行时依赖 | **无**。静态二进制，不需要 libc / Go / Node / 数据库 |
| 磁盘 | 程序约 7 MB，配置与证书按任务数增长（每张证书约 6 KB） |
| 端口 | 管理界面默认 **16601/TCP**；申请证书需要 **80/TCP** 能从公网访问 |
| 权限 | 装到系统目录需 root；普通用户可用 `--user` 装到 `~/.local/bin`（此时 80 端口需另行授权） |
| 内存 | 常驻约 15–30 MB |

### OpenWrt 额外说明

- 固件里需要有 `ca-bundle`（TLS 校验）和 `zoneinfo`（日志时间）。
  安装脚本在下载失败时会提示 `opkg update && opkg install uclient-fetch ca-bundle`。
- `/tmp` 在部分固件上是 `noexec`，所以安装脚本把临时目录放在**安装目录下面**，
  而不是 `/tmp`——否则下载下来的二进制无法自检。
- 管理界面 16601 端口默认只监听所有地址；是否允许公网访问由程序内的开关控制（默认拒绝公网来源）。
