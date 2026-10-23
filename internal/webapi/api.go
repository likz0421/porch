// Package webapi 提供管理端 HTTP 接口：登录、配置读写、任务操作、日志流与静态资源。
package webapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"porch/internal/acme"
	"porch/internal/cert"
	"porch/internal/config"
	"porch/internal/ddns"
	"porch/internal/lkcf"
	"porch/internal/logger"
	"porch/internal/metrics"
	"porch/internal/netutil"
	"porch/internal/reverseproxy"
	"porch/internal/sysinfo"
	"porch/web"
)

// Server 管理端 HTTP 服务
type Server struct {
	store  *config.Store
	ddns   *ddns.Manager
	cert   *cert.Manager
	proxy  *reverseproxy.Manager
	sess   *sessionStore
	server *http.Server
}

type sessionStore struct {
	mu      sync.Mutex
	tokens  map[string]time.Time
	timeout time.Duration
	// 登录失败计数：防止对默认账号的暴力枚举
	failed  map[string]*failRecord
	maxFail int
	lockFor time.Duration
}

type failRecord struct {
	count int
	until time.Time
}

func newSessionStore(timeout time.Duration) *sessionStore {
	s := &sessionStore{
		tokens:  make(map[string]time.Time),
		timeout: timeout,
		failed:  make(map[string]*failRecord),
		maxFail: 10,
		lockFor: 10 * time.Minute,
	}
	go s.gc()
	return s
}

// locked 判断来源 IP 是否因连续失败被临时锁定，返回剩余秒数
func (s *sessionStore) locked(ip string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.failed[ip]
	if !ok {
		return 0
	}
	if r.until.After(time.Now()) {
		return int(time.Until(r.until).Seconds())
	}
	return 0
}

func (s *sessionStore) noteFailure(ip string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.failed[ip]
	if !ok {
		r = &failRecord{}
		s.failed[ip] = r
	}
	r.count++
	if r.count >= s.maxFail {
		// 保持 count 在上限，剩余次数显示为 0
		r.count = s.maxFail
		r.until = time.Now().Add(s.lockFor)
		logger.Warn("web", "来源 %s 连续登录失败已达上限，锁定 %d 分钟", ip, int(s.lockFor.Minutes()))
	}
	return r.count
}

func (s *sessionStore) clearFailure(ip string) {
	s.mu.Lock()
	delete(s.failed, ip)
	s.mu.Unlock()
}

func (s *sessionStore) gc() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for k, v := range s.tokens {
			if time.Since(v) > s.timeout {
				delete(s.tokens, k)
			}
		}
		for k, v := range s.failed {
			if v.until.Before(time.Now()) {
				delete(s.failed, k)
			}
		}
		s.mu.Unlock()
	}
}

func (s *sessionStore) new() string {
	b := make([]byte, 32)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[tok] = time.Now()
	s.mu.Unlock()
	return tok
}

func (s *sessionStore) valid(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[tok]
	if !ok {
		return false
	}
	if time.Since(t) > s.timeout {
		delete(s.tokens, tok)
		return false
	}
	s.tokens[tok] = time.Now()
	return true
}

func (s *sessionStore) drop(tok string) {
	s.mu.Lock()
	delete(s.tokens, tok)
	s.mu.Unlock()
}

// New 创建管理端服务
func New(store *config.Store, dm *ddns.Manager, cm *cert.Manager, pm *reverseproxy.Manager) *Server {
	timeout := time.Duration(store.Get().Base.SessionTimeout) * time.Second
	if timeout <= 0 {
		timeout = 2 * time.Hour
	}
	return &Server{store: store, ddns: dm, cert: cm, proxy: pm, sess: newSessionStore(timeout)}
}

