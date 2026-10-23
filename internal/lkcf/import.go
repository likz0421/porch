package lkcf

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"porch/internal/config"
	"porch/internal/logger"
)

// 顶层字段名（沿用 Lucky 配置中的写法）
const (
	keyDDNS = "DDNSTaskList"
	keySSL  = "SSLCertficateList" // Lucky 原字段名拼写如此
)

// luckyDDNSTask Lucky 动态域名任务（仅取迁移所需字段）
type luckyDDNSTask struct {
	TaskName string `json:"TaskName"`
	Enable   bool   `json:"Enable"`
	TaskType string `json:"TaskType"`

	V4QueryIPEnable bool   `json:"V4QueryIPEnable"`
	V4QueryIPType   string `json:"V4QueryIPType"`
	V4QueryUrl      string `json:"V4QueryUrl"`
	V4NetInterface  string `json:"V4NetInterface"`

	V6QueryIPEnable bool   `json:"V6QueryIPEnable"`
	V6QueryIPType   string `json:"V6QueryIPType"`
	V6QueryUrl      string `json:"V6QueryUrl"`
	V6NetInterface  string `json:"V6NetInterface"`

	Records []struct {
		Key            string         `json:"Key"`
		Disable        bool           `json:"Disable"`
		SyncRecordData map[string]any `json:"SyncRecordData"`
	} `json:"Records"`

	Domains []string `json:"Domains"`

	DNS struct {
		Name          string `json:"Name"`
		ID            string `json:"ID"`
		Secret        string `json:"Secret"`
		ForceInterval int    `json:"ForceInterval"`
		Callback      struct {
			URL    string `json:"URL"`
			Method string `json:"Method"`
		} `json:"Callback"`
	} `json:"DNS"`

	WebhookEnable bool   `json:"WebhookEnable"`
	WebhookURL    string `json:"WebhookURL"`
	WebhookMethod string `json:"WebhookMethod"`

	Intervals int `json:"Intervals"`
}

// luckyCert Lucky 证书条目
type luckyCert struct {
	Enable              bool           `json:"Enable"`
	AddFrom             string         `json:"AddFrom"`
	Remark              string         `json:"Remark"`
	CertBase64          string         `json:"CertBase64"`
	KeyBase64           string         `json:"KeyBase64"`
	IssuerCertificate   string         `json:"IssuerCertificate"`
	MappingToPath       bool           `json:"MappingToPath"`
	MappingPath         string         `json:"MappingPath"`
	MappingChangeScript string         `json:"MappingChangeScript"`
	ExtParams           map[string]any `json:"ExtParams"`
}

// Report 单个模块的导入结果
type Report struct {
	Module   string   `json:"module"`
	Title    string   `json:"title"`
	Found    int      `json:"found"`
	Imported int      `json:"imported"`
	Warnings []string `json:"warnings,omitempty"`
}

// Result 一次导入的总体结果
type Result struct {
	Reports  []Report `json:"reports"`
	DDNS     int      `json:"ddns"`
	Cert     int      `json:"cert"`
	Applied  bool     `json:"applied"`
	Warnings []string `json:"warnings,omitempty"`
}

// Skipped 生成"已存在同名条目"的提示
func Skipped(name string) string {
	return name + ": 已存在同名条目，跳过"
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func num(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	}
	return 0
}

func decodePEM(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "-----BEGIN") {
		return s + "\n"
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b)) + "\n"
}

// providerAlias 把 Lucky 的服务商名映射到本程序支持的标识
var providerAlias = map[string]string{
	"cloudflare":   "cloudflare",
	"cf":           "cloudflare",
	"alidns":       "alidns",
	"aliyun":       "alidns",
	"dnspod":       "dnspod",
	"tencentcloud": "dnspod",
	"godaddy":      "godaddy",
	"duckdns":      "duckdns",
	"porkbun":      "porkbun",
	"callback":     "callback",
	"webhook":      "callback",
}

func mapProvider(name string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(name))
	v, ok := providerAlias[key]
	return v, ok
}

func caFromURL(u string) string {
	switch {
	case strings.Contains(u, "acme-staging"):
		return "letsencrypt-staging"
	case strings.Contains(u, "buypass"):
		return "buypass"
	case strings.Contains(u, "pki.goog"):
		return "google"
	default:
		return "letsencrypt"
	}
}

