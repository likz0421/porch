// Package reverseproxy 实现按域名/路径匹配的 HTTP 反向代理，支持多后端轮询、TLS 与 gzip。
package reverseproxy

import (
	"compress/gzip"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"porch/internal/config"
	"porch/internal/logger"
)

// certCache 缓存已加载的证书对，按文件修改时间失效。
// 证书自动续期会重写文件内容，缓存失效后下次 TLS 握手即使用新证书，无需重启进程。
var certCache sync.Map // certPath -> *certEntry

type certEntry struct {
	mu   sync.Mutex
	cert *tls.Certificate
	mod  time.Time
	err  error
}

func loadCertPair(certFile, keyFile string) (*tls.Certificate, error) {
	st, err := os.Stat(certFile)
	if err != nil {
		return nil, err
	}
	mod := st.ModTime()

	v, _ := certCache.LoadOrStore(certFile, &certEntry{})
	entry := v.(*certEntry)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.cert != nil && entry.err == nil && entry.mod.Equal(mod) {
		return entry.cert, nil
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		entry.err = err
		return nil, err
	}
	entry.cert = &pair
	entry.mod = mod
	entry.err = nil
	logger.Info("reverseproxy", "已加载证书 %s（修改时间 %s）", certFile, mod.Format("2006-01-02 15:04:05"))
	return entry.cert, nil
}

// tlsConfig 按 SNI 选择匹配的证书，实现证书热更新
func (m *Manager) tlsConfig(rules []config.ReverseProxyRule) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			var chosen *config.ReverseProxyRule
			for i := range rules {
				r := &rules[i]
				if !r.EnableTLS || r.CertFile == "" || r.KeyFile == "" {
					continue
				}
				if r.Domain != "" && chi.ServerName != "" && strings.EqualFold(r.Domain, chi.ServerName) {
					chosen = r
					break
				}
				if chosen == nil {
					chosen = r
				}
			}
			if chosen == nil {
				return nil, fmt.Errorf("规则未配置证书")
			}
			return loadCertPair(chosen.CertFile, chosen.KeyFile)
		},
	}
}

// Manager 反向代理管理器
type Manager struct {
	mu      sync.Mutex
	store   *config.Store
	servers map[int]*http.Server
	counter map[string]*int64
	sig     map[int]string // 端口 -> 规则指纹，指纹不变时不重建监听
}

// New 创建反向代理管理器
func New(store *config.Store) *Manager {
	return &Manager{
		store:   store,
		servers: make(map[int]*http.Server),
		counter: make(map[string]*int64),
		sig:     make(map[int]string),
	}
}

// Reload 按最新配置重建监听
func (m *Manager) Reload() {
	cfg := m.store.Get()
	needed := make(map[int][]config.ReverseProxyRule)
	for _, r := range cfg.ReverseProxies {
		if !r.Enable {
			continue
		}
		needed[r.ListenPort] = append(needed[r.ListenPort], r)
	}

	m.mu.Lock()
	for port := range m.servers {
		if _, ok := needed[port]; !ok {
			m.stopLocked(port)
		}
	}
	for port, rules := range needed {
		// 规则指纹未变化则保持现有监听，避免断开已有连接
		fingerprint := rulesSignature(rules)
		if m.sig[port] == fingerprint {
			if _, ok := m.servers[port]; ok {
				continue
			}
		}
		m.stopLocked(port)
		m.sig[port] = fingerprint
		srv := &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			Handler:           m.handler(port),
			ReadHeaderTimeout: 30 * time.Second,
		}
		m.servers[port] = srv
		go servePort(m, port, srv, rules)
	}
	m.mu.Unlock()
}

func servePort(m *Manager, port int, srv *http.Server, rules []config.ReverseProxyRule) {
	tlsRule := config.ReverseProxyRule{}
	for _, r := range rules {
		if r.EnableTLS && r.CertFile != "" && r.KeyFile != "" {
			tlsRule = r
			break
		}
	}
	var err error
	if tlsRule.Key != "" {
		// 用 GetCertificate 动态加载：证书续期后新连接自动使用新证书
		srv.TLSConfig = m.tlsConfig(rules)
		err = srv.ListenAndServeTLS("", "")
	} else {
		err = srv.ListenAndServe()
	}
	if err != nil && err != http.ErrServerClosed {
		logger.Error("reverseproxy", "端口 %d 监听失败: %v", port, err)
		for _, r := range rules {
			key := r.Key
			msg := err.Error()
			m.store.SetStatus("reverseproxy", key, func(item interface{}) {
				if v, ok := item.(*config.ReverseProxyRule); ok {
					v.Status = "监听失败: " + msg
				}
			})
		}
		return
	}
	for _, r := range rules {
		logger.Info("reverseproxy", "规则[%s]已启动 :%d -> %v", r.Name, port, r.Backends)
		key := r.Key
		m.store.SetStatus("reverseproxy", key, func(item interface{}) {
			if v, ok := item.(*config.ReverseProxyRule); ok {
				v.Status = "运行中"
			}
		})
	}
}

