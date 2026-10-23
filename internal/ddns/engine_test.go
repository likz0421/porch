package ddns

import (
	"testing"

	"porch/internal/config"
)

// TestExecuteUsesLatestConfig 验证执行时取的是配置存储中的最新内容：
// 修改任务（如更换服务商凭据 / 手动 IP）后无需重启进程即可生效。
func TestExecuteUsesLatestConfig(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	task := config.DDNSTask{
		Key:        "ddns-test",
		Name:       "测试任务",
		Enable:     true,
		Provider:   "none",
		Domain:     "example.com",
		SubDomain:  "home",
		RecordType: "A",
		IPType:     "ipv4",
		IPSource:   "manual",
		ManualIP:   "198.51.100.1",
	}
	store.Update(func(c *config.Config) { c.DDNSTasks = []config.DDNSTask{task} })

	stale := task // 模拟运行中任务持有的旧副本
	if err := execute(store, stale); err != nil {
		t.Fatalf("首次执行失败: %v", err)
	}
	if got := currentIP(store, "ddns-test"); got != "198.51.100.1" {
		t.Fatalf("首次解析 IP 不正确: %q", got)
	}

	// 修改配置中的 IP
	store.Update(func(c *config.Config) { c.DDNSTasks[0].ManualIP = "203.0.113.9" })
	// 仍传入旧副本执行，应当自动取到新值
	if err := execute(store, stale); err != nil {
		t.Fatalf("第二次执行失败: %v", err)
	}
	if got := currentIP(store, "ddns-test"); got != "203.0.113.9" {
		t.Fatalf("修改后仍使用旧配置，实际 IP: %q", got)
	}
}

func currentIP(store *config.Store, key string) string {
	for _, t := range store.Get().DDNSTasks {
		if t.Key == key {
			return t.CurrentIP
		}
	}
	return ""
}
