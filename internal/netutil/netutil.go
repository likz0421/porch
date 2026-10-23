// Package netutil 提供 IP 名单判定、公网 IP 获取、网卡枚举等通用网络工具。
package netutil

import (
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// IfInfo 网卡信息
type IfInfo struct {
	Name string   `json:"name"`
	IPv4 []string `json:"ipv4"`
	IPv6 []string `json:"ipv6"`
	MAC  string   `json:"mac"`
}

// CheckAccess 判断 IP 是否允许访问。
// 白名单优先：开启白名单时，只有名单内（精确匹配或 CIDR 命中）的 IP 允许；
// 黑名单开启时，名单内的 IP 一律拒绝。
func CheckAccess(ip string, whiteEnable, blackEnable bool, list []string) bool {
	if len(list) == 0 {
		return true
	}
	inList := false
	for _, item := range list {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			if _, cidr, err := net.ParseCIDR(item); err == nil {
				if cidr.Contains(net.ParseIP(ip)) {
					inList = true
					break
				}
			}
			continue
		}
		if item == ip {
			inList = true
			break
		}
	}
	if whiteEnable {
		return inList
	}
	if blackEnable && inList {
		return false
	}
	return true
}

// Interfaces 列出本机网卡地址
func Interfaces() []IfInfo {
	list, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]IfInfo, 0, len(list))
	for _, i := range list {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		info := IfInfo{Name: i.Name, MAC: i.HardwareAddr.String()}
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				info.IPv4 = append(info.IPv4, ip.String())
			} else if ip.To16() != nil {
				info.IPv6 = append(info.IPv6, ip.String())
			}
		}
		out = append(out, info)
	}
	return out
}

// InterfaceIP 返回指定网卡的地址，ipType 为 ipv4/ipv6
func InterfaceIP(name, ipType string) (string, error) {
	list, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, i := range list {
		if i.Name != name {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		var fallback string
		for _, a := range addrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if ipType == "ipv6" {
				if ip.To4() == nil && ip.To16() != nil {
					return ip.String(), nil
				}
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				if !isPrivate(ip) {
					return ip.String(), nil
				}
				if fallback == "" {
					fallback = ip.String()
				}
			}
		}
		if fallback != "" {
			return fallback, nil
		}
	}
	return "", io.ErrUnexpectedEOF
}

func isPrivate(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		return v4[0] == 127 || // 回环
			v4[0] == 10 ||
			(v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31) ||
			(v4[0] == 192 && v4[1] == 168) ||
			(v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127) ||
			(v4[0] == 169 && v4[1] == 254) // 链路本地
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate()
}

var v4Sources = []string{
	"https://api.ipify.org",
	"https://myip.ipip.net",
	"https://ipv4.icanhazip.com",
	"https://api-ipv4.ip.sb/ip",
	"https://ip.3322.net",
}

var v6Sources = []string{
	"https://api6.ipify.org",
	"https://ipv6.icanhazip.com",
	"https://api-ipv6.ip.sb/ip",
	"https://speed.neu6.edu.cn/getIP.php",
}

var httpClient = &http.Client{Timeout: 8 * time.Second}

// 探测结果缓存：同一 IP 类型的多个任务在短时间内共享一次探测结果
var (
	ipCacheMu sync.Mutex
	ipCache   = map[string]ipCacheItem{}
)

type ipCacheItem struct {
	ip  string
	err error
	exp time.Time
}

const (
	ipCacheTTL    = 30 * time.Second
	ipCacheErrTTL = 10 * time.Second
)

// GetPublicIP 通过外部接口获取本机公网地址，ipType: ipv4/ipv6
func GetPublicIP(ipType string) (string, error) {
	now := time.Now()
	ipCacheMu.Lock()
	if it, ok := ipCache[ipType]; ok && it.exp.After(now) {
		ipCacheMu.Unlock()
		return it.ip, it.err
	}
	ipCacheMu.Unlock()

	ip, err := probePublicIP(ipType)

	ipCacheMu.Lock()
	ttl := ipCacheTTL
	if err != nil {
		ttl = ipCacheErrTTL
	}
	ipCache[ipType] = ipCacheItem{ip: ip, err: err, exp: time.Now().Add(ttl)}
	ipCacheMu.Unlock()
	return ip, err
}

func probePublicIP(ipType string) (string, error) {
	src := v4Sources
	if ipType == "ipv6" {
		src = v6Sources
	}
	var lastErr error
	for _, u := range src {
		resp, err := httpClient.Get(u)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		txt := strings.TrimSpace(string(body))
		// ipip.net 返回 "当前 IP：1.2.3.4 来自于：..." 这类文本，需要抽取出 IP
		for _, field := range strings.FieldsFunc(txt, func(r rune) bool {
			return r == ' ' || r == '\n' || r == '\t' || r == '：' || r == ',' || r == ';'
		}) {
			field = strings.Trim(field, "：:，,。.")
			if ip := net.ParseIP(field); ip != nil {
				if ipType == "ipv6" {
					if ip.To4() == nil && ip.To16() != nil {
						return ip.String(), nil
					}
					continue
				}
				if ip.To4() != nil {
					return ip.String(), nil
				}
			}
		}
		lastErr = io.ErrUnexpectedEOF
	}
	if lastErr == nil {
		lastErr = io.EOF
	}
	return "", lastErr
}

// IsGlobalIP 判断是否为可对外访问的全局地址
func IsGlobalIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if parsed.To4() != nil {
		return !isPrivate(parsed)
	}
	return !parsed.IsLoopback() && !parsed.IsLinkLocalUnicast() && !parsed.IsPrivate()
}