// ImportDDNS 把 Lucky 的动态域名任务转换为本程序的任务
func ImportDDNS(doc Document, keepEnabled bool) ([]config.DDNSTask, *Report) {
	raw, ok := doc[keyDDNS]
	if !ok {
		return nil, nil
	}
	var src []luckyDDNSTask
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, &Report{Module: "ddns", Title: "动态域名", Warnings: []string{"解析失败: " + err.Error()}}
	}
	rep := &Report{Module: "ddns", Title: "动态域名", Found: len(src)}
	var out []config.DDNSTask

	for i, lt := range src {
		label := lt.TaskName
		if label == "" {
			label = fmt.Sprintf("第 %d 个任务", i+1)
		}
		provider, supported := mapProvider(lt.DNS.Name)
		if !supported {
			rep.Warnings = append(rep.Warnings, label+": 服务商 "+lt.DNS.Name+" 未内置，已按原值保留（需自行确认接口可用）")
			provider = strings.ToLower(lt.DNS.Name)
		}

		// 每个解析记录生成一条任务
		for j, lr := range lt.Records {
			d := lr.SyncRecordData
			domain := str(d, "DomainName")
			sub := str(d, "SubDomainName")
			if domain == "" {
				rep.Warnings = append(rep.Warnings, label+fmt.Sprintf(": 第 %d 条记录缺少主域名，已跳过", j+1))
				continue
			}
			rtype := strings.ToUpper(str(d, "type"))
			ipType := "ipv4"
			if rtype == "AAAA" {
				ipType = "ipv6"
			}
			if rtype == "" {
				rtype = "A"
			}
			ipSource := "api"
			manualIP := ""
			if ipType == "ipv6" {
				if lt.V6QueryIPType == "netInterface" && lt.V6NetInterface != "" {
					ipSource = "interface"
					manualIP = lt.V6NetInterface
				}
			} else if lt.V4QueryIPType == "netInterface" && lt.V4NetInterface != "" {
				ipSource = "interface"
				manualIP = lt.V4NetInterface
			}

			t := config.DDNSTask{
				Key:           config.NewKey("ddns"),
				Name:          taskLabel(label, sub, domain),
				Enable:        keepEnabled && lt.Enable && !lr.Disable,
				Provider:      provider,
				AccessID:      lt.DNS.ID,
				AccessKey:     lt.DNS.Secret,
				Domain:        domain,
				SubDomain:     sub,
				RecordType:    rtype,
				IPType:        ipType,
				IPSource:      ipSource,
				ManualIP:      "",
				CheckInterval: normalizeInterval(lt.Intervals),
				ForceInterval: lt.DNS.ForceInterval,
				TTL:           normalizeTTL(num(d, "ttl")),
			}
			if ipSource == "interface" {
				t.InterfaceName = manualIP
			}
			if provider == "callback" {
				u := lt.DNS.Callback.URL
				if u != "" && lt.DNS.Callback.Method != "" {
					u = lt.DNS.Callback.Method + " " + u
				}
				t.AccessKey = u
			}
			if lt.WebhookEnable && lt.WebhookURL != "" {
				u := lt.WebhookURL
				if lt.WebhookMethod != "" && lt.WebhookMethod != "GET" {
					u = lt.WebhookMethod + " " + u
				}
				t.WebhookURL = u
			}
			out = append(out, t)
		}

		// 旧版 Lucky 使用 Domains 列表
		if len(lt.Records) == 0 {
			rtype := "A"
			ipType := "ipv4"
			if strings.EqualFold(lt.TaskType, "IPv6") {
				rtype = "AAAA"
				ipType = "ipv6"
			}
			for _, full := range lt.Domains {
				full = strings.TrimSpace(full)
				if full == "" {
					continue
				}
				sub, zone := splitFQDN(full)
				out = append(out, config.DDNSTask{
					Key:           config.NewKey("ddns"),
					Name:          taskLabel(label, sub, zone),
					Enable:        keepEnabled && lt.Enable,
					Provider:      provider,
					AccessID:      lt.DNS.ID,
					AccessKey:     lt.DNS.Secret,
					Domain:        zone,
					SubDomain:     sub,
					RecordType:    rtype,
					IPType:        ipType,
					IPSource:      "api",
					CheckInterval: normalizeInterval(lt.Intervals),
					ForceInterval: lt.DNS.ForceInterval,
					TTL:           600,
				})
			}
		}
	}
	rep.Imported = len(out)
	return out, rep
}

