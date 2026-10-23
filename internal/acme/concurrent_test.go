package acme

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// TestChallengeServerConcurrent 验证 HTTP-01 挑战服务在并发读写 token 时不产生数据竞争。
// 用 `go test -race` 运行：修复前这里会报 concurrent map read and map write。
func TestChallengeServerConcurrent(t *testing.T) {
	c, err := NewClient("race@example.com", "letsencrypt", "ec256")
	if err != nil {
		t.Fatal(err)
	}
	port := 18099
	if err := c.startChallengeServer(port); err != nil {
		t.Skipf("端口 %d 不可用，跳过: %v", port, err)
	}
	defer c.stopChallengeServer()

	var wg sync.WaitGroup
	// 并发写入挑战 token（模拟多域名同时验证）
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.setToken(fmt.Sprintf("token-%d", i), fmt.Sprintf("token-%d.thumb", i))
		}(i)
	}
	// 并发读取（模拟 ACME 服务器回调）
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			url := fmt.Sprintf("http://127.0.0.1:%d/.well-known/acme-challenge/token-%d", port, i)
			resp, err := http.Get(url)
			if err != nil {
				return
			}
			defer resp.Body.Close()
		}(i)
	}
	wg.Wait()

	if v, ok := c.lookupToken("token-7"); !ok || v != "token-7.thumb" {
		t.Fatalf("挑战内容读取不正确: %q %v", v, ok)
	}
}
