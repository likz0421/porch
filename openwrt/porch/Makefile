# Porch 核心服务包（DDNS + SSL 证书 + 反向代理）
include $(TOPDIR)/rules.mk

PKG_NAME:=porch
PKG_VERSION:=1.1.0
PKG_RELEASE:=1

PKG_SOURCE:=$(PKG_NAME)-$(PKG_VERSION).tar.gz
# 预编译发布模式：从 Release 下载 porch_<ver>_linux_<arch>.tar.gz 解出二进制
PKG_SOURCE_URL:=https://github.com/likz0421/porch/releases/download/v$(PKG_VERSION)
PKG_HASH:=skip

# 架构映射：OpenWrt ARCH -> 发布包目录名
ifeq ($(ARCH),x86_64)
  PORCH_ARCH:=amd64
endif
ifeq ($(ARCH),i386)
  PORCH_ARCH:=386
endif
ifeq ($(ARCH),aarch64)
  PORCH_ARCH:=arm64
endif
ifeq ($(ARCH),arm)
  PORCH_ARCH:=armv7
  ifeq ($(BOARD),bcm53xx)
    PORCH_ARCH:=armv6
  endif
  ifeq ($(BOARD),kirkwood)
    PORCH_ARCH:=armv5
  endif
endif
ifeq ($(ARCH),mipsel)
  PORCH_ARCH:=mipsle
endif
ifeq ($(ARCH),mips)
  PORCH_ARCH:=mips
endif

PKG_MAINTAINER:=Porch contributors <porch@users.noreply.github.com>
PKG_LICENSE:=MIT
PKG_LICENSE_FILES:=LICENSE

include $(INCLUDE_DIR)/package.mk

define Package/porch
  SECTION:=net
  CATEGORY:=Network
  TITLE:=Porch - DDNS / SSL certificate / reverse proxy
  URL:=https://github.com/likz0421/porch
  DEPENDS:=@(aarch64||arm||armeb||i386||mips||mipsel||x86_64)
endef

define Package/porch/description
 Porch 提供动态域名解析(DDNS)、ACME SSL 证书自动签发与续期、
 以及基于域名/路径的 HTTP 反向代理，自带毛玻璃风格 Web 管理界面。
endef

define Build/Compile
	# 预编译二进制发布模式，无需在固件构建时编译
endef

define Package/porch/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/$(PORCH_ARCH)/porch $(1)/usr/bin/porch
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/porch.init $(1)/etc/init.d/porch
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_CONF) ./files/porch.uci $(1)/etc/config/porch
	$(INSTALL_DIR) $(1)/etc/porch
endef

define Package/porch/conffiles
/etc/config/porch
endef

define Package/porch/postinst
#!/bin/sh
chmod 755 /etc/init.d/porch
/etc/init.d/porch enable
exit 0
endef

define Package/porch/prerm
#!/bin/sh
/etc/init.d/porch disable
/etc/init.d/porch stop
exit 0
endef

$(eval $(call BuildPackage,porch))