// rulesSignature 生成规则指纹，用于判断是否需要重建监听
func rulesSignature(rules []config.ReverseProxyRule) string {
	var b strings.Builder
	for _, r := range rules {
		fmt.Fprintf(&b, "%s|%d|%s|%s|%v|%s|%s|%v|%v|%v;",
			r.Key, r.ListenPort, r.Domain, r.PathPrefix, r.Backends,
			r.CertFile, r.KeyFile, r.EnableTLS, r.EnableGzip, r.EnableXReal)
	}
	return b.String()
}

func (m *Manager) stopLocked(port int) {
	srv, ok := m.servers[port]
	if !ok {
		return
	}
	delete(m.servers, port)
	delete(m.sig, port)
	srv.Close()
	logger.Info("reverseproxy", "端口 %d 监听已关闭", port)
}

// Stop 停止全部监听
func (m *Manager) Stop() {
	m.mu.Lock()
	for port := range m.servers {
		m.stopLocked(port)
	}
	m.mu.Unlock()
}

func (m *Manager) handler(port int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		cfg := m.store.Get()
		var rule *config.ReverseProxyRule
		for i := range cfg.ReverseProxies {
			rr := cfg.ReverseProxies[i]
			if !rr.Enable || rr.ListenPort != port {
				continue
			}
			if rr.Domain != "" && !strings.EqualFold(rr.Domain, host) {
				continue
			}
			if rr.PathPrefix != "" && !strings.HasPrefix(r.URL.Path, rr.PathPrefix) {
				continue
			}
			rule = &cfg.ReverseProxies[i]
			break
		}
		if rule == nil || len(rule.Backends) == 0 {
			http.Error(w, "no matching reverse proxy rule", http.StatusBadGateway)
			return
		}

		target := pickBackend(m, rule)
		u, err := url.Parse(target)
		if err != nil {
			http.Error(w, "invalid backend: "+target, http.StatusBadGateway)
			return
		}
		key := rule.Key
		m.store.SetStatus("reverseproxy", key, func(item interface{}) {
			if v, ok := item.(*config.ReverseProxyRule); ok {
				v.RequestCount++
			}
		})

		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = u.Scheme
				req.URL.Host = u.Host
				req.Host = u.Host
				if rule.EnableXReal {
					if ip, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
						req.Header.Set("X-Real-IP", ip)
						req.Header.Set("X-Forwarded-For", ip)
					}
				}
				req.Header.Set("X-Forwarded-Host", r.Host)
				req.Header.Set("X-Forwarded-Proto", schemeOf(r, rule.EnableTLS))
			},
			ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
				logger.Error("reverseproxy", "规则[%s]代理失败: %v", rule.Name, err)
				http.Error(w, "bad gateway: "+err.Error(), http.StatusBadGateway)
			},
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
				MaxIdleConns:          64,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 2 * time.Second,
			},
		}

		if rule.EnableGzip && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			gw := &gzipWriter{ResponseWriter: w, raw: w}
			defer gw.Close()
			proxy.ServeHTTP(gw, r)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

func schemeOf(r *http.Request, tls bool) string {
	if tls || r.TLS != nil {
		return "https"
	}
	return "http"
}

func pickBackend(m *Manager, rule *config.ReverseProxyRule) string {
	if len(rule.Backends) == 1 {
		return normalize(rule.Backends[0])
	}
	m.mu.Lock()
	c, ok := m.counter[rule.Key]
	if !ok {
		var v int64
		c = &v
		m.counter[rule.Key] = c
	}
	idx := atomic.AddInt64(c, 1)
	m.mu.Unlock()
	return normalize(rule.Backends[int(idx)%len(rule.Backends)])
}

func normalize(addr string) string {
	addr = strings.TrimSpace(addr)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + strings.TrimRight(addr, "/")
}

type gzipWriter struct {
	http.ResponseWriter
	raw  http.ResponseWriter
	gz   *gzip.Writer
	once sync.Once
}

func (g *gzipWriter) WriteHeader(code int) {
	g.initgz()
	g.raw.WriteHeader(code)
}

func (g *gzipWriter) initgz() {
	g.once.Do(func() {
		g.raw.Header().Set("Content-Encoding", "gzip")
		g.raw.Header().Del("Content-Length")
		g.gz = gzip.NewWriter(g.raw)
	})
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	g.initgz()
	return g.gz.Write(b)
}

// Flush 立即输出已压缩的数据，流式响应依赖该方法
func (g *gzipWriter) Flush() {
	if g.gz != nil {
		g.gz.Flush()
	}
	if f, ok := g.raw.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipWriter) Close() {
	if g.gz != nil {
		g.gz.Close()
	}
}
