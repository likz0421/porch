package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestJWKFixedLength 验证 JWK 的 EC 坐标按曲线长度补齐（补齐前会导致 ACME 侧 JWS 校验失败）
func TestJWKFixedLength(t *testing.T) {
	c, err := NewClient("tester@example.com", "letsencrypt", "ec256")
	if err != nil {
		t.Fatal(err)
	}
	jwk, err := c.jwk()
	if err != nil {
		t.Fatal(err)
	}
	if jwk["kty"] != "EC" || jwk["crv"] != "P-256" {
		t.Fatalf("JWK 类型不正确: %v", jwk)
	}
	for _, k := range []string{"x", "y"} {
		raw, err := base64.RawURLEncoding.DecodeString(jwk[k])
		if err != nil {
			t.Fatalf("%s 解码失败: %v", k, err)
		}
		if len(raw) != 32 {
			t.Fatalf("%s 长度应为 32 字节，实际 %d 字节（会导致 JWS verification error）", k, len(raw))
		}
	}
}

// TestJWSVerifiable 验证生成的 JWS 能被账号公钥验签通过
func TestJWSVerifiable(t *testing.T) {
	c, err := NewClient("tester@example.com", "letsencrypt", "ec256")
	if err != nil {
		t.Fatal(err)
	}
	// 用本地服务提供 nonce，避免依赖外网
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", "test-nonce-123456")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c.dir.NewNonce = srv.URL + "/new-nonce"
	c.kid = "https://example.com/acme/acct/1"

	body, err := c.signRequest("https://example.com/acme/new-order", map[string]any{
		"identifiers": []map[string]string{{"type": "dns", "value": "example.com"}},
	})
	if err != nil {
		t.Fatalf("签名失败: %v", err)
	}
	var jws struct {
		Protected string `json:"protected"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(body, &jws); err != nil {
		t.Fatal(err)
	}
	var header map[string]any
	rawH, err := base64.RawURLEncoding.DecodeString(jws.Protected)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawH, &header); err != nil {
		t.Fatal(err)
	}
	if header["alg"] != "ES256" {
		t.Fatalf("算法应为 ES256，实际 %v", header["alg"])
	}
	if header["kid"] != c.kid {
		t.Fatalf("kid 未写入 protected: %v", header)
	}

	signingInput := jws.Protected + "." + jws.Payload
	sig, err := base64.RawURLEncoding.DecodeString(jws.Signature)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 64 {
		t.Fatalf("ES256 签名应为 64 字节(r||s)，实际 %d", len(sig))
	}
	digest := sha256.Sum256([]byte(signingInput))
	pub, ok := c.accountKey.Public().(*ecdsa.PublicKey)
	if !ok {
		t.Fatal("账号密钥不是 EC")
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("JWS 签名未能通过公钥验签")
	}
	// 同时确认曲线为 P-256
	if pub.Curve != elliptic.P256() {
		t.Fatalf("曲线应为 P-256")
	}
}

// TestThumbprint 验证 JWK 指纹可计算且长度正确
func TestThumbprint(t *testing.T) {
	c, err := NewClient("t@example.com", "letsencrypt", "ec256")
	if err != nil {
		t.Fatal(err)
	}
	thumb, err := c.thumbprint()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(thumb)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 32 {
		t.Fatalf("指纹应为 SHA-256 的 32 字节，实际 %d", len(raw))
	}
	if strings.Contains(thumb, "=") || strings.Contains(thumb, "+") || strings.Contains(thumb, "/") {
		t.Fatalf("指纹必须为 base64url 无填充编码: %s", thumb)
	}
}

// TestRSAKeyType 验证 RSA 账号密钥同样可用
func TestRSAKeyType(t *testing.T) {
	c, err := NewClient("t@example.com", "letsencrypt", "rsa2048")
	if err != nil {
		t.Fatal(err)
	}
	jwk, err := c.jwk()
	if err != nil {
		t.Fatal(err)
	}
	if jwk["kty"] != "RSA" {
		t.Fatalf("应为 RSA JWK，实际 %v", jwk)
	}
	n, err := base64.RawURLEncoding.DecodeString(jwk["n"])
	if err != nil {
		t.Fatal(err)
	}
	if len(n) != 256 {
		t.Fatalf("RSA-2048 模数应为 256 字节，实际 %d", len(n))
	}
}
