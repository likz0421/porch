// Package config 负责配置结构定义、加载、原子保存与变更通知。
// 本程序只保留两个业务模块：动态域名(DDNS) 与 SSL 证书(ACME)。
package config

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"porch/internal/logger"
)

// BaseConfig 全局基础配置
type BaseConfig struct {
	AdminWebListenPort  int    `json:"adminWebListenPort"`
	AdminAccount        string `json:"adminAccount"`
	AdminPassword       string `json:"adminPassword"`
	AllowInternetAccess bool   `json:"allowInternetAccess"`
	EnableTLS           bool   `json:"enableTLS"`
	TLSCertFile         string `json:"tlsCertFile"`
	TLSKeyFile          string `json:"tlsKeyFile"`
	SessionTimeout      int    `json:"sessionTimeout"`
	LogLevel            string `json:"logLevel"`
	AutoStart           bool   `json:"autoStart"`
}

// DDNSTask 动态域名任务
type DDNSTask struct {
	Key           string `json:"key"`
	Enable        bool   `json:"enable"`
	Name          string `json:"name"`
	Provider      string `json:"provider"` // cloudflare/alidns/dnspod/godaddy/duckdns/noip/callback/none
	AccessID      string `json:"accessID"`
	AccessKey     string `json:"accessKey"`
	Domain        string `json:"domain"`
	SubDomain     string `json:"subDomain"`
	RecordType    string `json:"recordType"` // A / AAAA
	IPType        string `json:"ipType"`     // ipv4 / ipv6
	IPSource      string `json:"ipSource"`   // api / interface / manual
	InterfaceName string `json:"interfaceName"`
	ManualIP      string `json:"manualIP"`
	CheckInterval int    `json:"checkInterval"` // 秒
	ForceInterval int    `json:"forceInterval"` // 小时，0 表示不强制更新
	TTL           int    `json:"ttl"`
	WebhookURL    string `json:"webhookURL"`
	Status        string `json:"status,omitempty"`
	CurrentIP     string `json:"currentIP,omitempty"`
	LastUpdate    string `json:"lastUpdate,omitempty"`
	UpdateCount   int    `json:"updateCount,omitempty"`
}

// ReverseProxyRule 反向代理规则
type ReverseProxyRule struct {
	Key          string   `json:"key"`
	Enable       bool     `json:"enable"`
	Name         string   `json:"name"`
	ListenPort   int      `json:"listenPort"`
	Domain       string   `json:"domain"` // 留空表示匹配所有 Host
	PathPrefix   string   `json:"pathPrefix"`
	Backends     []string `json:"backends"`
	EnableTLS    bool     `json:"enableTLS"`
	CertFile     string   `json:"certFile"`
	KeyFile      string   `json:"keyFile"`
	EnableGzip   bool     `json:"enableGzip"`
	EnableXReal  bool     `json:"enableXReal"`
	Status       string   `json:"status,omitempty"`
	RequestCount int64    `json:"requestCount,omitempty"`
}

// CertTask ACME 证书任务
type CertTask struct {
	Key         string   `json:"key"`
	Enable      bool     `json:"enable"`
	Name        string   `json:"name"`
	Domains     []string `json:"domains"`
	Email       string   `json:"email"`
	CAProvider  string   `json:"caProvider"` // letsencrypt / buypass / google / 测试环境
	KeyType     string   `json:"keyType"`    // ec256 / rsa2048
	Challenge   string   `json:"challenge"`  // http01
	HTTPPort    int      `json:"httpPort"`
	RenewBefore int      `json:"renewBefore"` // 剩余天数小于该值时续期
	Status      string   `json:"status,omitempty"`
	ExpireTime  string   `json:"expireTime,omitempty"`
	LastIssue   string   `json:"lastIssue,omitempty"`
	LastError   string   `json:"lastError,omitempty"` // 最近一次签发/续期失败原因（不影响已签发状态）
	CertPath    string   `json:"certPath,omitempty"`
	KeyPath     string   `json:"keyPath,omitempty"`
	IssueCount  int      `json:"issueCount,omitempty"`
}

// Config 完整配置集合
type Config struct {
	Base           BaseConfig         `json:"base"`
	DDNSTasks      []DDNSTask         `json:"ddnsTasks"`
	CertTasks      []CertTask         `json:"certTasks"`
	ReverseProxies []ReverseProxyRule `json:"reverseProxies"`
}

// Store 配置存储
type Store struct {
	mu       sync.RWMutex
	dir      string
	cfg      Config
	onChange []func(name string)
}

var global *Store

// Init 初始化配置目录并加载配置
func Init(dir string) (*Store, error) {
	if dir == "" {
		dir = "porchconf"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建配置目录失败: %w", err)
	}
	s := &Store{dir: dir}
	if err := s.Load(); err != nil {
		return nil, err
	}
	global = s
	return s, nil
}

