package webapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"porch/internal/cert"
	"porch/internal/config"
	"porch/internal/ddns"
	"porch/internal/reverseproxy"
)

var testPort int64 = 26300

func nextPort() int { return int(atomic.AddInt64(&testPort, 1)) }

type envelope struct {
	Ok   bool            `json:"ok"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type env struct {
	ts    *httptest.Server
	store *config.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatalf("初始化配置失败: %v", err)
	}
	dm := ddns.New(store)
	cm := cert.New(store)
	pm := reverseproxy.New(store)
	s := New(store, dm, cm, pm)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		dm.Stop()
		pm.Stop()
	})
	return &env{ts: ts, store: store}
}

func (e *env) do(t *testing.T, method, path, token string, body interface{}) (*http.Response, envelope) {
	t.Helper()
	var buf *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		buf = bytes.NewBuffer(b)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Token", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s %s 失败: %v", method, path, err)
	}
	var ev envelope
	json.NewDecoder(resp.Body).Decode(&ev)
	resp.Body.Close()
	return resp, ev
}

func (e *env) login(t *testing.T) string {
	t.Helper()
	resp, ev := e.do(t, http.MethodPost, "/api/login", "", map[string]string{"account": "666", "password": "666"})
	if resp.StatusCode != http.StatusOK || !ev.Ok {
		t.Fatalf("登录失败: %d %s", resp.StatusCode, ev.Msg)
	}
	var d struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(ev.Data, &d); err != nil {
		t.Fatal(err)
	}
	if d.Token == "" {
		t.Fatal("登录未返回 token")
	}
	return d.Token
}

func TestPingNoAuth(t *testing.T) {
	e := newEnv(t)
	resp, err := http.Get(e.ts.URL + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("免登录探活应成功，实际 %d", resp.StatusCode)
	}
	// handlePing 直接返回扁平结构（非 data 包装）
	var d map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d["ok"] != true {
		t.Errorf("探活 ok 应为 true，实际 %v", d["ok"])
	}
	if d["service"] != "porch" {
		t.Errorf("service 字段应为 porch，实际 %v", d["service"])
	}
	if _, ok := d["confDir"]; !ok {
		t.Error("探活响应缺少 confDir")
	}
}

func TestProtectedRoutesRequireToken(t *testing.T) {
	e := newEnv(t)
	for _, path := range []string{"/api/state", "/api/meta", "/api/metrics", "/api/ddns", "/api/cert", "/api/proxy", "/api/logs"} {
		resp, ev := e.do(t, http.MethodGet, path, "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s 未登录应返回 401，实际 %d", path, resp.StatusCode)
		}
		if ev.Ok {
			t.Errorf("%s 未登录不应返回 ok=true", path)
		}
	}
}

func TestLoginWrongPassword(t *testing.T) {
	e := newEnv(t)
	resp, ev := e.do(t, http.MethodPost, "/api/login", "", map[string]string{"account": "666", "password": "wrong"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误密码应 401，实际 %d", resp.StatusCode)
	}
	if !strings.Contains(ev.Msg, "账号或密码错误") {
		t.Errorf("错误提示不符: %q", ev.Msg)
	}
}

func TestLoginLockoutAfterRepeatedFailures(t *testing.T) {
	e := newEnv(t)
	for i := 1; i <= 10; i++ {
		resp, _ := e.do(t, http.MethodPost, "/api/login", "", map[string]string{"account": "666", "password": "bad"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败应 401，实际 %d", i, resp.StatusCode)
		}
	}
	resp, ev := e.do(t, http.MethodPost, "/api/login", "", map[string]string{"account": "666", "password": "bad"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("第 11 次应被锁定并返回 429，实际 %d", resp.StatusCode)
	}
	if !strings.Contains(ev.Msg, "次数过多") {
		t.Errorf("锁定提示不符: %q", ev.Msg)
	}
	// 即使密码正确也应处于锁定期
	resp, _ = e.do(t, http.MethodPost, "/api/login", "", map[string]string{"account": "666", "password": "666"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("锁定期内正确密码也应返回 429，实际 %d", resp.StatusCode)
	}
}

func TestLogoutInvalidatesToken(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)
	resp, _ := e.do(t, http.MethodPost, "/api/logout", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("退出登录失败: %d", resp.StatusCode)
	}
	resp, _ = e.do(t, http.MethodGet, "/api/state", tok, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("退出后 token 应失效，实际 %d", resp.StatusCode)
	}
}

func TestStateContainsAllModules(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)
	resp, ev := e.do(t, http.MethodGet, "/api/state", tok, nil)
	if resp.StatusCode != http.StatusOK || !ev.Ok {
		t.Fatalf("获取状态失败: %d %s", resp.StatusCode, ev.Msg)
	}
	var d struct {
		Base    config.BaseConfig         `json:"base"`
		DDNS    []config.DDNSTask         `json:"ddns"`
		Cert    []config.CertTask         `json:"cert"`
		Proxy   []config.ReverseProxyRule `json:"proxy"`
		System  map[string]interface{}    `json:"system"`
		Version string                    `json:"version"`
		ConfDir string                    `json:"confDir"`
	}
	if err := json.Unmarshal(ev.Data, &d); err != nil {
		t.Fatal(err)
	}
	if d.Base.AdminWebListenPort != 16601 {
		t.Errorf("默认端口不符: %d", d.Base.AdminWebListenPort)
	}
	if d.System == nil {
		t.Error("state 缺少 system 字段")
	}
	if d.Version == "" || d.ConfDir == "" {
		t.Error("state 缺少版本或配置目录")
	}
}

func TestDDNSCRUDAndToggle(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)

	create := map[string]interface{}{
		"name": "接口任务", "provider": "none", "domain": "example.com", "subDomain": "home",
		"recordType": "A", "ipType": "ipv4", "ipSource": "manual", "manualIP": "198.51.100.1",
		"enable": true, "checkInterval": 300, "ttl": 600,
	}
	resp, ev := e.do(t, http.MethodPost, "/api/ddns", tok, create)
	if resp.StatusCode != http.StatusOK || !ev.Ok {
		t.Fatalf("创建 DDNS 失败: %d %s", resp.StatusCode, ev.Msg)
	}
	var task config.DDNSTask
	json.Unmarshal(ev.Data, &task)
	if task.Key == "" {
		t.Fatal("创建后未返回 key")
	}

	// 列表应包含 1 条
	resp, ev = e.do(t, http.MethodGet, "/api/ddns", tok, nil)
	var list []config.DDNSTask
	json.Unmarshal(ev.Data, &list)
	if resp.StatusCode != http.StatusOK || len(list) != 1 {
		t.Fatalf("列表条数不符: %d", len(list))
	}

	// 更新名称
	create["name"] = "已改名"
	resp, _ = e.do(t, http.MethodPut, "/api/ddns/"+task.Key, tok, create)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("更新失败: %d", resp.StatusCode)
	}
	if got := e.store.Get().DDNSTasks[0].Name; got != "已改名" {
		t.Errorf("更新未生效: %q", got)
	}

	// 立即执行
	resp, ev = e.do(t, http.MethodPost, "/api/ddns/"+task.Key+"/run", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("执行失败: %d %s", resp.StatusCode, ev.Msg)
	}
	if ip := e.store.Get().DDNSTasks[0].CurrentIP; ip != "198.51.100.1" {
		t.Errorf("执行后 IP 不符: %q", ip)
	}

	// 一键停用
	resp, ev = e.do(t, http.MethodPost, "/api/ddns/"+task.Key+"/toggle", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("启停失败: %d", resp.StatusCode)
	}
	var tg map[string]bool
	json.Unmarshal(ev.Data, &tg)
	if tg["enable"] {
		t.Error("停用后 enable 应为 false")
	}
	if e.store.Get().DDNSTasks[0].Enable {
		t.Error("停用未写入配置")
	}

	// 删除
	resp, _ = e.do(t, http.MethodDelete, "/api/ddns/"+task.Key, tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除失败: %d", resp.StatusCode)
	}
	if n := len(e.store.Get().DDNSTasks); n != 0 {
		t.Errorf("删除后应剩 0 条，实际 %d", n)
	}
}

func TestDDNSToggleNotFound(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)
	resp, _ := e.do(t, http.MethodPost, "/api/ddns/not-exist/toggle", tok, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("不存在的任务应 404，实际 %d", resp.StatusCode)
	}
}

func TestProxyValidationAndToggle(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)

	// 缺少后端应被拒绝
	resp, _ := e.do(t, http.MethodPost, "/api/proxy", tok, map[string]interface{}{
		"name": "坏规则", "listenPort": 8080, "backends": []string{},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("缺少后端应 400，实际 %d", resp.StatusCode)
	}

	// 端口非法应被拒绝
	resp, _ = e.do(t, http.MethodPost, "/api/proxy", tok, map[string]interface{}{
		"name": "坏端口", "listenPort": 0, "backends": []string{"http://127.0.0.1:9"},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法端口应 400，实际 %d", resp.StatusCode)
	}

	port := nextPort()
	resp, ev := e.do(t, http.MethodPost, "/api/proxy", tok, map[string]interface{}{
		"name": "接口规则", "listenPort": port, "domain": "", "pathPrefix": "",
		"backends": []string{"http://127.0.0.1:9"}, "enable": false,
	})
	if resp.StatusCode != http.StatusOK || !ev.Ok {
		t.Fatalf("创建反代失败: %d %s", resp.StatusCode, ev.Msg)
	}
	var rule config.ReverseProxyRule
	json.Unmarshal(ev.Data, &rule)
	if rule.Key == "" {
		t.Fatal("创建后未返回 key")
	}

	// 启用（会尝试监听端口）
	resp, ev = e.do(t, http.MethodPost, "/api/proxy/"+rule.Key+"/toggle", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("启用反代失败: %d", resp.StatusCode)
	}
	var tg map[string]bool
	json.Unmarshal(ev.Data, &tg)
	if !tg["enable"] {
		t.Error("启用后 enable 应为 true")
	}

	// 删除
	resp, _ = e.do(t, http.MethodDelete, "/api/proxy/"+rule.Key, tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除反代失败: %d", resp.StatusCode)
	}
	if n := len(e.store.Get().ReverseProxies); n != 0 {
		t.Errorf("删除后应剩 0 条，实际 %d", n)
	}
}

func TestCertCRUDAndToggle(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)

	resp, ev := e.do(t, http.MethodPost, "/api/cert", tok, map[string]interface{}{
		"name": "接口证书", "domains": []string{"a.example.com"}, "email": "a@example.com",
		"caProvider": "letsencrypt", "keyType": "ec256", "httpPort": 80, "renewBefore": 20, "enable": true,
	})
	if resp.StatusCode != http.StatusOK || !ev.Ok {
		t.Fatalf("创建证书失败: %d %s", resp.StatusCode, ev.Msg)
	}
	var task config.CertTask
	json.Unmarshal(ev.Data, &task)
	if task.Key == "" {
		t.Fatal("创建后未返回 key")
	}

	// 停用（Reload 会跳过禁用任务，不会触网申请）
	resp, ev = e.do(t, http.MethodPost, "/api/cert/"+task.Key+"/toggle", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("停用证书失败: %d %s", resp.StatusCode, ev.Msg)
	}
	var tg map[string]bool
	json.Unmarshal(ev.Data, &tg)
	if tg["enable"] {
		t.Error("停用后 enable 应为 false")
	}

	resp, ev = e.do(t, http.MethodGet, "/api/cert", tok, nil)
	var list []config.CertTask
	json.Unmarshal(ev.Data, &list)
	if resp.StatusCode != http.StatusOK || len(list) != 1 {
		t.Fatalf("证书列表条数不符: %d", len(list))
	}

	resp, _ = e.do(t, http.MethodDelete, "/api/cert/"+task.Key, tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("删除证书失败: %d", resp.StatusCode)
	}
	if n := len(e.store.Get().CertTasks); n != 0 {
		t.Errorf("删除后应剩 0 条，实际 %d", n)
	}
}

func TestMetaProviders(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)
	resp, ev := e.do(t, http.MethodGet, "/api/meta", tok, nil)
	if resp.StatusCode != http.StatusOK || !ev.Ok {
		t.Fatalf("获取元数据失败: %d", resp.StatusCode)
	}
	var d struct {
		DDNSProviders []map[string]string `json:"ddnsProviders"`
		CAProviders   []map[string]string `json:"caProviders"`
	}
	json.Unmarshal(ev.Data, &d)
	if len(d.DDNSProviders) == 0 {
		t.Error("DDNS 服务商列表不应为空")
	}
	if len(d.CAProviders) == 0 {
		t.Error("CA 列表不应为空")
	}
}

func TestMetricsEndpoint(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)
	resp, ev := e.do(t, http.MethodGet, "/api/metrics", tok, nil)
	if resp.StatusCode != http.StatusOK || !ev.Ok {
		t.Fatalf("获取指标失败: %d %s", resp.StatusCode, ev.Msg)
	}
	var d struct {
		System struct {
			CPUAvailable bool                     `json:"cpuAvailable"`
			Goroutines   int                      `json:"goroutines"`
			History      []map[string]interface{} `json:"history"`
			UptimeSec    int64                    `json:"uptimeSec"`
		} `json:"system"`
		Certs    []map[string]interface{} `json:"certs"`
		Counters map[string]interface{}   `json:"counters"`
	}
	if err := json.Unmarshal(ev.Data, &d); err != nil {
		t.Fatal(err)
	}
	if d.System.Goroutines <= 0 {
		t.Error("Goroutine 数应大于 0")
	}
	if d.Counters == nil {
		t.Error("缺少 counters")
	}
	if _, ok := d.Counters["proxyRequests"]; !ok {
		t.Error("counters 缺少 proxyRequests")
	}
	if d.Certs == nil {
		t.Error("certs 不应为 nil")
	}
}

func TestExportConfig(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)
	e.store.Update(func(c *config.Config) {
		c.DDNSTasks = []config.DDNSTask{{Key: "k1", Name: "导出任务", Domain: "example.com"}}
	})

	req, _ := http.NewRequest(http.MethodGet, e.ts.URL+"/api/export", nil)
	req.Header.Set("X-Token", tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("导出应 200，实际 %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if !strings.Contains(body, `"ddnsTasks"`) || !strings.Contains(body, "导出任务") {
		t.Errorf("导出内容缺少配置数据: %s", body[:minInt(len(body), 200)])
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "porch-backup.json") {
		t.Errorf("下载文件名不正确: %q", cd)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestBaseSaveValidation(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)

	// 非法端口
	resp, _ := e.do(t, http.MethodPut, "/api/base", tok, map[string]interface{}{
		"adminWebListenPort": 0, "adminAccount": "666", "adminPassword": "666",
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("非法端口应 400，实际 %d", resp.StatusCode)
	}

	resp, ev := e.do(t, http.MethodPut, "/api/base", tok, map[string]interface{}{
		"adminWebListenPort": 18088, "adminAccount": "admin", "adminPassword": "pass",
		"sessionTimeout": 3600, "logLevel": "info", "autoStart": true,
	})
	if resp.StatusCode != http.StatusOK || !ev.Ok {
		t.Fatalf("保存基础配置失败: %d %s", resp.StatusCode, ev.Msg)
	}
	base := e.store.Get().Base
	if base.AdminWebListenPort != 18088 || base.AdminAccount != "admin" {
		t.Errorf("基础配置未生效: %+v", base)
	}
}

func TestImportEmptyDirFails(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)
	resp, ev := e.do(t, http.MethodPost, "/api/import/lucky", tok, map[string]interface{}{
		"dir": t.TempDir(), "apply": false,
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("空目录导入应 400，实际 %d", resp.StatusCode)
	}
	if !strings.Contains(ev.Msg, "未找到") {
		t.Errorf("错误提示不符: %q", ev.Msg)
	}
}

func TestReloadService(t *testing.T) {
	e := newEnv(t)
	tok := e.login(t)
	resp, ev := e.do(t, http.MethodPost, "/api/service/reload", tok, nil)
	if resp.StatusCode != http.StatusOK || !ev.Ok {
		t.Fatalf("重载服务失败: %d %s", resp.StatusCode, ev.Msg)
	}
}

func TestAccessGuardLogic(t *testing.T) {
	// isPrivateIP 是内网访问判定的核心，直接校验
	cases := map[string]bool{
		"127.0.0.1":   true,
		"192.168.1.5": true,
		"10.0.0.8":    true,
		"172.20.1.1":  true,
		"100.64.0.1":  true,
		"8.8.8.8":     false,
		"203.0.113.9": false,
	}
	for ip, want := range cases {
		if got := isPrivateIP(ip); got != want {
			t.Errorf("isPrivateIP(%q) = %v，期望 %v", ip, got, want)
		}
	}
}
