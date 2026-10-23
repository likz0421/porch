package reverseproxy

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"porch/internal/config"
)

// TestMain 避免本机 HTTP_PROXY 影响回环地址的代理测试
func TestMain(m *testing.M) {
	os.Setenv("NO_PROXY", "127.0.0.1,localhost")
	os.Setenv("no_proxy", "127.0.0.1,localhost")
	os.Exit(m.Run())
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"192.168.1.10:5000":        "http://192.168.1.10:5000",
		"http://10.0.0.2:8080":     "http://10.0.0.2:8080",
		"https://a.example.com/":   "https://a.example.com",
		"  http://b.example.com  ": "http://b.example.com",
	}
	for in, want := range cases {
		if got := normalize(in); got != want {
			t.Errorf("normalize(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestRulesSignatureChanges(t *testing.T) {
	base := config.ReverseProxyRule{Key: "k", ListenPort: 8080, Domain: "a.com", Backends: []string{"http://1.1.1.1:80"}, EnableGzip: true}
	other := base
	other.Domain = "b.com"
	if rulesSignature([]config.ReverseProxyRule{base}) == rulesSignature([]config.ReverseProxyRule{other}) {
		t.Error("域名变化应导致指纹不同")
	}
	same := base
	if rulesSignature([]config.ReverseProxyRule{base}) != rulesSignature([]config.ReverseProxyRule{same}) {
		t.Error("规则未变化时指纹应一致")
	}
}

func TestPickBackendRoundRobin(t *testing.T) {
	m := &Manager{counter: make(map[string]*int64), servers: make(map[int]*http.Server), sig: make(map[int]string)}
	rule := &config.ReverseProxyRule{Key: "r1", Backends: []string{"http://a:1", "http://b:2"}}
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		seen[pickBackend(m, rule)]++
	}
	if seen["http://a:1"] != 3 || seen["http://b:2"] != 3 {
		t.Errorf("轮询分配不均: %+v", seen)
	}
}

func TestPickBackendSingle(t *testing.T) {
	m := &Manager{counter: make(map[string]*int64)}
	rule := &config.ReverseProxyRule{Key: "r2", Backends: []string{"10.0.0.9:8080"}}
	if got := pickBackend(m, rule); got != "http://10.0.0.9:8080" {
		t.Errorf("单后端应原样返回并补全 scheme，实际 %q", got)
	}
}

func TestHandlerRoutesToBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Got-Real-IP", r.Header.Get("X-Real-IP"))
		w.Header().Set("X-Got-Proto", r.Header.Get("X-Forwarded-Proto"))
		io.WriteString(w, "backend-ok:"+r.URL.Path)
	}))
	defer backend.Close()

	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) {
		c.ReverseProxies = []config.ReverseProxyRule{{
			Key: "rp-test", Enable: true, Name: "测试", ListenPort: 18099,
			Domain: "nas.example.com", Backends: []string{backend.URL}, EnableXReal: true,
		}}
	})
	m := New(store)
	h := m.handler(18099)

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	req.Host = "nas.example.com:18099"
	req.RemoteAddr = "203.0.113.9:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码应为 200，实际 %d", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "backend-ok:/hello") {
		t.Errorf("未转发到后端，响应: %q", body)
	}
	if got := rec.Header().Get("X-Got-Real-IP"); got != "203.0.113.9" {
		t.Errorf("X-Real-IP 透传错误: %q", got)
	}
	if got := rec.Header().Get("X-Got-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto 错误: %q", got)
	}
}

func TestHandlerNoMatchingRule(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) {
		c.ReverseProxies = []config.ReverseProxyRule{{
			Key: "rp", Enable: true, ListenPort: 18100, Domain: "only.example.com",
			Backends: []string{"http://127.0.0.1:1"},
		}}
	})
	m := New(store)
	h := m.handler(18100)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "other.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("无匹配规则应返回 502，实际 %d", rec.Code)
	}
}

func TestHandlerPathPrefix(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer backend.Close()

	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) {
		c.ReverseProxies = []config.ReverseProxyRule{{
			Key: "rp", Enable: true, ListenPort: 18101, PathPrefix: "/api",
			Backends: []string{backend.URL},
		}}
	})
	m := New(store)
	h := m.handler(18101)

	hit := httptest.NewRecorder()
	h.ServeHTTP(hit, httptest.NewRequest(http.MethodGet, "/api/users", nil))
	if hit.Code != http.StatusOK {
		t.Errorf("命中前缀应 200，实际 %d", hit.Code)
	}
	miss := httptest.NewRecorder()
	h.ServeHTTP(miss, httptest.NewRequest(http.MethodGet, "/other", nil))
	if miss.Code != http.StatusBadGateway {
		t.Errorf("未命中前缀应 502，实际 %d", miss.Code)
	}
}

func TestTLSConfigWithoutCert(t *testing.T) {
	m := &Manager{}
	cfg := m.tlsConfig([]config.ReverseProxyRule{{Key: "r", EnableTLS: false}})
	if cfg == nil {
		t.Fatal("tlsConfig 不应返回 nil")
	}
	if _, err := cfg.GetCertificate(&tls.ClientHelloInfo{}); err == nil {
		t.Error("未配置证书时应返回错误")
	}
}