// Handler 注册全部路由
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.auth(s.handleLogout))
	mux.HandleFunc("GET /api/ping", s.handlePing)

	mux.HandleFunc("GET /api/state", s.auth(s.handleState))
	mux.HandleFunc("GET /api/meta", s.auth(s.handleMeta))
	mux.HandleFunc("GET /api/system", s.auth(s.handleSystem))
	mux.HandleFunc("GET /api/metrics", s.auth(s.handleMetrics))
	mux.HandleFunc("GET /api/export", s.auth(s.handleExport))
	mux.HandleFunc("PUT /api/base", s.auth(s.handleBaseSave))
	mux.HandleFunc("POST /api/service/reload", s.auth(s.handleReload))
	mux.HandleFunc("POST /api/import/lucky", s.auth(s.handleImportLucky))

	mux.HandleFunc("GET /api/ddns", s.auth(s.handleDDNSList))
	mux.HandleFunc("POST /api/ddns", s.auth(s.handleDDNSCreate))
	mux.HandleFunc("PUT /api/ddns/{key}", s.auth(s.handleDDNSUpdate))
	mux.HandleFunc("DELETE /api/ddns/{key}", s.auth(s.handleDDNSDelete))
	mux.HandleFunc("POST /api/ddns/{key}/run", s.auth(s.handleDDNSRun))
	mux.HandleFunc("POST /api/ddns/{key}/toggle", s.auth(s.handleDDNSToggle))

	mux.HandleFunc("GET /api/cert", s.auth(s.handleCertList))
	mux.HandleFunc("POST /api/cert", s.auth(s.handleCertCreate))
	mux.HandleFunc("PUT /api/cert/{key}", s.auth(s.handleCertUpdate))
	mux.HandleFunc("DELETE /api/cert/{key}", s.auth(s.handleCertDelete))
	mux.HandleFunc("POST /api/cert/{key}/apply", s.auth(s.handleCertApply))
	mux.HandleFunc("POST /api/cert/{key}/toggle", s.auth(s.handleCertToggle))

	mux.HandleFunc("GET /api/proxy", s.auth(s.handleProxyList))
	mux.HandleFunc("POST /api/proxy", s.auth(s.handleProxyCreate))
	mux.HandleFunc("PUT /api/proxy/{key}", s.auth(s.handleProxyUpdate))
	mux.HandleFunc("DELETE /api/proxy/{key}", s.auth(s.handleProxyDelete))
	mux.HandleFunc("POST /api/proxy/{key}/toggle", s.auth(s.handleProxyToggle))

	mux.HandleFunc("GET /api/logs", s.auth(s.handleLogs))
	mux.HandleFunc("GET /api/logs/stream", s.auth(s.handleLogStream))

	mux.Handle("/", web.Handler())
	return mux
}

// Run 启动监听（阻塞）
func (s *Server) Run() error {
	base := s.store.Get().Base
	addr := fmt.Sprintf(":%d", base.AdminWebListenPort)
	s.server = &http.Server{
		Addr:              addr,
		Handler:           s.accessGuard(s.Handler()),
		ReadHeaderTimeout: 30 * time.Second,
	}
	if base.EnableTLS && base.TLSCertFile != "" && base.TLSKeyFile != "" {
		logger.Info("web", "管理页面已启动(HTTPS): https://%s", addr)
		return s.server.ListenAndServeTLS(base.TLSCertFile, base.TLSKeyFile)
	}
	logger.Info("web", "管理页面已启动: http://%s", addr)
	return s.server.ListenAndServe()
}

// Shutdown 平滑关闭
func (s *Server) Shutdown() error {
	if s.server == nil {
		return nil
	}
	return s.server.Close()
}

// accessGuard 当未开启"允许外网访问"时，拒绝来自公网地址的请求
func (s *Server) accessGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := s.store.Get().Base
		if base.AllowInternetAccess {
			next.ServeHTTP(w, r)
			return
		}
		ip := clientIP(r)
		if ip == "127.0.0.1" || ip == "::1" || isPrivateIP(ip) {
			next.ServeHTTP(w, r)
			return
		}
		logger.Warn("web", "拒绝来自公网地址 %s 的访问（未开启允许外网访问）", ip)
		http.Error(w, "当前未开启允许外网访问", http.StatusForbidden)
	})
}

func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	if v4 := parsed.To4(); v4 != nil {
		return v4[0] == 127 || // 整个 127.0.0.0/8 均为本机回环
			v4[0] == 10 ||
			(v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31) ||
			(v4[0] == 192 && v4[1] == 168) ||
			(v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127) ||
			(v4[0] == 169 && v4[1] == 254)
	}
	return parsed.IsLoopback() || parsed.IsLinkLocalUnicast() || parsed.IsPrivate()
}

