include $(TOPDIR)/rules.mk

LUCI_TITLE:=LuCI 支持 - Porch（动态域名 / SSL 证书 / 反向代理）
LUCI_PKGARCH:=all
LUCI_DEPENDS:=+porch +luci-base
LUCI_EXTRA_DEPENDS:=

PKG_NAME:=luci-app-porch
PKG_VERSION:=1.1.2
PKG_RELEASE:=1

PKG_MAINTAINER:=Porch contributors <porch@users.noreply.github.com>
PKG_LICENSE:=MIT

include $(TOPDIR)/feeds/luci/luci.mk

define Package/luci-app-porch/postinst
#!/bin/sh
rm -f /tmp/luci-indexcache
exit 0
endef

$(eval $(call BuildPackage,luci-app-porch))
