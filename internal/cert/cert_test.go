package cert

import (
	"testing"
	"time"

	"porch/internal/config"
)

func TestSafeName(t *testing.T) {
	cases := []struct {
		task config.CertTask
		want string
	}{
		{config.CertTask{Name: "家庭 证书"}, "家庭_证书"},
		{config.CertTask{Name: "a/b\\c"}, "a_b_c"},
		{config.CertTask{Name: "*.example.com"}, "wildcard.example.com"},
		{config.CertTask{Domains: []string{"a/b.com"}}, "a_b.com"},
		{config.CertTask{Key: "cert-1"}, "cert-1"},
	}
	for _, c := range cases {
		if got := safeName(c.task); got != c.want {
			t.Errorf("safeName(%+v) = %q，期望 %q", c.task, got, c.want)
		}
	}
}

func TestNeedRenewWhenCertMissing(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	need, days := needRenew(store, config.CertTask{Key: "c1", Name: "abc", Domains: []string{"a.com"}})
	if !need {
		t.Error("证书文件不存在时应判定为需要申请")
	}
	if days != 0 {
		t.Errorf("缺失证书剩余天数应为 0，实际 %d", days)
	}
}

func TestSetStatus(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) { c.CertTasks = []config.CertTask{{Key: "c1", Name: "t"}} })

	setStatus(store, "c1", "已签发", "2027-01-01 00:00:00", "2026-10-23 10:00:00")
	got := store.Get().CertTasks[0]
	if got.Status != "已签发" || got.ExpireTime != "2027-01-01 00:00:00" || got.IssueCount != 1 {
		t.Fatalf("状态写入不正确: %+v", got)
	}

	// 未提供签发时间时不应累加签发次数
	setStatus(store, "c1", "申请中", "", "")
	got = store.Get().CertTasks[0]
	if got.Status != "申请中" || got.IssueCount != 1 || got.ExpireTime != "2027-01-01 00:00:00" {
		t.Fatalf("二次状态写入不正确: %+v", got)
	}
}

func TestApplyQueueFull(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	// forceCh 容量为 8，未启动消费循环时第 9 次应失败
	for i := 0; i < 8; i++ {
		if err := m.Apply("k"); err != nil {
			t.Fatalf("第 %d 次入队不应失败: %v", i+1, err)
		}
	}
	if err := m.Apply("k"); err == nil {
		t.Error("队列已满时应返回错误")
	}
}

func TestApplyUnknownKeyNoPanic(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.applyByKey("not-exist", true) // 不应 panic
}

func TestStartStopWithNoTasks(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	m.Start()
	time.Sleep(30 * time.Millisecond)
	m.Stop()
}

func TestReloadSkipsDisabled(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) {
		c.CertTasks = []config.CertTask{{Key: "c1", Name: "disabled", Enable: false, Domains: []string{"a.com"}}}
	})
	m := New(store)
	m.Reload() // 已禁用任务不应触发任何申请
	m.mu.Lock()
	busy := len(m.busy)
	m.mu.Unlock()
	if busy != 0 {
		t.Errorf("已禁用任务不应进入申请流程，busy=%d", busy)
	}
}

// 续期失败时若证书仍在有效期内，不能把「已签发」改成「失败」
func TestFailStatusKeepsValidCert(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().AddDate(0, 2, 0).Format("2006-01-02 15:04:05")
	store.Update(func(c *config.Config) {
		c.CertTasks = []config.CertTask{{Key: "c1", Name: "有效证书", Status: "已签发", ExpireTime: future}}
	})
	m := New(store)

	m.failStatus(store.Get().CertTasks[0], "获取 ACME 目录失败: EOF")
	got := store.Get().CertTasks[0]
	if got.Status != "已签发" {
		t.Errorf("证书未过期时应保留已签发状态，实际 %q", got.Status)
	}
	if got.ExpireTime != future {
		t.Errorf("过期时间不应被清空，实际 %q", got.ExpireTime)
	}
	if got.LastError == "" {
		t.Error("失败原因应写入 LastError")
	}
}

// 证书已过期或从未签发时，失败应体现为失败状态
func TestFailStatusMarksFailedWhenExpired(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().AddDate(0, 0, -3).Format("2006-01-02 15:04:05")
	store.Update(func(c *config.Config) {
		c.CertTasks = []config.CertTask{
			{Key: "old", Name: "已过期", Status: "已签发", ExpireTime: past},
			{Key: "new", Name: "从未签发"},
		}
	})
	m := New(store)

	for _, key := range []string{"old", "new"} {
		var task config.CertTask
		for _, x := range store.Get().CertTasks {
			if x.Key == key {
				task = x
			}
		}
		m.failStatus(task, "连接超时")
	}
	for _, x := range store.Get().CertTasks {
		if x.Status == "已签发" {
			t.Errorf("任务 %s 已过期/未签发，不应保留已签发状态: %q", x.Key, x.Status)
		}
		if x.LastError != "连接超时" {
			t.Errorf("任务 %s 的 LastError 未写入: %q", x.Key, x.LastError)
		}
	}
}

// 异步签发用的是过期快照（例如创建时还没导入证书信息）时，
// 失败回写必须以存储里的最新状态为准，不能把新导入的"已签发"打成"失败"
func TestFailStatusUsesLatestStoredTask(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().AddDate(0, 3, 0).Format("2006-01-02 15:04:05")
	store.Update(func(c *config.Config) {
		c.CertTasks = []config.CertTask{{Key: "c1", Name: "刚导入的证书", Status: "已签发", ExpireTime: future}}
	})
	m := New(store)

	// 模拟 issue() 启动时的旧快照：没有 expireTime
	stale := config.CertTask{Key: "c1", Name: "刚导入的证书"}
	m.failStatus(stale, "获取 ACME 目录失败: EOF")

	got := store.Get().CertTasks[0]
	if got.Status != "已签发" {
		t.Errorf("应按存储里的最新状态保留已签发，实际 %q", got.Status)
	}
	if got.ExpireTime != future {
		t.Errorf("过期时间不应被改动，实际 %q", got.ExpireTime)
	}
	if got.LastError == "" {
		t.Error("失败原因仍应记录到 LastError")
	}
}