// clientIP 只取真实连接的远端地址。
// 不信任 X-Forwarded-For：该头可被伪造，会让"仅允许内网访问"形同虚设。
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Token")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if !s.sess.valid(tok) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{"ok": false, "msg": "未登录或登录已过期"})
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func ok(w http.ResponseWriter, v interface{}) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": v})
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]interface{}{"ok": false, "msg": msg})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Account  string `json:"account"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	ip := clientIP(r)
	if left := s.sess.locked(ip); left > 0 {
		writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"ok": false, "msg": fmt.Sprintf("登录失败次数过多，请 %d 秒后再试", left)})
		return
	}
	base := s.store.Get().Base
	if req.Account != base.AdminAccount || req.Password != base.AdminPassword {
		left := s.sess.maxFail - s.sess.noteFailure(ip)
		logger.Warn("web", "登录失败，账号: %s 来源: %s", req.Account, ip)
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"ok": false, "msg": fmt.Sprintf("账号或密码错误（还可尝试 %d 次）", left)})
		return
	}
	s.sess.clearFailure(ip)
	tok := s.sess.new()
	logger.Info("web", "账号 %s 登录成功，来源 %s", req.Account, ip)
	ok(w, map[string]interface{}{"token": tok, "expiresIn": int(s.sess.timeout.Seconds())})
}

// handlePing 免登录探活接口，供 LUCI 与运维脚本判断服务是否存活
func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"service":   "porch",
		"version":   sysinfo.Version.Load(),
		"port":      cfg.Base.AdminWebListenPort,
		"ddns":      len(cfg.DDNSTasks),
		"cert":      len(cfg.CertTasks),
		"proxy":     len(cfg.ReverseProxies),
		"confDir":   s.store.Dir(),
		"uptime":    sysinfo.Collect().Uptime,
		"timestamp": time.Now().Format("2006-01-02 15:04:05"),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sess.drop(r.Header.Get("X-Token"))
	ok(w, "已退出")
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	ok(w, map[string]interface{}{
		"base":    cfg.Base,
		"ddns":    cfg.DDNSTasks,
		"cert":    cfg.CertTasks,
		"proxy":   cfg.ReverseProxies,
		"system":  sysinfo.Collect(),
		"version": sysinfo.Version.Load(),
		"confDir": s.store.Dir(),
	})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	ok(w, map[string]interface{}{
		"ddnsProviders": ddns.Providers(),
		"caProviders":   certCAProviders(),
		"interfaces":    netutil.Interfaces(),
	})
}

func certCAProviders() []map[string]string { return acme.CAProviders }

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	info := sysinfo.Collect()
	v4, err4 := netutil.GetPublicIP("ipv4")
	v6, err6 := netutil.GetPublicIP("ipv6")
	ok(w, map[string]interface{}{
		"info":       info,
		"publicIPv4": v4,
		"publicIPv6": v6,
		"ipv4Error":  errText(err4),
		"ipv6Error":  errText(err6),
	})
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// handleMetrics 返回实时监控数据：系统指标历史、证书到期倒计时、各模块计数。
// 面板概览页按 2 秒轮询该接口绘制曲线。
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()

	certs := make([]map[string]interface{}, 0, len(cfg.CertTasks))
	for _, c := range cfg.CertTasks {
		days := -1
		if c.ExpireTime != "" {
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", c.ExpireTime, time.Local); err == nil {
				days = int(time.Until(t).Hours() / 24)
				if days < 0 {
					days = 0
				}
			}
		}
		certs = append(certs, map[string]interface{}{
			"key":        c.Key,
			"name":       c.Name,
			"domains":    c.Domains,
			"status":     c.Status,
			"expireTime": c.ExpireTime,
			"daysLeft":   days,
			"enable":     c.Enable,
		})
	}

	var ddnsUpdated int
	for _, t := range cfg.DDNSTasks {
		ddnsUpdated += t.UpdateCount
	}
	var proxyReq int64
	for _, p := range cfg.ReverseProxies {
		proxyReq += p.RequestCount
	}

	ok(w, map[string]interface{}{
		"system": metrics.Current(),
		"certs":  certs,
		"counters": map[string]interface{}{
			"ddnsTasks":     len(cfg.DDNSTasks),
			"ddnsUpdated":   ddnsUpdated,
			"certTasks":     len(cfg.CertTasks),
			"proxyRules":    len(cfg.ReverseProxies),
			"proxyRequests": proxyReq,
		},
	})
}

// handleExport 导出全部配置（JSON），用于备份或迁移到另一台设备
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	cfg := s.store.Get()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="porch-backup.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(cfg)
}

func (s *Server) handleBaseSave(w http.ResponseWriter, r *http.Request) {
	var base config.BaseConfig
	if err := json.NewDecoder(r.Body).Decode(&base); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if base.AdminWebListenPort <= 0 || base.AdminWebListenPort > 65535 {
		fail(w, http.StatusBadRequest, "管理端口不合法")
		return
	}
	old := s.store.Get().Base
	s.store.Update(func(c *config.Config) { c.Base = base })
	if err := s.store.SaveBase(); err != nil {
		s.store.Update(func(c *config.Config) { c.Base = old })
		fail(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	logger.Info("web", "基础配置已更新(管理端口 %d，修改端口后需重启生效)", base.AdminWebListenPort)
	ok(w, "已保存")
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	s.ddns.Reload()
	s.proxy.Reload()
	s.cert.Reload()
	logger.Info("web", "已按最新配置重载全部模块")
	ok(w, "已重载")
}

// handleImportLucky 从 Lucky 的配置目录导入动态域名与证书任务
// 请求体: {"dir":"/etc/lucky/luckyconf", "apply":true, "keepEnabled":false}（指向 Lucky 导出的配置目录）
func (s *Server) handleImportLucky(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir         string `json:"dir"`
		Apply       bool   `json:"apply"`
		KeepEnabled bool   `json:"keepEnabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if req.Dir == "" {
		req.Dir = s.store.Dir()
	}
	doc, loaded, err := lkcf.LoadDir(req.Dir)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := lkcf.ImportAll(doc, s.store, req.KeepEnabled, req.Apply)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	logger.Info("web", "Lucky 配置导入(%v): %d 条 DDNS、%d 张证书", loaded, res.DDNS, res.Cert)
	if req.Apply {
		s.ddns.Reload()
		s.cert.Reload()
	}
	ok(w, map[string]interface{}{
		"files":  loaded,
		"result": res,
	})
}

