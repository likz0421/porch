// Package cert 管理 ACME 证书的申请、落盘、到期巡检与自动续期。
package cert

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"porch/internal/acme"
	"porch/internal/config"
	"porch/internal/logger"
)

// Manager 证书任务管理器
type Manager struct {
	mu       sync.Mutex
	store    *config.Store
	busy     map[string]bool
	failedAt map[string]time.Time
	stopCh   chan struct{}
	wg       sync.WaitGroup
	forceCh  chan string
	checkInt time.Duration
}

// New 创建证书管理器
func New(store *config.Store) *Manager {
	return &Manager{
		store:    store,
		busy:     make(map[string]bool),
		failedAt: make(map[string]time.Time),
		stopCh:   make(chan struct{}),
		forceCh:  make(chan string, 8),
		checkInt: 1 * time.Hour,
	}
}

// Start 启动到期巡检循环（每 1 小时一次）
func (m *Manager) Start() {
	m.wg.Add(1)
	go m.loop()
	// 启动后立即巡检一次，缺失/即将过期的证书会自动补签
	go m.Reload()
}

// Stop 停止巡检
func (m *Manager) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

func (m *Manager) loop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.checkInt)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case key := <-m.forceCh:
			m.applyByKey(key, true)
		case <-ticker.C:
			m.Reload()
		}
	}
}

// Reload 巡检全部任务：证书缺失或临近到期则自动续期
func (m *Manager) Reload() {
	cfg := m.store.Get()
	for _, t := range cfg.CertTasks {
		if !t.Enable {
			continue
		}
		// 失败退避：申请失败后 6 小时内不再自动重试，避免触发 ACME 限流
		m.mu.Lock()
		last, has := m.failedAt[t.Key]
		m.mu.Unlock()
		if has && time.Since(last) < 6*time.Hour {
			continue
		}
		need, days := needRenew(m.store, t)
		if !need {
			logger.Debug("cert", "任务[%s]证书剩余 %d 天，无需续期", t.Name, days)
			continue
		}
		logger.Info("cert", "任务[%s]证书需要续期(剩余 %d 天)", t.Name, days)
		m.applyByKey(t.Key, false)
	}
}

// Apply 手动触发指定任务申请/续期
func (m *Manager) Apply(key string) error {
	m.mu.Lock()
	if m.busy[key] {
		m.mu.Unlock()
		return fmt.Errorf("该任务正在处理中")
	}
	m.mu.Unlock()
	select {
	case m.forceCh <- key:
	default:
		return fmt.Errorf("任务队列已满，请稍后再试")
	}
	return nil
}

func (m *Manager) applyByKey(key string, force bool) {
	cfg := m.store.Get()
	for _, t := range cfg.CertTasks {
		if t.Key == key {
			if !force && !t.Enable {
				return
			}
			go m.issue(t)
			return
		}
	}
}

func (m *Manager) issue(task config.CertTask) {
	m.mu.Lock()
	if m.busy[task.Key] {
		m.mu.Unlock()
		return
	}
	m.busy[task.Key] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.busy, task.Key)
		m.mu.Unlock()
	}()

	// 用存储里的最新配置执行，避免异步排队期间用户改了域名/邮箱还用旧快照
	for _, t := range m.store.Get().CertTasks {
		if t.Key == task.Key {
			task = t
			break
		}
	}

	setStatus(m.store, task.Key, "申请中", "", "")
	if len(task.Domains) == 0 {
		setStatus(m.store, task.Key, "失败: 未填写域名", "", "")
		return
	}
	port := task.HTTPPort
	if port <= 0 {
		port = 80
	}
	client, err := acme.NewClient(task.Email, task.CAProvider, task.KeyType)
	if err != nil {
		logger.Error("cert", "任务[%s]创建 ACME 客户端失败: %v", task.Name, err)
		m.failStatus(task, err.Error())
		return
	}
	if err := client.Register(); err != nil {
		logger.Error("cert", "任务[%s]注册 ACME 账号失败: %v", task.Name, err)
		m.noteFailure(task.Key)
		m.failStatus(task, err.Error())
		return
	}
	certPEM, keyPEM, err := client.Obtain(task.Domains, port)
	if err != nil {
		logger.Error("cert", "任务[%s]申请失败: %v", task.Name, err)
		m.noteFailure(task.Key)
		m.failStatus(task, err.Error())
		return
	}
	m.clearFailure(task.Key)
	certPath, keyPath, err := m.save(task, certPEM, keyPEM)
	if err != nil {
		logger.Error("cert", "任务[%s]保存证书失败: %v", task.Name, err)
		m.failStatus(task, err.Error())
		return
	}
	expire := ""
	if t, err := acme.CertExpire(certPath); err == nil {
		expire = t.Format("2006-01-02 15:04:05")
	}
	logger.Info("cert", "任务[%s]证书已保存: %s (过期时间 %s)", task.Name, certPath, expire)
	setStatus(m.store, task.Key, "已签发", expire, time.Now().Format("2006-01-02 15:04:05"))

	// 同步更新配置中的路径，便于反代规则引用
	m.store.Update(func(c *config.Config) {
		for i := range c.CertTasks {
			if c.CertTasks[i].Key == task.Key {
				c.CertTasks[i].CertPath = certPath
				c.CertTasks[i].KeyPath = keyPath
				c.CertTasks[i].ExpireTime = expire
				c.CertTasks[i].Status = "已签发"
			}
		}
	})
	m.store.SaveCert()
}

