// Package ddns 实现动态域名解析：多服务商记录更新 + 定时巡检任务。
package ddns

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"porch/internal/config"
)

// Provider DNS 服务商接口
type Provider interface {
	// Name 返回服务商标识
	Name() string
	// Update 将记录更新为指定 IP
	Update(task config.DDNSTask, ip string) error
}

var client = &http.Client{Timeout: 15 * time.Second}

// Get 返回指定名称的服务商实现
func Get(name string) (Provider, error) {
	switch strings.ToLower(name) {
	case "cloudflare", "cf":
		return cloudflare{}, nil
	case "alidns", "aliyun", "ali":
		return alidns{}, nil
	case "dnspod":
		return dnspod{}, nil
	case "godaddy":
		return godaddy{}, nil
	case "porkbun":
		return porkbun{}, nil
	case "duckdns":
		return duckdns{}, nil
	case "callback", "webhook", "url":
		return callback{}, nil
	case "none", "debug":
		return noneProvider{}, nil
	}
	return nil, fmt.Errorf("不支持的服务商: %s", name)
}

// Providers 返回支持的服务商列表（供前端下拉使用）
func Providers() []map[string]string {
	return []map[string]string{
		{"value": "cloudflare", "label": "Cloudflare", "idLabel": "API Token", "keyLabel": "API Token", "idRequired": "false"},
		{"value": "alidns", "label": "阿里云 DNS", "idLabel": "AccessKey ID", "keyLabel": "AccessKey Secret", "idRequired": "true"},
		{"value": "dnspod", "label": "DNSPod 腾讯云", "idLabel": "ID", "keyLabel": "Token", "idRequired": "true"},
		{"value": "godaddy", "label": "GoDaddy", "idLabel": "API Key", "keyLabel": "API Secret", "idRequired": "true"},
		{"value": "porkbun", "label": "Porkbun", "idLabel": "API Key", "keyLabel": "Secret API Key", "idRequired": "true"},
		{"value": "duckdns", "label": "DuckDNS", "idLabel": "", "keyLabel": "Token", "idRequired": "false"},
		{"value": "callback", "label": "自定义回调 URL", "idLabel": "用户名(可选)", "keyLabel": "密码(可选)", "idRequired": "false"},
		{"value": "none", "label": "空操作(仅记录日志)", "idLabel": "", "keyLabel": "", "idRequired": "false"},
	}
}

func fqdn(task config.DDNSTask) string {
	domain := strings.Trim(task.Domain, ".")
	sub := strings.Trim(task.SubDomain, ".")
	if sub == "" || sub == "@" {
		return domain
	}
	return sub + "." + domain
}

func recordType(ipType string) string {
	if strings.EqualFold(ipType, "ipv6") {
		return "AAAA"
	}
	return "A"
}

// ---------------- Cloudflare ----------------

type cloudflare struct{}

func (cloudflare) Name() string { return "cloudflare" }

type cfResp struct {
	Success bool `json:"success"`
	Errors  []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

func (c cloudflare) req(method, urlStr, token string, body io.Reader) (*cfResp, error) {
	r, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	out := &cfResp{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("Cloudflare 返回解析失败: %s", string(raw))
	}
	if !out.Success {
		msg := ""
		for _, e := range out.Errors {
			msg += e.Message + "; "
		}
		return out, fmt.Errorf("Cloudflare 返回错误: %s", msg)
	}
	return out, nil
}

func (c cloudflare) Update(task config.DDNSTask, ip string) error {
	token := task.AccessKey
	if token == "" {
		token = task.AccessID
	}
	zoneResp, err := c.req("GET", "https://api.cloudflare.com/client/v4/zones?name="+url.QueryEscape(task.Domain), token, nil)
	if err != nil {
		return err
	}
	var zones []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(zoneResp.Result, &zones); err != nil || len(zones) == 0 {
		return fmt.Errorf("未找到域名 %s 对应的 zone", task.Domain)
	}
	zoneID := zones[0].ID

	name := fqdn(task)
	rtype := recordType(task.IPType)
	listResp, err := c.req("GET",
		fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?type=%s&name=%s", zoneID, rtype, url.QueryEscape(name)),
		token, nil)
	if err != nil {
		return err
	}
	var records []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	_ = json.Unmarshal(listResp.Result, &records)

	ttl := task.TTL
	if ttl <= 0 {
		ttl = 600
	}
	if ttl < 60 {
		ttl = 60
	}
	payload := fmt.Sprintf(`{"type":"%s","name":"%s","content":"%s","ttl":%d,"proxied":false}`, rtype, name, ip, ttl)

	if len(records) > 0 {
		if records[0].Content == ip {
			return nil // 无需更新
		}
		_, err = c.req("PATCH", fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", zoneID, records[0].ID),
			token, strings.NewReader(payload))
		return err
	}
	_, err = c.req("POST", fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", zoneID),
		token, strings.NewReader(payload))
	return err
}

// ---------------- 阿里云 DNS ----------------

type alidns struct{}

func (alidns) Name() string { return "alidns" }

type aliRecord struct {
	RecordID string `json:"RecordId"`
	Value    string `json:"Value"`
	Type     string `json:"Type"`
	RR       string `json:"RR"`
}

type aliListResp struct {
	TotalCount    int `json:"TotalCount"`
	DomainRecords struct {
		Record []aliRecord `json:"Record"`
	} `json:"DomainRecords"`
	Code    string `json:"Code"`
	Message string `json:"Message"`
}

func (a alidns) sign(params map[string]string, secret string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString("&")
		}
		b.WriteString(aliEncode(k))
		b.WriteString("=")
		b.WriteString(aliEncode(params[k]))
	}
	canonical := b.String()
	stringToSign := "GET&" + aliEncode("/") + "&" + aliEncode(canonical)
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func aliEncode(s string) string {
	s = url.QueryEscape(s)
	s = strings.ReplaceAll(s, "+", "%20")
	s = strings.ReplaceAll(s, "*", "%2A")
	s = strings.ReplaceAll(s, "%7E", "~")
	return s
}