// Global 返回全局配置存储
func Global() *Store { return global }

// Dir 返回配置目录
func (s *Store) Dir() string { return s.dir }

// CertDir 返回证书存放目录
func (s *Store) CertDir() string { return filepath.Join(s.dir, "certs") }

// OnChange 注册配置变更回调
func (s *Store) OnChange(fn func(name string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = append(s.onChange, fn)
}

func (s *Store) fire(name string) {
	cbs := make([]func(string), len(s.onChange))
	copy(cbs, s.onChange)
	for _, fn := range cbs {
		go fn(name)
	}
}

// Get 返回配置快照
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Update 在锁内修改配置，返回修改后的快照
func (s *Store) Update(fn func(c *Config)) Config {
	s.mu.Lock()
	fn(&s.cfg)
	snap := s.cfg
	s.mu.Unlock()
	return snap
}

// Load 从磁盘加载全部配置
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c := defaultConfig()
	if err := readJSON(filepath.Join(s.dir, "base.json"), &c.Base); err != nil {
		logger.Warn("config", "读取 base.json 失败(%v)，使用默认配置", err)
	} else {
		// 兼容旧配置缺少默认值的场景
		if c.Base.AdminWebListenPort == 0 {
			c.Base.AdminWebListenPort = 16601
		}
		if c.Base.AdminAccount == "" {
			c.Base.AdminAccount = "666"
			c.Base.AdminPassword = "666"
		}
		if c.Base.SessionTimeout == 0 {
			c.Base.SessionTimeout = 7200
		}
	}
	readJSON(filepath.Join(s.dir, "ddns.json"), &c.DDNSTasks)
	readJSON(filepath.Join(s.dir, "cert.json"), &c.CertTasks)
	readJSON(filepath.Join(s.dir, "reverseproxy.json"), &c.ReverseProxies)
	s.cfg = c
	return nil
}

func defaultConfig() Config {
	return Config{
		Base: BaseConfig{
			AdminWebListenPort:  16601,
			AdminAccount:        "666",
			AdminPassword:       "666",
			AllowInternetAccess: false,
			SessionTimeout:      7200,
			LogLevel:            "info",
			AutoStart:           true,
		},
	}
}

func readJSON(path string, v interface{}) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// saveJSON 原子写入：先写临时文件再重命名，避免掉电导致配置损坏
func (s *Store) saveJSON(name string, v interface{}) error {
	path := filepath.Join(s.dir, name+".json")
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SaveBase 保存基础配置
func (s *Store) SaveBase() error {
	s.mu.RLock()
	v := s.cfg.Base
	s.mu.RUnlock()
	if err := s.saveJSON("base", v); err != nil {
		return err
	}
	s.fire("base")
	return nil
}

// SaveDDNS 保存 DDNS 任务
func (s *Store) SaveDDNS() error {
	s.mu.RLock()
	v := s.cfg.DDNSTasks
	s.mu.RUnlock()
	if err := s.saveJSON("ddns", v); err != nil {
		return err
	}
	s.fire("ddns")
	return nil
}

// SaveReverseProxy 保存反向代理规则
func (s *Store) SaveReverseProxy() error {
	s.mu.RLock()
	v := s.cfg.ReverseProxies
	s.mu.RUnlock()
	if err := s.saveJSON("reverseproxy", v); err != nil {
		return err
	}
	s.fire("reverseproxy")
	return nil
}

// SaveCert 保存证书任务
func (s *Store) SaveCert() error {
	s.mu.RLock()
	v := s.cfg.CertTasks
	s.mu.RUnlock()
	if err := s.saveJSON("cert", v); err != nil {
		return err
	}
	s.fire("cert")
	return nil
}

// SetStatus 更新运行时状态字段（不落盘，避免频繁写 Flash）
func (s *Store) SetStatus(module, key string, apply func(item interface{})) {
	s.mu.Lock()
	switch module {
	case "ddns":
		for i := range s.cfg.DDNSTasks {
			if s.cfg.DDNSTasks[i].Key == key && apply != nil {
				apply(&s.cfg.DDNSTasks[i])
			}
		}
	case "cert":
		for i := range s.cfg.CertTasks {
			if s.cfg.CertTasks[i].Key == key && apply != nil {
				apply(&s.cfg.CertTasks[i])
			}
		}
	case "reverseproxy":
		for i := range s.cfg.ReverseProxies {
			if s.cfg.ReverseProxies[i].Key == key && apply != nil {
				apply(&s.cfg.ReverseProxies[i])
			}
		}
	}
	s.mu.Unlock()
}

// NewKey 生成规则唯一标识
func NewKey(prefix string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(0xffffff))
	if err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()%0xffffff)
	}
	return fmt.Sprintf("%s-%06x", prefix, n.Int64())
}