// splitFQDN 把完整域名拆成主机记录与主域名（取最后两段为主域名）
func splitFQDN(full string) (sub, zone string) {
	parts := strings.Split(strings.Trim(full, "."), ".")
	if len(parts) <= 2 {
		return "", strings.Join(parts, ".")
	}
	return strings.Join(parts[:len(parts)-2], "."), strings.Join(parts[len(parts)-2:], ".")
}

func taskLabel(label, sub, domain string) string {
	if sub == "" || sub == "@" {
		return label + " · " + domain
	}
	return label + " · " + sub + "." + domain
}

func normalizeInterval(v int) int {
	if v < 10 {
		return 300
	}
	return v
}

func normalizeTTL(v int) int {
	if v <= 0 {
		return 600
	}
	if v < 60 {
		return 60
	}
	return v
}

// ImportCert 把 Lucky 的证书转换为本程序的证书任务，并把已签发的证书写入证书目录
func ImportCert(doc Document, certDir string, keepEnabled bool) ([]config.CertTask, *Report) {
	raw, ok := doc[keySSL]
	if !ok {
		return nil, nil
	}
	var src []luckyCert
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, &Report{Module: "cert", Title: "SSL 证书", Warnings: []string{"解析失败: " + err.Error()}}
	}
	rep := &Report{Module: "cert", Title: "SSL 证书", Found: len(src)}
	var out []config.CertTask

	for i, lc := range src {
		name := strings.TrimSpace(lc.Remark)
		if name == "" {
			name = fmt.Sprintf("第 %d 张证书", i+1)
		}
		certPEM := decodePEM(lc.CertBase64)
		keyPEM := decodePEM(lc.KeyBase64)
		domains := certDomains(certPEM)
		if len(domains) == 0 {
			rep.Warnings = append(rep.Warnings, name+": 无法从证书内容解析域名，已跳过")
			continue
		}
		task := config.CertTask{
			Key:         config.NewKey("cert"),
			Name:        name,
			Enable:      keepEnabled && lc.Enable,
			Domains:     domains,
			CAProvider:  "letsencrypt",
			KeyType:     "ec256",
			Challenge:   "http01",
			HTTPPort:    80,
			RenewBefore: 20,
		}
		if lc.AddFrom == "acme" && lc.ExtParams != nil {
			task.Email = str(lc.ExtParams, "acmeEmail")
			task.CAProvider = caFromURL(str(lc.ExtParams, "acmeCADirURL"))
			if kt := str(lc.ExtParams, "acmeKeyType"); strings.Contains(strings.ToLower(kt), "rsa") {
				task.KeyType = "rsa2048"
			}
			if strings.TrimSpace(str(lc.ExtParams, "acmeDNSServer")) != "" {
				rep.Warnings = append(rep.Warnings, name+": Lucky 使用 DNS-01 验证，本程序仅支持 HTTP-01，已改为 HTTP-01")
			}
		}
		if certPEM != "" && keyPEM != "" {
			dir := filepath.Join(certDir, safeName(name))
			if err := os.MkdirAll(dir, 0o755); err == nil {
				certPath := filepath.Join(dir, "fullchain.pem")
				keyPath := filepath.Join(dir, "privkey.pem")
				if err := os.WriteFile(certPath, []byte(certPEM), 0o644); err == nil {
					if err := os.WriteFile(keyPath, []byte(keyPEM), 0o600); err == nil {
						task.CertPath = certPath
						task.KeyPath = keyPath
						if exp, err := certExpire(certPEM); err == nil {
							task.ExpireTime = exp.Format("2006-01-02 15:04:05")
							task.Status = "已签发"
							task.LastIssue = time.Now().Format("2006-01-02 15:04:05")
						}
					}
				}
			}
		}
		if task.CertPath == "" {
			rep.Warnings = append(rep.Warnings, name+": 证书内容缺失，导入后需要重新申请")
			task.Status = "待申请"
		}
		if lc.MappingToPath && lc.MappingPath != "" {
			rep.Warnings = append(rep.Warnings, name+": Lucky 的证书映射目录 "+lc.MappingPath+" 需自行改为引用本程序证书路径")
		}
		out = append(out, task)
	}
	rep.Imported = len(out)
	return out, rep
}