func (a alidns) call(params map[string]string, id, secret string) ([]byte, error) {
	base := map[string]string{
		"Format":           "JSON",
		"Version":          "2015-01-09",
		"AccessKeyId":      id,
		"SignatureMethod":  "HMAC-SHA1",
		"Timestamp":        time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"SignatureVersion": "1.0",
		"SignatureNonce":   fmt.Sprintf("%d", time.Now().UnixNano()),
	}
	for k, v := range params {
		base[k] = v
	}
	base["Signature"] = a.sign(base, secret)

	keys := make([]string, 0, len(base))
	for k := range base {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	q := url.Values{}
	for _, k := range keys {
		q.Set(k, base[k])
	}
	resp, err := client.Get("https://alidns.aliyuncs.com/?" + q.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func (a alidns) Update(task config.DDNSTask, ip string) error {
	rr := strings.Trim(task.SubDomain, ".")
	if rr == "" || rr == "@" {
		rr = "@"
	}
	rtype := recordType(task.IPType)

	raw, err := a.call(map[string]string{
		"Action":      "DescribeDomainRecords",
		"DomainName":  task.Domain,
		"RRKeyWord":   rr,
		"TypeKeyWord": rtype,
	}, task.AccessID, task.AccessKey)
	if err != nil {
		return err
	}
	list := aliListResp{}
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("阿里云返回解析失败: %s", string(raw))
	}
	if list.Code != "" && list.Code != "200" {
		return fmt.Errorf("阿里云返回错误: %s %s", list.Code, list.Message)
	}

	var exist *aliRecord
	for i := range list.DomainRecords.Record {
		r := list.DomainRecords.Record[i]
		if r.RR == rr && r.Type == rtype {
			exist = &list.DomainRecords.Record[i]
			break
		}
	}
	ttl := task.TTL
	if ttl <= 0 {
		ttl = 600
	}
	if exist != nil {
		if exist.Value == ip {
			return nil
		}
		_, err = a.call(map[string]string{
			"Action":   "UpdateDomainRecord",
			"RecordId": exist.RecordID,
			"RR":       rr,
			"Type":     rtype,
			"Value":    ip,
			"TTL":      fmt.Sprintf("%d", ttl),
		}, task.AccessID, task.AccessKey)
		return err
	}
	_, err = a.call(map[string]string{
		"Action":     "AddDomainRecord",
		"DomainName": task.Domain,
		"RR":         rr,
		"Type":       rtype,
		"Value":      ip,
		"TTL":        fmt.Sprintf("%d", ttl),
	}, task.AccessID, task.AccessKey)
	return err
}

// ---------------- DNSPod ----------------

type dnspod struct{}

func (dnspod) Name() string { return "dnspod" }

type dpResp struct {
	Status struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"status"`
	Records []struct {
		ID    string `json:"id"`
		Value string `json:"value"`
		Name  string `json:"name"`
		Type  string `json:"type"`
	} `json:"records"`
	Record struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	} `json:"record"`
}

func (d dnspod) post(api string, data url.Values) (*dpResp, error) {
	resp, err := client.PostForm("https://dnsapi.cn/"+api, data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	out := &dpResp{}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, fmt.Errorf("DNSPod 返回解析失败: %s", string(raw))
	}
	if out.Status.Code != "1" && out.Status.Code != "" {
		return out, fmt.Errorf("DNSPod 返回错误: %s", out.Status.Message)
	}
	return out, nil
}

func (d dnspod) Update(task config.DDNSTask, ip string) error {
	token := task.AccessID
	if task.AccessKey != "" {
		token = task.AccessID + "," + task.AccessKey
	}
	sub := strings.Trim(task.SubDomain, ".")
	if sub == "" {
		sub = "@"
	}
	rtype := recordType(task.IPType)

	v := url.Values{}
	v.Set("login_token", token)
	v.Set("format", "json")
	v.Set("domain", task.Domain)
	v.Set("sub_domain", sub)
	v.Set("record_type", rtype)
	list, err := d.post("Record.List", v)
	if err != nil {
		return err
	}
	ttl := task.TTL
	if ttl <= 0 {
		ttl = 600
	}
	v2 := url.Values{}
	v2.Set("login_token", token)
	v2.Set("format", "json")
	v2.Set("domain", task.Domain)
	v2.Set("record_type", rtype)
	v2.Set("record_line", "默认")
	v2.Set("value", ip)
	v2.Set("ttl", fmt.Sprintf("%d", ttl))
	v2.Set("sub_domain", sub)

	if len(list.Records) > 0 {
		if list.Records[0].Value == ip {
			return nil
		}
		v2.Set("record_id", list.Records[0].ID)
		_, err = d.post("Record.Modify", v2)
		return err
	}
	_, err = d.post("Record.Create", v2)
	return err
}

// ---------------- GoDaddy ----------------

type godaddy struct{}

func (godaddy) Name() string { return "godaddy" }

func (g godaddy) Update(task config.DDNSTask, ip string) error {
	name := strings.Trim(task.SubDomain, ".")
	if name == "" || name == "@" {
		name = "@"
	}
	api := fmt.Sprintf("https://api.godaddy.com/v1/domains/%s/records/%s/%s",
		url.PathEscape(task.Domain), recordType(task.IPType), url.PathEscape(name))
	ttl := task.TTL
	if ttl <= 0 {
		ttl = 600
	}
	body := fmt.Sprintf(`[{"data":"%s","ttl":%d}]`, ip, ttl)
	r, err := http.NewRequest("PUT", api, strings.NewReader(body))
	if err != nil {
		return err
	}
	r.Header.Set("Authorization", "sso-key "+task.AccessID+":"+task.AccessKey)
	r.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GoDaddy 返回 %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// ---------------- Porkbun ----------------

type porkbun struct{}

func (porkbun) Name() string { return "porkbun" }

func (p porkbun) Update(task config.DDNSTask, ip string) error {
	sub := strings.Trim(task.SubDomain, ".")
	if sub == "" || sub == "@" {
		sub = ""
	}
	api := fmt.Sprintf("https://porkbun.com/api/json/v3/dns/editByNameType/%s/%s/%s",
		url.PathEscape(task.Domain), recordType(task.IPType), url.PathEscape(sub))
	ttl := task.TTL
	if ttl <= 0 {
		ttl = 600
	}
	if ttl < 60 {
		ttl = 60
	}
	body := fmt.Sprintf(`{"apikey":%q,"secretapikey":%q,"content":%q,"ttl":"%d"}`,
		task.AccessID, task.AccessKey, ip, ttl)
	resp, err := client.Post(api, "application/json", strings.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var out struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.Status != "SUCCESS" {
		return fmt.Errorf("Porkbun 返回: %s %s", out.Status, out.Message)
	}
	return nil
}

// ---------------- DuckDNS ----------------

type duckdns struct{}

func (duckdns) Name() string { return "duckdns" }

func (d duckdns) Update(task config.DDNSTask, ip string) error {
	u := fmt.Sprintf("https://www.duckdns.org/update?domains=%s&token=%s&ip=%s",
		url.QueryEscape(strings.Trim(task.SubDomain, ".")), url.QueryEscape(task.AccessKey), url.QueryEscape(ip))
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
	if strings.TrimSpace(string(raw)) != "OK" {
		return fmt.Errorf("DuckDNS 返回: %s", strings.TrimSpace(string(raw)))
	}
	return nil
}

// ---------------- 自定义回调 ----------------

type callback struct{}

func (callback) Name() string { return "callback" }

func (c callback) Update(task config.DDNSTask, ip string) error {
	tpl := task.AccessKey
	if tpl == "" {
		tpl = task.AccessID
	}
	if tpl == "" {
		return fmt.Errorf("回调地址为空")
	}
	replacer := strings.NewReplacer(
		"#{ip}", ip,
		"#{IP}", ip,
		"#{domain}", task.Domain,
		"#{subDomain}", task.SubDomain,
		"#{recordType}", recordType(task.IPType),
	)
	u := replacer.Replace(tpl)
	method := "GET"
	if strings.Contains(u, " ") {
		parts := strings.SplitN(u, " ", 2)
		method = strings.ToUpper(parts[0])
		u = parts[1]
	}
	r, err := http.NewRequest(method, u, nil)
	if err != nil {
		return err
	}
	if task.AccessID != "" && task.AccessKey != "" {
		r.SetBasicAuth(task.AccessID, task.AccessKey)
	}
	resp, err := client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("回调返回状态码 %d", resp.StatusCode)
	}
	return nil
}

// ---------------- 空操作 ----------------

type noneProvider struct{}

func (noneProvider) Name() string { return "none" }

func (noneProvider) Update(task config.DDNSTask, ip string) error { return nil }
