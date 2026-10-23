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

### 2.3 OpenWrt 包（ipk 11 + 1，apk 11 + 1）

OpenWrt 从 **25.12 起把包管理器从 `opkg` 换成了 `apk`**，包格式从 `.ipk` 变成
apk-tools v3 的 `.apk`（ADB 容器）。两者**互不兼容**，所以同一份二进制我们打两套包：

```
# 老时代：OpenWrt 24.10 及更早（opkg）
porch_<ver>_openwrt_<arch>.ipk       # arch ∈ x86_64 i386 arm64 armv7 armv6 armv5
                                     #        mips_hardfloat mips_softfloat
                                     #        mipsle_hardfloat mipsle_softfloat riscv64
luci-app-porch_<ver>_all.ipk         # LuCI 界面（服务 → Porch 门廊）

# 新时代：OpenWrt 25.12 及更新（apk）
porch_<ver>_openwrt_<arch>.apk       # 同一份 arch 清单
luci-app-porch_<ver>_all.apk
```

两套包的架构清单、文件名后缀、安装后行为完全一致，只是容器格式不同。**`<arch>` 用的是
Go 的架构名（`arm64` / `armv7`…），不是 OpenWrt 的子目标名（`aarch64_cortex-a53`…）**
——这两者怎么对应见下面第 3 节。

ipk 的 `Architecture` 字段和 apk 的 `arch` 字段都写的是 `all` / `noarch`（和上游 Lucky
的做法一致）——这样同一个包能装在各种具体板型上，不会被包管理器的架构白名单挡住。
代价是**装错架构时包管理器不会拦你**，所以安装脚本与安装后脚本都会实跑一次
`porch -v` 做自检，跑不通会明确提示"很可能装错了架构包"。

两种格式的安装命令：

```sh
# apk 时代（25.12+）—— 未签名包必须加 --allow-untrusted
apk add --repositories-file /dev/null --allow-untrusted /tmp/porch_1.1.2_openwrt_arm64.apk
apk del porch                                     # 卸载

# opkg 时代（≤24.10）
opkg install porch_1.1.2_openwrt_armv7.ipk
opkg remove porch                                 # 卸载
```

> `.apk` / `.ipk` 里的二进制都是**同一个静态 ELF**（与 tar.gz 里那份逐字节相同），
> 所以不装包、直接把 tar.gz 解压丢进 `/usr/bin` 也一样能跑——`install.sh` 走的就是这条路，
> 这也是它能在两个时代用同一条命令的原因。

---

## 3. OpenWrt 架构名怎么对上

**先说结论：只看 `uname -m`。** 我们的包 `arch` 写的是 `noarch`，包管理器不做架构校验，
所以选包的唯一依据就是这条命令的输出：

```sh
uname -m
```

厂商和 OpenWrt 文档里爱说 `armv8` / `armv7` / `armv6` / `armv5`，Linux 生态（包括 Go）
只说 `arm64` / `armv7` / `armv6` / `armv5`，OpenWrt 自己的 `DISTRIB_ARCH` 又是一种更细的
写法（`aarch64_cortex-a53`、`arm_cortex-a7_neon-vfpv4`、`mipsel_24kc`…）。三套叫法指的是
同一批东西，下面这张表把它们对齐：

| `uname -m` | OpenWrt 的 `DISTRIB_ARCH` 常见值 | 该下哪个包 | 典型设备 |
|---|---|---|---|
| `x86_64` | `x86_64` | `x86_64` | 软路由、x86 小主机、PVE 里的 OpenWrt |
| `i686` `i586` `i486` `i386` | `i386_pentium4` `i386_pentium-mmx` `i386_generic` | `i386` | 老 32 位 x86 |
| `aarch64` | `aarch64_cortex-a53` `aarch64_cortex-a72` `aarch64_cortex-a55` `aarch64_generic` | **`arm64`** | ARMv8 的 64 位态：MT7986/MT7981、IPQ807x、RK3399、树莓派 3/4/5 |
| `armv8l` | 少见，通常直接是 `arm_*` | **`armv7`** | ARMv8 芯片但用户态跑的是 32 位 |
| `armv7l` | `arm_cortex-a7_neon-vfpv4` `arm_cortex-a9` `arm_cortex-a15_neon-vfpv4` `arm_cortex-a5` | `armv7` | 绝大多数硬路由（MT7621、IPQ4019、BCM53xx） |
| `armv6l` | `arm_arm1176jzf-s_vfp` | `armv6` | 树莓派 1 / Zero（BCM2835） |
| `armv5tel` `armv5l` | `arm_xscale`（IXP4xx）、`arm_arm926ej-s`（Marvell Kirkwood/Orion） | `armv5` | 老款 Marvell NAS、老路由器 |
| `mips` | `mips_24kc` `mips_mips32` `mips_4kec` | `mips_hardfloat` → 跑不动自动换 `mips_softfloat` | 大端 MIPS |
| `mipsel` | `mipsel_24kc` `mipsel_24kec+dsp` `mipsel_74kc` `mipsel_mips32` | `mipsle_hardfloat` → 跑不动自动换 `mipsle_softfloat` | 多数 MTK 路由（小端） |
| `mips64el` | `mips64el_*` | `mips64le_softfloat` | 龙芯/君正等 MIPS64 小端 |
| `riscv64` | `riscv64_generic` `riscv64` | `riscv64` | RISC-V 开发板 |
| `mips64` | `mips64_octeonplus` | **没有对应包** | 大端 MIPS64（Cavium Octeon），上游 Lucky 也没发 |

