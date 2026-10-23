package netutil

import "testing"

func TestCheckAccessWhitelist(t *testing.T) {
	list := []string{"192.168.1.10", "10.0.0.0/8", "  "}
	if !CheckAccess("192.168.1.10", true, false, list) {
		t.Error("白名单内的精确 IP 应允许")
	}
	if CheckAccess("192.168.1.11", true, false, list) {
		t.Error("白名单外的 IP 应拒绝")
	}
	if !CheckAccess("10.9.8.7", true, false, list) {
		t.Error("命中 CIDR 的 IP 应允许")
	}
}

func TestCheckAccessBlacklist(t *testing.T) {
	list := []string{"203.0.113.0/24", "198.51.100.7"}
	if CheckAccess("203.0.113.9", false, true, list) {
		t.Error("黑名单 CIDR 内的 IP 应拒绝")
	}
	if CheckAccess("198.51.100.7", false, true, list) {
		t.Error("黑名单精确 IP 应拒绝")
	}
	if !CheckAccess("8.8.8.8", false, true, list) {
		t.Error("黑名单外的 IP 应允许")
	}
}

func TestCheckAccessEmptyListAllows(t *testing.T) {
	if !CheckAccess("8.8.8.8", true, true, nil) {
		t.Error("空名单应默认允许")
	}
	if !CheckAccess("8.8.8.8", true, true, []string{}) {
		t.Error("空名单应默认允许")
	}
}

func TestCheckAccessInvalidEntryIgnored(t *testing.T) {
	if CheckAccess("1.2.3.4", true, false, []string{"not-an-ip", "bad/cidr"}) {
		t.Error("非法条目不应误命中")
	}
}

func TestIsGlobalIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"203.0.113.7", true},
		{"192.168.1.1", false},
		{"10.1.2.3", false},
		{"172.20.0.1", false},
		{"127.0.0.1", false},
		{"169.254.1.1", false},
		{"2408:8207:78cc::1", true},
		{"fe80::1", false},
		{"not-an-ip", false},
	}
	for _, c := range cases {
		if got := IsGlobalIP(c.ip); got != c.want {
			t.Errorf("IsGlobalIP(%q) = %v，期望 %v", c.ip, got, c.want)
		}
	}
}

func TestInterfacesNoPanic(t *testing.T) {
	list := Interfaces()
	for _, i := range list {
		if i.Name == "" {
			t.Error("网卡名称不应为空")
		}
	}
}

func TestInterfaceIPUnknown(t *testing.T) {
	if _, err := InterfaceIP("definitely-not-exist-0", "ipv4"); err == nil {
		t.Error("不存在的网卡应返回错误")
	}
}
