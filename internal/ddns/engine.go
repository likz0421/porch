package ddns

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"porch/internal/config"
	"porch/internal/logger"
	"porch/internal/netutil"
)

// Manager DDNS 任务管理器
type Manager struct {
	mu      sync.Mutex
	store   *config.Store
	runners map[string]*runner
}

type runner struct {
	cfg     config.DDNSTask
	stopCh  chan struct{}
	forceCh chan struct{}
	wg      sync.WaitGroup
}

// New 创建 DDNS 管理器
func New(store *config.Store) *Manager {
	return &Manager{store: store, runners: make(map[string]*runner)}
}

// Reload 按最新配置启停任务
func (m *Manager) Reload() {
	cfg := m.store.Get()
	alive := make(map[string]bool, len(cfg.DDNSTasks))

	m.mu.Lock()
	for _, t := range cfg.DDNSTasks {
		if !t.Enable {
			continue
		}
		old, exist := m.runners[t.Key]
		if exist && sameTask(old.cfg, t) {
			alive[t.Key] = true
			continue
		}
		if exist {
			m.stopLocked(t.Key)
		}
		r := &runner{cfg: t, stopCh: make(chan struct{}), forceCh: make(chan struct{}, 1)}
		m.runners[t.Key] = r
		alive[t.Key] = true
		r.wg.Add(1)
		go r.loop(m.store)
		logger.Info("ddns", "任务[%s]已启动，检查间隔 %d 秒", t.Name, t.CheckInterval)
	}
	for key := range m.runners {
		if !alive[key] {
			m.stopLocked(key)
		}
	}
	m.mu.Unlock()
}

func sameTask(a, b config.DDNSTask) bool {
	return a.Enable == b.Enable && a.Provider == b.Provider && a.Domain == b.Domain &&
		a.SubDomain == b.SubDomain && a.IPType == b.IPType && a.IPSource == b.IPSource &&
		a.InterfaceName == b.InterfaceName && a.CheckInterval == b.CheckInterval
}

func (m *Manager) stopLocked(key string) {
	r, ok := m.runners[key]
	if !ok {
		return
	}
	delete(m.runners, key)
	close(r.stopCh)
	r.wg.Wait()
	logger.Info("ddns", "任务[%s]已停止", r.cfg.Name)
}

// Stop 停止全部任务
func (m *Manager) Stop() {
	m.mu.Lock()
	for key := range m.runners {
		m.stopLocked(key)
	}
	m.mu.Unlock()
}

// Run 立即执行一次指定任务
func (m *Manager) Run(key string) error {
	m.mu.Lock()
	r, ok := m.runners[key]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("任务未运行: %s", key)
	}
	select {
	case r.forceCh <- struct{}{}:
	default:
	}
	return nil
}

// RunOnce 不依赖运行状态，直接按配置执行一次（供未启用的任务手动测试）
func (m *Manager) RunOnce(task config.DDNSTask) error {
	return execute(m.store, task)
}

func (r *runner) loop(store *config.Store) {
	defer r.wg.Done()
	interval := time.Duration(r.cfg.CheckInterval) * time.Second
	if interval < 10*time.Second {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	execute(store, r.cfg)
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			// 每次触发时重新对齐间隔，配置中修改后无需重启进程
			if fresh, ok := findTask(store, r.cfg.Key); ok {
				r.cfg = fresh
				if d := time.Duration(fresh.CheckInterval) * time.Second; d >= 10*time.Second && d != interval {
					interval = d
					ticker.Reset(interval)
				}
			}
			execute(store, r.cfg)
		case <-r.forceCh:
			execute(store, r.cfg)
		}
	}
}

// findTask 按 key 取配置存储中的最新任务
func findTask(store *config.Store, key string) (config.DDNSTask, bool) {
	for _, t := range store.Get().DDNSTasks {
		if t.Key == key {
			return t, true
		}
	}
	return config.DDNSTask{}, false
}