### 3.1 最容易搞混的三件事

**① `armv8` 不是包名，它有两种运行状态。**

ARMv8-A 是个 64 位架构，但它可以向下兼容跑 32 位代码。同一颗芯片，`uname -m` 的
输出取决于**内核和用户态是几位**，而这才是决定装哪个包的因素：

| 实际情况 | `uname -m` | 装哪个 |
|---|---|---|
| 64 位内核 + 64 位用户态（OpenWrt 官方固件的默认情况） | `aarch64` | `arm64` |
| 64 位内核 + 32 位用户态（部分厂商固件为了省内存这么干） | `armv8l` | `armv7` |
| 32 位内核 | `armv7l` | `armv7` |

**② `armv8` ≠ `arm64`。** 说"我的设备是 armv8"只说明了 CPU 是 ARMv8-A 指令集，
没说明它跑的是 64 位还是 32 位。**`armv8` 只能当 `armv7` 处理，除非 `uname -m` 明确
输出 `aarch64`。** 反过来，`armv8l` 里的 `l` 是 little-endian（小端），不是"64 位"。

**③ OpenWrt 的子目标名不能直接用来选包。** `arm_cortex-a7_neon-vfpv4` 里的
`neon-vfpv4` 是浮点单元特性标记。Go 的 `GOARM=7` 目标要求的是 **ARMv7 + VFPv3**
（硬浮点），而 vfpv4 是它的超集，所以 `armv7` 这一个包就能覆盖
`arm_cortex-a5` / `a7_neon-vfpv4` / `a9` / `a15_neon-vfpv4` 全部子目标，
不需要为它们各打一份。同理 `mipsel_24kec+dsp` 的 `+dsp` 也只是个特性标记。

### 3.2 一条命令自查

不确定的话直接跑这个，把三行输出对上面的表：

```sh
uname -m                                          # ① 决定装哪个包
. /etc/openwrt_release; echo "$DISTRIB_ARCH"       # ② OpenWrt 自己的叫法（仅参考）
grep -m1 -iE 'model name|Processor|system type|cpu model' /proc/cpuinfo   # ③ 芯片型号
```

装完再验一次：

```sh
/usr/bin/porch -v
# 正常输出形如： porch 1.1.2 (release) linux/arm64
# 后面那个 xxx/yyy 就是实际能跑的架构；如果报 Exec format error 或 Illegal instruction，
# 就是装错了（MIPS 机型报 Illegal instruction 请换 softfloat 版）。
```

> `install.sh` 会自动做完上面这套判断（`uname -m` → 下载对应 tar.gz → 实跑 `porch -v` 校验），
> 所以能用脚本就别手动挑包。手动挑包时才需要查这张表。

---

## 4. 各架构怎么选（脚本会自动判断，这里是查表用）

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

这张表对 **Linux 和 OpenWrt 是同一套**（OpenWrt 上跑的仍是标准 Linux ELF）。但 OpenWrt
还有一个额外变量：厂商和 OpenWrt 自己的架构叫法跟这里不一样，`armv8` / `armv8l` 这类
容易踩坑的情况单独写在上面第 3 节 [「OpenWrt 架构名怎么对上」](#3-openwrt-架构名怎么对上)。

---

## 5. 运行环境要求

| 项目 | 要求 |
|---|---|
| 操作系统 | Linux ≥ 2.6.32（含 OpenWrt 18.06+）、macOS 11+、FreeBSD 12+、Windows 7+ |
| CPU | 见上表，无 SSE/NEON 等额外指令集要求 |
| 运行时依赖 | **无**。静态二进制，不需要 libc / Go / Node / 数据库 |
| 磁盘 | 程序约 7 MB，配置与证书按任务数增长（每张证书约 6 KB） |
| 端口 | 管理界面默认 **5200/TCP**；申请证书需要 **80/TCP** 能从公网访问 |
| 权限 | 装到系统目录需 root；普通用户可用 `--user` 装到 `~/.local/bin`（此时 80 端口需另行授权） |
| 内存 | 常驻约 15–30 MB |

### OpenWrt 额外说明

- 固件里需要有 `ca-bundle`（TLS 校验）和 `zoneinfo`（日志时间）。安装脚本在下载失败时
  会按机型提示：apk 时代 `apk add ca-bundle zoneinfo`，opkg 时代
  `opkg update && opkg install ca-bundle zoneinfo`。
- **包管理器的两个时代**：≤24.10 是 `opkg` + `.ipk`，≥25.12 是 `apk` + `.apk`。
  `install.sh` 两者都不用（自己放静态二进制 + 写 procd 脚本），所以一条命令通吃；
  只有想用包管理器装的时候才需要区分，见
  [INSTALL.md 的 4.2 节](INSTALL.md#42-openwrt)。
- `/tmp` 在部分固件上是 `noexec`，所以安装脚本把临时目录放在**安装目录下面**，
  而不是 `/tmp`——否则下载下来的二进制无法自检。
- 管理界面 5200 端口默认只监听所有地址；是否允许公网访问由程序内的开关控制（默认拒绝公网来源）。
