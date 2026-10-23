# Porch 核心服务包（DDNS + SSL 证书 + 反向代理）
#
# 这个 Makefile 同时适用于 OpenWrt 的两个包管理时代：
#   CONFIG_USE_APK 未开（≤24.10）→ 产出 .ipk，走 opkg
#   CONFIG_USE_APK 打开（≥25.12）→ 产出 .apk，走 apk-tools v3
# 我们不需要为两者写两份逻辑：package.mk / package-pack.mk 会把下面这些
# Package/<name>/install、/conffiles、/postinst、/prerm 自动翻译成对应格式
# （apk 那边映射成 post-install / post-upgrade / pre-deinstall 维护脚本 +
#  /lib/apk/packages/<pkg>.conffiles_static 记账文件）。
include $(TOPDIR)/rules.mk

PKG_NAME:=porch
PKG_VERSION:=1.1.1
PKG_RELEASE:=1

# --------------------------------------------------------------------------- #
# 架构映射：OpenWrt 的 ARCH + CPU_TYPE → 我们 Release 资产里的架构后缀
#
# 注意：资产名用的是 Go 的架构名（x86_64 / i386 / arm64 / armv7 / armv6 /
# armv5 / mips_hardfloat / mipsle_softfloat …），**不是** amd64 / 386，
# 也不是 OpenWrt 的子目标名（aarch64_cortex-a53、mipsel_24kc）。
# --------------------------------------------------------------------------- #
ifeq ($(ARCH),x86_64)
  PORCH_ARCH:=x86_64
endif
ifeq ($(ARCH),i386)
  PORCH_ARCH:=i386
endif
ifeq ($(ARCH),aarch64)
  PORCH_ARCH:=arm64
endif
ifeq ($(ARCH),riscv64)
  PORCH_ARCH:=riscv64
endif

# arm 细分：CPU_TYPE 决定是 v7 / v6 / v5
#   arm1176jzf-s → 树莓派 1 / Zero（BCM2835）→ armv6
#   xscale / arm926ej-s → IXP4xx / Marvell Kirkwood-Orion → armv5
#   其余（cortex-a5/a7/a9/a15、mpcore…）→ armv7
ifeq ($(ARCH),arm)
  ifneq ($(findstring arm1176,$(CPU_TYPE)),)
    PORCH_ARCH:=armv6
  else ifneq ($(findstring xscale,$(CPU_TYPE)),)
    PORCH_ARCH:=armv5
  else ifneq ($(findstring arm926,$(CPU_TYPE)),)
    PORCH_ARCH:=armv5
  else
    PORCH_ARCH:=armv7
  endif
endif

# MIPS 的大小端由 ARCH 区分，硬/软浮点由 CONFIG_SOFT_FLOAT 区分
# （这是 ABI 差别，从 uname 看不出来，但在 SDK 里是明确的编译选项）
ifeq ($(ARCH),mips)
  ifeq ($(CONFIG_SOFT_FLOAT),y)
    PORCH_ARCH:=mips_softfloat
  else
    PORCH_ARCH:=mips_hardfloat
  endif
endif
ifeq ($(ARCH),mipsel)
  ifeq ($(CONFIG_SOFT_FLOAT),y)
    PORCH_ARCH:=mipsle_softfloat
  else
    PORCH_ARCH:=mipsle_hardfloat
  endif
endif
ifeq ($(ARCH),mips64el)
  PORCH_ARCH:=mips64le_softfloat
endif

ifeq ($(PORCH_ARCH),)
  # 不用 $(error)：OpenWrt 生成包元数据时会用当前 target 的 ARCH 求值本文件，
  # 一旦 error 会让整个 menuconfig 挂掉。这里只警告并把候选集交给下面的
  # DEPENDS 过滤（@(...) 会让包装不出现在不支持的架构上）。
  PORCH_ARCH:=unsupported
  $(warning porch: 未收录的目标架构 ARCH=$(ARCH) CPU_TYPE=$(CPU_TYPE)，该架构下不会构建 porch)
endif

# 预编译发布模式：从 Release 直接取 porch_<ver>_linux_<arch>.tar.gz 解出二进制，
# 固件构建时不需要 Go 工具链。
PKG_SOURCE:=$(PKG_NAME)_$(PKG_VERSION)_linux_$(PORCH_ARCH).tar.gz
PKG_SOURCE_URL:=https://github.com/likz0421/porch/releases/download/v$(PKG_VERSION)
PKG_HASH:=skip

PKG_MAINTAINER:=Porch contributors <porch@users.noreply.github.com>
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE

include $(INCLUDE_DIR)/package.mk

define Package/porch
  SECTION:=net
  CATEGORY:=Network
  TITLE:=Porch - DDNS / SSL certificate / reverse proxy
  URL:=https://github.com/likz0421/porch
  DEPENDS:=@(aarch64||arm||armeb||i386||mips||mipsel||mips64el||x86_64||riscv64)
endef

define Package/porch/description
 Porch 提供动态域名解析(DDNS)、ACME SSL 证书自动签发与续期、
 以及基于域名/路径的 HTTP 反向代理，自带浅色毛玻璃风格 Web 管理界面。
endef

# 我们的 tar.gz 里有一层顶层目录（porch_<ver>_linux_<arch>/），
# 默认的 Build/Unpack 不会剥掉它。这里显式剥一层，让二进制落在
# $(PKG_BUILD_DIR)/porch。
define Build/Unpack
	rm -rf $(PKG_BUILD_DIR)
	mkdir -p $(PKG_BUILD_DIR)
	$(TAR) $(TAR_OPTIONS) --strip-components=1 -C $(PKG_BUILD_DIR) -xzf $(DL_DIR)/$(PKG_SOURCE)
endef

define Build/Compile
	# 预编译二进制发布模式，无需在固件构建时编译
endef

define Package/porch/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/porch $(1)/usr/bin/porch
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/porch.init $(1)/etc/init.d/porch
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/porch.uci $(1)/etc/config/porch
	$(INSTALL_DIR) $(1)/etc/porch
endef

# apk 时代这一行会被 package-pack.mk 翻译成
# /lib/apk/packages/porch.conffiles + conffiles_static；
# ipk 时代它就是 control 文件里的 Conffiles 字段。两边共用同一份声明。
define Package/porch/conffiles
/etc/config/porch
endef

define Package/porch/postinst
#!/bin/sh
if [ -z "$${IPKG_INSTROOT}" ]; then
	chmod 755 /etc/init.d/porch
	/etc/init.d/porch enable >/dev/null 2>&1 || true
	# 兜底：不同版本 rc.common 的 enable 行为不完全一致，无条件确认 rc.d 链接
	if [ ! -L /etc/rc.d/S99porch ]; then
		mkdir -p /etc/rc.d 2>/dev/null || true
		ln -sf ../init.d/porch /etc/rc.d/S99porch 2>/dev/null || true
		ln -sf ../init.d/porch /etc/rc.d/K15porch 2>/dev/null || true
	fi
	if ! /usr/bin/porch -v >/dev/null 2>&1; then
		echo "porch: [警告] /usr/bin/porch 无法在本机执行，很可能装错了架构包" >&2
		echo "porch: 本机 uname -m = $$(uname -m)；请按架构名重新下载" >&2
	fi
fi
exit 0
endef

define Package/porch/prerm
#!/bin/sh
if [ -z "$${IPKG_INSTROOT}" ]; then
	/etc/init.d/porch disable
	/etc/init.d/porch stop
fi
exit 0
endef

$(eval $(call BuildPackage,porch))