func (m *Manager) noteFailure(key string) {
	m.mu.Lock()
	m.failedAt[key] = time.Now()
	m.mu.Unlock()
}

func (m *Manager) clearFailure(key string) {
	m.mu.Lock()
	delete(m.failedAt, key)
	m.mu.Unlock()
}

func (m *Manager) save(task config.CertTask, certPEM, keyPEM []byte) (string, string, error) {
	dir := filepath.Join(m.store.CertDir(), safeName(task))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	certPath := filepath.Join(dir, "fullchain.pem")
	keyPath := filepath.Join(dir, "privkey.pem")
	if err := acme.SavePEM(certPath, certPEM); err != nil {
		return "", "", err
	}
	if err := acme.SavePEM(keyPath, keyPEM); err != nil {
		return "", "", err
	}
	return certPath, keyPath, nil
}

func safeName(task config.CertTask) string {
	name := strings.TrimSpace(task.Name)
	if name == "" && len(task.Domains) > 0 {
		name = task.Domains[0]
	}
	if name == "" {
		name = task.Key
	}
	name = strings.NewReplacer("/", "_", "\\", "_", " ", "_", "*", "wildcard").Replace(name)
	return name
}

// failStatus 记录签发/续期失败。
// 若该任务已有仍在有效期内的证书，则保留「已签发」状态，
// 仅把失败原因写入 LastError，避免一次网络抖动让看板误报「0 张已签发」。
//
// 注意：这里必须用存储里的**最新**任务，而不是 issue() 启动时的快照。
// 异步签发期间用户可能刚导入/续期成功，用旧快照会把新状态误判成"从未签发"。
func (m *Manager) failStatus(task config.CertTask, msg string) {
	cur := task
	for _, t := range m.store.Get().CertTasks {
		if t.Key == task.Key {
			cur = t
			break
		}
	}
	task = cur
	keepIssued := false
	if task.ExpireTime != "" {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", task.ExpireTime, time.Local); err == nil && time.Now().Before(t) {
			keepIssued = true
		}
	}
	if keepIssued {
		logger.Warn("cert", "任务[%s]续期失败，但现有证书仍在有效期（%s 到期），保留已签发状态: %s",
			task.Name, task.ExpireTime, msg)
		setStatus(m.store, task.Key, "已签发", "", "")
	} else {
		setStatus(m.store, task.Key, "失败: "+msg, "", "")
	}
	m.store.SetStatus("cert", task.Key, func(item interface{}) {
		if v, ok := item.(*config.CertTask); ok {
			v.LastError = msg
		}
	})
	m.store.SaveCert()
}

func setStatus(store *config.Store, key, status, expire, issue string) {
	store.SetStatus("cert", key, func(item interface{}) {
		v, ok := item.(*config.CertTask)
		if !ok {
			return
		}
		v.Status = status
		if expire != "" {
			v.ExpireTime = expire
		}
		if issue != "" {
			v.LastIssue = issue
			v.IssueCount++
		}
	})
}

// needRenew 判断证书是否需要续期，返回剩余天数
func needRenew(store *config.Store, task config.CertTask) (bool, int) {
	certPath := task.CertPath
	if certPath == "" {
		certPath = filepath.Join(store.CertDir(), safeName(task), "fullchain.pem")
	}
	expire, err := acme.CertExpire(certPath)
	if err != nil {
		return true, 0
	}
	days := int(time.Until(expire).Hours() / 24)
	before := task.RenewBefore
	if before <= 0 {
		before = 20
	}
	return days <= before, days
}
