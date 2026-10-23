package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestInitCreatesDefaultConfig(t *testing.T) {
	dir := t.TempDir()
	s, err := Init(dir)
	if err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	if s.Dir() != dir {
		t.Fatalf("配置目录不符: %q", s.Dir())
	}
	base := s.Get().Base
	if base.AdminWebListenPort != 16601 {
		t.Errorf("默认端口应为 16601，实际 %d", base.AdminWebListenPort)
	}
	if base.AdminAccount != "666" || base.AdminPassword != "666" {
		t.Errorf("默认账号密码不正确: %q/%q", base.AdminAccount, base.AdminPassword)
	}
	if base.SessionTimeout != 7200 {
		t.Errorf("默认会话超时应为 7200，实际 %d", base.SessionTimeout)
	}
	if !base.AutoStart {
		t.Error("默认应开启自动启动")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Errorf("配置目录未创建: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Update(func(c *Config) {
		c.DDNSTasks = []DDNSTask{{Key: "k1", Name: "任务一", Domain: "example.com", CheckInterval: 300}}
		c.ReverseProxies = []ReverseProxyRule{{Key: "r1", Name: "规则一", ListenPort: 8080, Backends: []string{"http://127.0.0.1:5000"}}}
		c.CertTasks = []CertTask{{Key: "c1", Name: "证书一", Domains: []string{"a.example.com"}}}
	})
	if err := s.SaveDDNS(); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveReverseProxy(); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCert(); err != nil {
		t.Fatal(err)
	}

	// 重新从同一目录加载，数据应完整保留
	s2, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s2.Get()
	if len(cfg.DDNSTasks) != 1 || cfg.DDNSTasks[0].Name != "任务一" {
		t.Errorf("DDNS 任务未正确持久化: %+v", cfg.DDNSTasks)
	}
	if len(cfg.ReverseProxies) != 1 || cfg.ReverseProxies[0].ListenPort != 8080 {
		t.Errorf("反代规则未正确持久化: %+v", cfg.ReverseProxies)
	}
	if len(cfg.CertTasks) != 1 || cfg.CertTasks[0].Domains[0] != "a.example.com" {
		t.Errorf("证书任务未正确持久化: %+v", cfg.CertTasks)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	s, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Update(func(c *Config) { c.Base.AdminWebListenPort = 18001 })
	if err := s.SaveBase(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "base.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("base.json 未生成: %v", err)
	}
	// 原子写入不应残留临时文件
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Error("存在未清理的临时文件 base.json.tmp")
	}
}

func TestLoadCorruptBaseFallsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.json"), []byte("{not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Init(dir)
	if err != nil {
		t.Fatalf("配置损坏时不应导致初始化失败: %v", err)
	}
	if got := s.Get().Base.AdminWebListenPort; got != 16601 {
		t.Errorf("损坏配置应回退默认端口 16601，实际 %d", got)
	}
}

func TestOnChangeFires(t *testing.T) {
	s, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan string, 1)
	s.OnChange(func(name string) { ch <- name })
	if err := s.SaveDDNS(); err != nil {
		t.Fatal(err)
	}
	select {
	case name := <-ch:
		if name != "ddns" {
			t.Errorf("变更事件名应为 ddns，实际 %q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("配置变更回调未被触发")
	}
}

func TestNewKeyUnique(t *testing.T) {
	seen := make(map[string]bool, 500)
	for i := 0; i < 500; i++ {
		k := NewKey("ddns")
		if len(k) != len("ddns-")+6 {
			t.Fatalf("key 格式异常: %q", k)
		}
		if k[:5] != "ddns-" {
			t.Fatalf("key 前缀异常: %q", k)
		}
		if seen[k] {
			t.Fatalf("生成了重复 key: %q", k)
		}
		seen[k] = true
	}
}

func TestSetStatusOnlyRuntime(t *testing.T) {
	dir := t.TempDir()
	s, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Update(func(c *Config) {
		c.DDNSTasks = []DDNSTask{{Key: "k1", Name: "任务", Domain: "example.com"}}
	})
	s.SetStatus("ddns", "k1", func(item interface{}) {
		v, ok := item.(*DDNSTask)
		if !ok {
			t.Fatalf("SetStatus 传入类型错误: %T", item)
		}
		v.Status = "运行中"
		v.CurrentIP = "1.2.3.4"
		v.UpdateCount++
	})
	got := s.Get().DDNSTasks[0]
	if got.Status != "运行中" || got.CurrentIP != "1.2.3.4" || got.UpdateCount != 1 {
		t.Fatalf("运行时状态未更新: %+v", got)
	}
	// SetStatus 不落盘：磁盘上不应出现 ddns.json
	if _, err := os.Stat(filepath.Join(dir, "ddns.json")); err == nil {
		t.Error("SetStatus 不应写入磁盘")
	}
}

func TestConcurrentUpdateAndGet(t *testing.T) {
	s, err := Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.Update(func(c *Config) { c.Base.AdminWebListenPort = 16601 + n })
				_ = s.Get()
				s.SetStatus("ddns", "none", nil)
			}
		}(i)
	}
	wg.Wait()
	if s.Get().Base.AdminWebListenPort < 16601 {
		t.Error("并发写入后配置异常")
	}
}

func TestRecoverPortWhenZero(t *testing.T) {
	dir := t.TempDir()
	body := `{"adminWebListenPort":0,"adminAccount":"","adminPassword":""}`
	if err := os.WriteFile(filepath.Join(dir, "base.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	base := s.Get().Base
	if base.AdminWebListenPort != 16601 {
		t.Errorf("端口 0 应回退 16601，实际 %d", base.AdminWebListenPort)
	}
	if base.AdminAccount != "666" {
		t.Errorf("空账号应回退 666，实际 %q", base.AdminAccount)
	}
}