// safeName 把任务名转换为安全的目录名
func safeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "cert"
	}
	return strings.NewReplacer("/", "_", "\\", "_", " ", "_", "*", "wildcard").Replace(name)
}

func certDomains(certPEM string) []string {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(n string) {
		n = strings.TrimPrefix(strings.TrimSpace(n), "*.")
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	if cert.Subject.CommonName != "" {
		add(cert.Subject.CommonName)
	}
	for _, d := range cert.DNSNames {
		add(d)
	}
	return out
}

func certExpire(certPEM string) (time.Time, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return time.Time{}, fmt.Errorf("证书格式不正确")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}

// ImportAll 解析 doc 并生成待导入的任务列表；apply 为真时写入配置存储。
// 已存在同名条目会被跳过，重复导入不会产生重复数据。
func ImportAll(doc Document, store *config.Store, keepEnabled, apply bool) (*Result, error) {
	res := &Result{Applied: apply}

	ddnsTasks, ddnsRep := ImportDDNS(doc, keepEnabled)
	if ddnsRep != nil {
		exist := map[string]bool{}
		for _, t := range store.Get().DDNSTasks {
			exist[t.Name] = true
		}
		kept := make([]config.DDNSTask, 0, len(ddnsTasks))
		for _, t := range ddnsTasks {
			if exist[t.Name] {
				ddnsRep.Warnings = append(ddnsRep.Warnings, Skipped(t.Name))
				continue
			}
			exist[t.Name] = true
			kept = append(kept, t)
		}
		ddnsTasks = kept
		ddnsRep.Imported = len(kept)
		res.Reports = append(res.Reports, *ddnsRep)
		res.Warnings = append(res.Warnings, ddnsRep.Warnings...)
	}

	certTasks, certRep := ImportCert(doc, store.CertDir(), keepEnabled)
	if certRep != nil {
		exist := map[string]bool{}
		for _, t := range store.Get().CertTasks {
			exist[t.Name] = true
		}
		kept := make([]config.CertTask, 0, len(certTasks))
		for _, t := range certTasks {
			if exist[t.Name] {
				certRep.Warnings = append(certRep.Warnings, Skipped(t.Name))
				continue
			}
			exist[t.Name] = true
			kept = append(kept, t)
		}
		certTasks = kept
		certRep.Imported = len(kept)
		res.Reports = append(res.Reports, *certRep)
		res.Warnings = append(res.Warnings, certRep.Warnings...)
	}
	res.DDNS = len(ddnsTasks)
	res.Cert = len(certTasks)

	if apply && (len(ddnsTasks) > 0 || len(certTasks) > 0) {
		store.Update(func(c *config.Config) {
			c.DDNSTasks = append(c.DDNSTasks, ddnsTasks...)
			c.CertTasks = append(c.CertTasks, certTasks...)
		})
		if len(ddnsTasks) > 0 {
			if err := store.SaveDDNS(); err != nil {
				return res, err
			}
		}
		if len(certTasks) > 0 {
			if err := store.SaveCert(); err != nil {
				return res, err
			}
		}
		logger.Info("lkcf", "已从 Lucky 配置导入 DDNS %d 条、证书 %d 张", len(ddnsTasks), len(certTasks))
	}
	return res, nil
}

// LoadDir 从目录中读取 Lucky 配置文件并合并为一个文档
func LoadDir(dir string) (Document, []string, error) {
	merged := Document{}
	loaded := []string{}
	names := []string{
		"lucky_ddns.lkcf", "lucky_ssl.lkcf", "lucky_base.lkcf",
		"lucky_portforward.lkcf", "lucky_reverseproxy.lkcf", "lucky_stun.lkcf",
	}
	for _, n := range names {
		path := filepath.Join(dir, n)
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		doc, err := Parse(b)
		if err != nil {
			logger.Warn("lkcf", "解析 %s 失败: %v", n, err)
			continue
		}
		for k, v := range doc {
			merged[k] = v
		}
		loaded = append(loaded, n)
	}
	if len(loaded) == 0 {
		return nil, nil, fmt.Errorf("目录 %s 中未找到可解析的 lucky_*.lkcf 文件", dir)
	}
	return merged, loaded, nil
}