// ---------- DDNS ----------

func findDDNS(store *config.Store, key string) (int, *config.DDNSTask) {
	cfg := store.Get()
	for i := range cfg.DDNSTasks {
		if cfg.DDNSTasks[i].Key == key {
			return i, &cfg.DDNSTasks[i]
		}
	}
	return -1, nil
}

func (s *Server) handleDDNSList(w http.ResponseWriter, r *http.Request) {
	ok(w, s.store.Get().DDNSTasks)
}

func (s *Server) handleDDNSCreate(w http.ResponseWriter, r *http.Request) {
	var t config.DDNSTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	t.Key = config.NewKey("ddns")
	fillDDNSDefaults(&t)
	s.store.Update(func(c *config.Config) { c.DDNSTasks = append(c.DDNSTasks, t) })
	if err := s.store.SaveDDNS(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.ddns.Reload()
	logger.Info("ddns", "新增任务[%s] %s.%s", t.Name, t.SubDomain, t.Domain)
	ok(w, t)
}

func fillDDNSDefaults(t *config.DDNSTask) {
	if t.Provider == "" {
		t.Provider = "cloudflare"
	}
	if t.RecordType == "" {
		t.RecordType = "A"
	}
	if t.IPType == "" {
		t.IPType = "ipv4"
	}
	if t.IPSource == "" {
		t.IPSource = "api"
	}
	if t.CheckInterval <= 0 {
		t.CheckInterval = 300
	}
	if t.TTL <= 0 {
		t.TTL = 600
	}
}

func (s *Server) handleDDNSUpdate(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var t config.DDNSTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	idx, _ := findDDNS(s.store, key)
	if idx < 0 {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	t.Key = key
	fillDDNSDefaults(&t)
	s.store.Update(func(c *config.Config) { c.DDNSTasks[idx] = t })
	if err := s.store.SaveDDNS(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.ddns.Reload()
	ok(w, t)
}

func (s *Server) handleDDNSDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	idx, _ := findDDNS(s.store, key)
	if idx < 0 {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	s.store.Update(func(c *config.Config) {
		c.DDNSTasks = append(c.DDNSTasks[:idx], c.DDNSTasks[idx+1:]...)
	})
	if err := s.store.SaveDDNS(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.ddns.Reload()
	ok(w, "已删除")
}

func (s *Server) handleDDNSRun(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	_, t := findDDNS(s.store, key)
	if t == nil {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	if err := s.ddns.RunOnce(*t); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, "已执行，请查看状态与日志")
}

// handleDDNSToggle 一键启用/停用任务
func (s *Server) handleDDNSToggle(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	idx, t := findDDNS(s.store, key)
	if idx < 0 || t == nil {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	enable := !t.Enable
	name := t.Name
	s.store.Update(func(c *config.Config) {
		c.DDNSTasks[idx].Enable = enable
		if !enable {
			c.DDNSTasks[idx].Status = "已禁用"
		}
	})
	if err := s.store.SaveDDNS(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.ddns.Reload()
	logger.Info("ddns", "任务[%s]已%s", name, enableWord(enable))
	ok(w, map[string]interface{}{"enable": enable})
}

func enableWord(v bool) string {
	if v {
		return "启用"
	}
	return "停用"
}

// ---------- 证书 ----------

func findCert(store *config.Store, key string) (int, *config.CertTask) {
	cfg := store.Get()
	for i := range cfg.CertTasks {
		if cfg.CertTasks[i].Key == key {
			return i, &cfg.CertTasks[i]
		}
	}
	return -1, nil
}

func (s *Server) handleCertList(w http.ResponseWriter, r *http.Request) {
	ok(w, s.store.Get().CertTasks)
}

func (s *Server) handleCertCreate(w http.ResponseWriter, r *http.Request) {
	var t config.CertTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	t.Key = config.NewKey("cert")
	fillCertDefaults(&t)
	s.store.Update(func(c *config.Config) { c.CertTasks = append(c.CertTasks, t) })
	if err := s.store.SaveCert(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, t)
}

func fillCertDefaults(t *config.CertTask) {
	if t.CAProvider == "" {
		t.CAProvider = "letsencrypt"
	}
	if t.KeyType == "" {
		t.KeyType = "ec256"
	}
	if t.Challenge == "" {
		t.Challenge = "http01"
	}
	if t.HTTPPort <= 0 {
		t.HTTPPort = 80
	}
	if t.RenewBefore <= 0 {
		t.RenewBefore = 20
	}
}

func (s *Server) handleCertUpdate(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var t config.CertTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	idx, _ := findCert(s.store, key)
	if idx < 0 {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	t.Key = key
	fillCertDefaults(&t)
	s.store.Update(func(c *config.Config) { c.CertTasks[idx] = t })
	if err := s.store.SaveCert(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, t)
}

func (s *Server) handleCertDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	idx, _ := findCert(s.store, key)
	if idx < 0 {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	s.store.Update(func(c *config.Config) {
		c.CertTasks = append(c.CertTasks[:idx], c.CertTasks[idx+1:]...)
	})
	if err := s.store.SaveCert(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	ok(w, "已删除")
}

func (s *Server) handleCertApply(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	_, t := findCert(s.store, key)
	if t == nil {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	if err := s.cert.Apply(key); err != nil {
		fail(w, http.StatusConflict, err.Error())
		return
	}
	ok(w, "已在后台开始申请，请查看日志")
}

// handleCertToggle 一键启用/停用证书任务
func (s *Server) handleCertToggle(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	idx, t := findCert(s.store, key)
	if idx < 0 || t == nil {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	enable := !t.Enable
	name := t.Name
	s.store.Update(func(c *config.Config) {
		c.CertTasks[idx].Enable = enable
		if !enable {
			c.CertTasks[idx].Status = "已禁用"
		}
	})
	if err := s.store.SaveCert(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.cert.Reload()
	logger.Info("cert", "任务[%s]已%s", name, enableWord(enable))
	ok(w, map[string]interface{}{"enable": enable})
}

// ---------- 反向代理 ----------

func findProxy(store *config.Store, key string) (int, *config.ReverseProxyRule) {
	cfg := store.Get()
	for i := range cfg.ReverseProxies {
		if cfg.ReverseProxies[i].Key == key {
			return i, &cfg.ReverseProxies[i]
		}
	}
	return -1, nil
}

func (s *Server) handleProxyList(w http.ResponseWriter, r *http.Request) {
	ok(w, s.store.Get().ReverseProxies)
}

func (s *Server) handleProxyCreate(w http.ResponseWriter, r *http.Request) {
	var t config.ReverseProxyRule
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	if t.ListenPort <= 0 || t.ListenPort > 65535 {
		fail(w, http.StatusBadRequest, "监听端口不合法")
		return
	}
	if len(t.Backends) == 0 {
		fail(w, http.StatusBadRequest, "至少需要一个后端地址")
		return
	}
	t.Key = config.NewKey("rp")
	s.store.Update(func(c *config.Config) { c.ReverseProxies = append(c.ReverseProxies, t) })
	if err := s.store.SaveReverseProxy(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.proxy.Reload()
	logger.Info("reverseproxy", "新增规则[%s] :%d", t.Name, t.ListenPort)
	ok(w, t)
}

func (s *Server) handleProxyUpdate(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var t config.ReverseProxyRule
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		fail(w, http.StatusBadRequest, "请求格式错误")
		return
	}
	idx, _ := findProxy(s.store, key)
	if idx < 0 {
		fail(w, http.StatusNotFound, "规则不存在")
		return
	}
	t.Key = key
	s.store.Update(func(c *config.Config) { c.ReverseProxies[idx] = t })
	if err := s.store.SaveReverseProxy(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.proxy.Reload()
	ok(w, t)
}

func (s *Server) handleProxyDelete(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	idx, _ := findProxy(s.store, key)
	if idx < 0 {
		fail(w, http.StatusNotFound, "规则不存在")
		return
	}
	s.store.Update(func(c *config.Config) {
		c.ReverseProxies = append(c.ReverseProxies[:idx], c.ReverseProxies[idx+1:]...)
	})
	if err := s.store.SaveReverseProxy(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.proxy.Reload()
	ok(w, "已删除")
}

// handleProxyToggle 一键启用/停用反代规则
func (s *Server) handleProxyToggle(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	idx, t := findProxy(s.store, key)
	if idx < 0 || t == nil {
		fail(w, http.StatusNotFound, "规则不存在")
		return
	}
	enable := !t.Enable
	name := t.Name
	s.store.Update(func(c *config.Config) {
		c.ReverseProxies[idx].Enable = enable
		if !enable {
			c.ReverseProxies[idx].Status = "已禁用"
		}
	})
	if err := s.store.SaveReverseProxy(); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.proxy.Reload()
	logger.Info("reverseproxy", "规则[%s]已%s", name, enableWord(enable))
	ok(w, map[string]interface{}{"enable": enable})
}

// ---------- 日志 ----------

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	since := uint64(0)
	fmt.Sscanf(r.URL.Query().Get("since"), "%d", &since)
	limit := 500
	ok(w, logger.Since(since, limit))
}

func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	flusher, okFlush := w.(http.Flusher)
	if !okFlush {
		fail(w, http.StatusInternalServerError, "当前连接不支持流式输出")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := logger.Subscribe(128)
	defer cancel()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case e, open := <-ch:
			if !open {
				return
			}
			raw, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", raw)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// 供 LUCI 使用的轻量探活接口（无需登录）
func (s *Server) PingHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":      true,
			"service": "porch",
			"version": sysinfo.Version.Load(),
			"pid":     os.Getpid(),
			"uptime":  sysinfo.Collect().Uptime,
		})
	})
}

// ServeStatic 返回内嵌前端资源的处理器（供外部挂载）
func ServeStatic() http.Handler { return web.Handler() }
