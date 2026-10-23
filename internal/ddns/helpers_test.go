package ddns

import (
	"testing"

	"porch/internal/config"
)

func TestFqdn(t *testing.T) {
	cases := []struct {
		task config.DDNSTask
		want string
	}{
		{config.DDNSTask{Domain: "example.com", SubDomain: "www"}, "www.example.com"},
		{config.DDNSTask{Domain: "example.com", SubDomain: ""}, "example.com"},
		{config.DDNSTask{Domain: "example.com", SubDomain: "@"}, "example.com"},
		{config.DDNSTask{Domain: ".example.com.", SubDomain: "a."}, "a.example.com"},
	}
	for _, c := range cases {
		if got := fqdn(c.task); got != c.want {
			t.Errorf("fqdn(%+v) = %q，期望 %q", c.task, got, c.want)
		}
	}
}

func TestRecordType(t *testing.T) {
	if got := recordType("ipv6"); got != "AAAA" {
		t.Errorf("ipv6 应返回 AAAA，实际 %q", got)
	}
	if got := recordType("IPv6"); got != "AAAA" {
		t.Errorf("IPv6 大小写不敏感，实际 %q", got)
	}
	if got := recordType("ipv4"); got != "A" {
		t.Errorf("ipv4 应返回 A，实际 %q", got)
	}
	if got := recordType(""); got != "A" {
		t.Errorf("空类型应默认 A，实际 %q", got)
	}
}

func TestResolveIPManual(t *testing.T) {
	ip, err := resolveIP(config.DDNSTask{IPSource: "manual", ManualIP: "198.51.100.7"})
	if err != nil {
		t.Fatalf("手动 IP 不应报错: %v", err)
	}
	if ip != "198.51.100.7" {
		t.Errorf("手动 IP 不符: %q", ip)
	}

	if _, err := resolveIP(config.DDNSTask{IPSource: "manual"}); err == nil {
		t.Error("缺少手动 IP 时应报错")
	}
}

func TestResolveIPInterfaceRequiresName(t *testing.T) {
	if _, err := resolveIP(config.DDNSTask{IPSource: "interface"}); err == nil {
		t.Error("未指定网卡时应报错")
	}
	if _, err := resolveIP(config.DDNSTask{IPSource: "netcard"}); err == nil {
		t.Error("netcard 别名未指定网卡时也应报错")
	}
}

func TestSetStatusUpdatesFields(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store.Update(func(c *config.Config) {
		c.DDNSTasks = []config.DDNSTask{{Key: "k1", Name: "任务"}}
	})

	setStatus(store, "k1", "运行中", "203.0.113.5", "2026-10-23 09:00:00", true)
	got := store.Get().DDNSTasks[0]
	if got.Status != "运行中" || got.CurrentIP != "203.0.113.5" || got.UpdateCount != 1 {
		t.Fatalf("状态写入不正确: %+v", got)
	}
	if got.LastUpdate != "2026-10-23 09:00:00" {
		t.Errorf("最后更新时间不符: %q", got.LastUpdate)
	}

	// count=false 时不应累加更新次数，空字段也不应覆盖已有值
	setStatus(store, "k1", "运行中(IP未变化)", "", "", false)
	got = store.Get().DDNSTasks[0]
	if got.UpdateCount != 1 {
		t.Errorf("不应累加更新次数，实际 %d", got.UpdateCount)
	}
	if got.CurrentIP != "203.0.113.5" || got.LastUpdate != "2026-10-23 09:00:00" {
		t.Errorf("空参数不应覆盖已有值: %+v", got)
	}
}

func TestSetStatusUnknownKeyNoPanic(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	setStatus(store, "not-exist", "x", "1.1.1.1", "", true) // 不应 panic
}

func TestProvidersMetadata(t *testing.T) {
	list := Providers()
	if len(list) < 8 {
		t.Fatalf("服务商数量偏少: %d", len(list))
	}
	seen := map[string]bool{}
	for _, p := range list {
		if p["value"] == "" || p["label"] == "" {
			t.Errorf("服务商缺少 value/label: %+v", p)
		}
		if seen[p["value"]] {
			t.Errorf("服务商标识重复: %s", p["value"])
		}
		seen[p["value"]] = true
	}
	for _, must := range []string{"cloudflare", "alidns", "dnspod", "duckdns", "callback", "none"} {
		if !seen[must] {
			t.Errorf("缺少必备服务商: %s", must)
		}
	}
}

func TestGetProvider(t *testing.T) {
	if p, err := Get("none"); err != nil {
		t.Fatalf("none 服务商应可用: %v", err)
	} else if p.Name() == "" {
		t.Error("服务商名称不应为空")
	}
	if p, err := Get("CF"); err != nil || p.Name() == "" {
		t.Errorf("别名 cf 应解析为 Cloudflare: %v", err)
	}
	if _, err := Get("not-a-provider"); err == nil {
		t.Error("未知服务商应返回错误")
	}
}

func TestNoneProviderUpdate(t *testing.T) {
	p, err := Get("none")
	if err != nil {
		t.Fatal(err)
	}
	// 空操作服务商不应报错，也不应发起网络请求
	if err := p.Update(config.DDNSTask{Key: "k", Domain: "example.com"}, "198.51.100.1"); err != nil {
		t.Errorf("none 服务商 Update 不应报错: %v", err)
	}
}

func TestNotFoundRunOnce(t *testing.T) {
	store, err := config.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(store)
	// 配置中不存在的任务：execute 仍按传入副本执行，手动 IP 场景应成功
	task := config.DDNSTask{Key: "ghost", Name: "幽灵", Provider: "none", Domain: "example.com", IPSource: "manual", ManualIP: "198.51.100.9"}
	if err := m.RunOnce(task); err != nil {
		t.Errorf("RunOnce 手动 IP 应成功: %v", err)
	}
}