func execute(store *config.Store, task config.DDNSTask) error {
	// 始终以配置存储中的最新内容执行：修改服务商凭据、域名、Webhook 后无需重启进程
	if fresh, ok := findTask(store, task.Key); ok {
		task = fresh
	}
	ip, err := resolveIP(task)
	if err != nil {
		logger.Error("ddns", "任务[%s]获取 IP 失败: %v", task.Name, err)
		setStatus(store, task.Key, "获取IP失败", "", "", false)
		return err
	}
	if ip == "" {
		err := fmt.Errorf("未获取到有效 IP")
		logger.Error("ddns", "任务[%s] %v", task.Name, err)
		setStatus(store, task.Key, "未获取到IP", "", "", false)
		return err
	}

	// 与上次成功更新的 IP 相同时跳过，减少 API 调用；但超过强制更新间隔时仍会刷新一次
	var last string
	var lastUpdate string
	cfg := store.Get()
	for _, t := range cfg.DDNSTasks {
		if t.Key == task.Key {
			last = t.CurrentIP
			lastUpdate = t.LastUpdate
			break
		}
	}
	force := false
	if task.ForceInterval > 0 && lastUpdate != "" {
		if t0, err := time.ParseInLocation("2006-01-02 15:04:05", lastUpdate, time.Local); err == nil {
			if time.Since(t0) > time.Duration(task.ForceInterval)*time.Hour {
				force = true
			}
		}
	}
	if last == ip && !force {
		setStatus(store, task.Key, "运行中(IP未变化)", ip, time.Now().Format("2006-01-02 15:04:05"), false)
		return nil
	}

	p, err := Get(task.Provider)
	if err != nil {
		logger.Error("ddns", "任务[%s] %v", task.Name, err)
		setStatus(store, task.Key, "服务商错误", "", "", false)
		return err
	}
	if err := p.Update(task, ip); err != nil {
		logger.Error("ddns", "任务[%s]更新失败: %v", task.Name, err)
		setStatus(store, task.Key, "更新失败: "+err.Error(), "", "", false)
		return err
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	logger.Info("ddns", "任务[%s]更新成功 %s -> %s", task.Name, fqdn(task), ip)
	setStatus(store, task.Key, "运行中", ip, now, true)
	notifyWebhook(task, ip)
	return nil
}

func setStatus(store *config.Store, key, status, ip, when string, count bool) {
	store.SetStatus("ddns", key, func(item interface{}) {
		v, ok := item.(*config.DDNSTask)
		if !ok {
			return
		}
		v.Status = status
		if ip != "" {
			v.CurrentIP = ip
		}
		if when != "" {
			v.LastUpdate = when
		}
		if count {
			v.UpdateCount++
		}
	})
}

func resolveIP(task config.DDNSTask) (string, error) {
	switch task.IPSource {
	case "interface", "netcard":
		if task.InterfaceName == "" {
			return "", fmt.Errorf("未指定网卡")
		}
		return netutil.InterfaceIP(task.InterfaceName, task.IPType)
	case "manual":
		if task.ManualIP == "" {
			return "", fmt.Errorf("未填写手动 IP")
		}
		return task.ManualIP, nil
	default:
		return netutil.GetPublicIP(task.IPType)
	}
}

func notifyWebhook(task config.DDNSTask, ip string) {
	if strings.TrimSpace(task.WebhookURL) == "" {
		return
	}
	u := strings.NewReplacer("#{ip}", ip, "#{domain}", task.Domain, "#{subDomain}", task.SubDomain).Replace(task.WebhookURL)
	method := http.MethodGet
	if strings.Contains(u, " ") {
		parts := strings.SplitN(u, " ", 2)
		method = strings.ToUpper(parts[0])
		u = parts[1]
	}
	go func() {
		req, err := http.NewRequest(method, u, nil)
		if err != nil {
			logger.Warn("ddns", "任务[%s] Webhook 构造失败: %v", task.Name, err)
			return
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			logger.Warn("ddns", "任务[%s] Webhook 调用失败: %v", task.Name, err)
			return
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		logger.Info("ddns", "任务[%s] Webhook 回调完成，状态码 %d", task.Name, resp.StatusCode)
	}()
}

var httpClient = &http.Client{Timeout: 10 * time.Second}
