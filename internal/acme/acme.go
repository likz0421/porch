// Package acme 实现 ACME v2 (RFC 8555) 客户端的 HTTP-01 挑战流程，用于自动申请/续期证书。
// 仅依赖标准库，便于交叉编译到路由器等小型设备。
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"porch/internal/logger"
)

// CAProviders 支持的证书颁发机构
var CAProviders = []map[string]string{
	{"value": "letsencrypt", "label": "Let's Encrypt", "url": "https://acme-v02.api.letsencrypt.org/directory"},
	{"value": "letsencrypt-staging", "label": "Let's Encrypt(测试环境)", "url": "https://acme-staging-v02.api.letsencrypt.org/directory"},
	{"value": "buypass", "label": "Buypass Go SSL", "url": "https://api.buypass.com/acme/directory"},
	{"value": "google", "label": "Google Trust Services", "url": "https://dv.acme-v02.api.pki.goog/directory"},
}

// Client ACME 客户端
type Client struct {
	Email        string
	DirectoryURL string
	KeyType      string // ec256 / rsa2048
	accountKey   crypto.Signer
	kid          string
	dir          directory
	httpClient   *http.Client
	mu           sync.Mutex
	nonce        string
	challengeMux *http.ServeMux
	challengeSrv *http.Server
	tokensMu     sync.RWMutex // 保护 tokens：HTTP-01 服务协程会并发读取
	tokens       map[string]string
}

// setToken 记录挑战响应内容
func (c *Client) setToken(token, keyAuth string) {
	c.tokensMu.Lock()
	c.tokens[token] = keyAuth
	c.tokensMu.Unlock()
}

// lookupToken 查询挑战响应内容
func (c *Client) lookupToken(token string) (string, bool) {
	c.tokensMu.RLock()
	v, ok := c.tokens[token]
	c.tokensMu.RUnlock()
	return v, ok
}

type directory struct {
	NewNonce   string `json:"newNonce"`
	NewAccount string `json:"newAccount"`
	NewOrder   string `json:"newOrder"`
}

// NewClient 创建 ACME 客户端
func NewClient(email, caProvider, keyType string) (*Client, error) {
	url := ""
	for _, p := range CAProviders {
		if p["value"] == caProvider {
			url = p["url"]
		}
	}
	if url == "" {
		url = CAProviders[0]["url"]
	}
	var key crypto.Signer
	var err error
	if keyType == "rsa2048" {
		key, err = rsa.GenerateKey(rand.Reader, 2048)
	} else {
		key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	}
	if err != nil {
		return nil, err
	}
	return &Client{
		Email:        email,
		DirectoryURL: url,
		KeyType:      keyType,
		accountKey:   key,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		tokens:       make(map[string]string),
	}, nil
}

type acmeError struct {
	Type   string `json:"type"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

func (e acmeError) Error() string { return fmt.Sprintf("ACME 错误(%d): %s", e.Status, e.Detail) }

func (c *Client) getNonce() (string, error) {
	req, err := http.NewRequest(http.MethodHead, c.dir.NewNonce, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	n := resp.Header.Get("Replay-Nonce")
	if n == "" {
		return "", fmt.Errorf("未获取到 nonce")
	}
	return n, nil
}

func b64(data []byte) string { return base64.RawURLEncoding.EncodeToString(data) }

func (c *Client) jwk() (map[string]string, error) {
	pub := c.accountKey.Public()
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		// JWK 要求坐标按曲线字节长度左侧补零，否则 ACME 侧校验 JWS 会失败
		size := (k.Curve.Params().BitSize + 7) / 8
		return map[string]string{
			"kty": "EC",
			"crv": "P-256",
			"x":   b64(padBytes(k.X, size)),
			"y":   b64(padBytes(k.Y, size)),
		}, nil
	case *rsa.PublicKey:
		size := (k.N.BitLen() + 7) / 8
		return map[string]string{
			"kty": "RSA",
			"n":   b64(padBytes(k.N, size)),
			"e":   b64(bigEndianExponent(k.E)),
		}, nil
	}
	return nil, fmt.Errorf("不支持的密钥类型")
}

// padBytes 把大整数按固定字节长度左侧补零（JWK / JWS 要求定长编码）
func padBytes(v *big.Int, size int) []byte {
	b := v.Bytes()
	if len(b) >= size {
		return b
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out
}

func bigEndianExponent(e int) []byte {
	b := make([]byte, 0, 4)
	if e == 0 {
		return []byte{0}
	}
	for e > 0 {
		b = append([]byte{byte(e & 0xff)}, b...)
		e >>= 8
	}
	if b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return b
}

// thumbprint 计算 JWK 指纹，用于 keyAuthorization
func (c *Client) thumbprint() (string, error) {
	jwk, err := c.jwk()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(jwk)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return b64(sum[:]), nil
}

type jwsHeader struct {
	Alg   string            `json:"alg"`
	Nonce string            `json:"nonce"`
	URL   string            `json:"url"`
	Kid   string            `json:"kid,omitempty"`
	JWK   map[string]string `json:"jwk,omitempty"`
}

func (c *Client) signRequest(url string, payload interface{}) ([]byte, error) {
	nonce, err := c.getNonce()
	if err != nil {
		return nil, err
	}
	alg := "ES256"
	if _, ok := c.accountKey.(*rsa.PrivateKey); ok {
		alg = "RS256"
	}
	h := jwsHeader{Alg: alg, Nonce: nonce, URL: url}
	if c.kid != "" {
		h.Kid = c.kid
	} else {
		h.JWK, _ = c.jwk()
	}
	rawH, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	emptyPayload := false
	var rawP []byte
	switch v := payload.(type) {
	case nil:
		rawP = []byte{}
	case string:
		if v == "" {
			emptyPayload = true
			rawP = []byte{}
		} else {
			rawP = []byte(v)
		}
	default:
		rawP, err = json.Marshal(v)
		if err != nil {
			return nil, err
		}
	}
	_ = emptyPayload
	protected := b64(rawH)
	body := protected + "." + b64(rawP)
	hasher := sha256.New()
	hasher.Write([]byte(body))
	digest := hasher.Sum(nil)
	sig, err := c.accountKey.Sign(rand.Reader, digest, crypto.SHA256)
	if err != nil {
		return nil, err
	}
	// Go 的 ecdsa 私钥 Sign 返回 ASN.1 DER 序列，而 JWS 的 ES256 要求 raw r||s
	if ecKey, ok := c.accountKey.(*ecdsa.PrivateKey); ok {
		var parsed struct {
			R *big.Int
			S *big.Int
		}
		if _, err := asn1.Unmarshal(sig, &parsed); err != nil {
			return nil, fmt.Errorf("解析 ECDSA 签名失败: %w", err)
		}
		size := (ecKey.Curve.Params().BitSize + 7) / 8
		sig = append(padBytes(parsed.R, size), padBytes(parsed.S, size)...)
	}
	out, err := json.Marshal(map[string]string{
		"protected": protected,
		"payload":   b64(rawP),
		"signature": b64(sig),
	})
	return out, err
}

func (c *Client) post(url string, payload interface{}, out interface{}) (*http.Response, error) {
	body, err := c.signRequest(url, payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/jose+json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return resp, err
	}
	if resp.StatusCode >= 400 {
		ae := acmeError{}
		_ = json.Unmarshal(raw, &ae)
		return resp, ae
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp, fmt.Errorf("解析 ACME 响应失败(%s): %s", url, string(raw))
		}
	}
	return resp, nil
}

func (c *Client) postRaw(url string, payload interface{}) ([]byte, *http.Response, error) {
	body, err := c.signRequest(url, payload)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/jose+json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return raw, resp, err
}

// Register 注册（或复用）ACME 账号
func (c *Client) Register() error {
	resp, err := c.httpClient.Get(c.DirectoryURL)
	if err != nil {
		return fmt.Errorf("获取 ACME 目录失败: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &c.dir); err != nil {
		return fmt.Errorf("解析 ACME 目录失败: %s", string(raw))
	}
	contact := []string{}
	if c.Email != "" {
		contact = append(contact, "mailto:"+c.Email)
	}
	acct := struct {
		Status  string   `json:"status"`
		Contact []string `json:"contact"`
	}{}
	r, err := c.post(c.dir.NewAccount, map[string]interface{}{
		"termsOfServiceAgreed": true,
		"contact":              contact,
	}, &acct)
	if err != nil {
		return err
	}
	c.kid = r.Header.Get("Location")
	if c.kid == "" {
		return fmt.Errorf("ACME 账号注册未返回 Location")
	}
	logger.Info("acme", "ACME 账号就绪: %s", c.kid)
	return nil
}

type orderResp struct {
	Status      string `json:"status"`
	Expires     string `json:"expires"`
	Identifiers []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"identifiers"`
	Authorizations []string `json:"authorizations"`
	Finalize       string   `json:"finalize"`
	Certificate    string   `json:"certificate"`
}

type authzResp struct {
	Status     string `json:"status"`
	Identifier struct {
		Value string `json:"value"`
	} `json:"identifier"`
	Challenges []struct {
		Type      string `json:"type"`
		URL       string `json:"url"`
		Token     string `json:"token"`
		Status    string `json:"status"`
		Validated string `json:"validated"`
		Error     struct {
			Detail string `json:"detail"`
		} `json:"error"`
	} `json:"challenges"`
}

// Obtain 申请证书，HTTP-01 挑战会在 httpPort 端口上临时提供
// /.well-known/acme-challenge/<token> 响应。
func (c *Client) Obtain(domains []string, httpPort int) (certPEM, keyPEM []byte, err error) {
	if err := c.startChallengeServer(httpPort); err != nil {
		return nil, nil, err
	}
	defer c.stopChallengeServer()

	ids := make([]map[string]string, 0, len(domains))
	for _, d := range domains {
		ids = append(ids, map[string]string{"type": "dns", "value": d})
	}
	order := orderResp{}
	or, err := c.post(c.dir.NewOrder, map[string]interface{}{"identifiers": ids}, &order)
	if err != nil {
		return nil, nil, fmt.Errorf("创建订单失败: %w", err)
	}
	orderURL := or.Header.Get("Location")
	logger.Info("acme", "订单已创建: %s 状态=%s", orderURL, order.Status)

	thumb, err := c.thumbprint()
	if err != nil {
		return nil, nil, err
	}
	for _, authURL := range order.Authorizations {
		az := authzResp{}
		if _, err := c.post(authURL, "", &az); err != nil {
			return nil, nil, fmt.Errorf("获取授权失败: %w", err)
		}
		var chosen *struct {
			Type  string
			URL   string
			Token string
		}
		for _, ch := range az.Challenges {
			if ch.Type == "http-01" {
				chosen = &struct {
					Type  string
					URL   string
					Token string
				}{ch.Type, ch.URL, ch.Token}
				break
			}
		}
		if chosen == nil {
			return nil, nil, fmt.Errorf("域名 %s 未提供 http-01 挑战", az.Identifier.Value)
		}
		c.setToken(chosen.Token, chosen.Token+"."+thumb)
		logger.Info("acme", "准备挑战 %s (token=%s)", az.Identifier.Value, chosen.Token)
		if _, err := c.post(chosen.URL, map[string]interface{}{}, nil); err != nil {
			return nil, nil, fmt.Errorf("触发挑战失败: %w", err)
		}
		// 轮询挑战状态
		deadline := time.Now().Add(2 * time.Minute)
		for {
			cur := authzResp{}
			if _, err := c.post(authURL, "", &cur); err != nil {
				return nil, nil, err
			}
			state := cur.Status
			detail := ""
			for _, ch := range cur.Challenges {
				if ch.Type == "http-01" {
					if ch.Error.Detail != "" {
						detail = ch.Error.Detail
					}
				}
			}
			if state == "valid" {
				logger.Info("acme", "域名 %s 验证通过", az.Identifier.Value)
				break
			}
			if state == "invalid" || state == "expired" {
				return nil, nil, fmt.Errorf("域名 %s 验证失败(%s): %s", az.Identifier.Value, state, detail)
			}
			if time.Now().After(deadline) {
				return nil, nil, fmt.Errorf("域名 %s 验证超时，请确保 80 端口可被公网访问", az.Identifier.Value)
			}
			time.Sleep(3 * time.Second)
		}
	}

	// 生成域名私钥与 CSR
	domainKey, err := c.generateDomainKey()
	if err != nil {
		return nil, nil, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: domains,
	}, domainKey)
	if err != nil {
		return nil, nil, err
	}
	final := orderResp{}
	if _, err := c.post(order.Finalize, map[string]string{"csr": b64(csrDER)}, &final); err != nil {
		return nil, nil, fmt.Errorf("提交 CSR 失败: %w", err)
	}

	deadline := time.Now().Add(3 * time.Minute)
	for final.Status != "valid" {
		if time.Now().After(deadline) {
			return nil, nil, fmt.Errorf("订单处理超时，状态=%s", final.Status)
		}
		time.Sleep(3 * time.Second)
		cur := orderResp{}
		if _, err := c.post(orderURL, "", &cur); err != nil {
			return nil, nil, err
		}
		final = cur
		if final.Status == "invalid" {
			return nil, nil, fmt.Errorf("订单被拒绝")
		}
	}

	raw, resp, err := c.postRaw(final.Certificate, "")
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		ae := acmeError{}
		_ = json.Unmarshal(raw, &ae)
		return nil, nil, ae
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(domainKey)
	if err != nil {
		return nil, nil, err
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certPEM = raw
	logger.Info("acme", "证书签发成功，域名: %s", strings.Join(domains, ", "))
	return certPEM, keyPEM, nil
}

func (c *Client) generateDomainKey() (crypto.Signer, error) {
	if c.KeyType == "rsa2048" {
		return rsa.GenerateKey(rand.Reader, 2048)
	}
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

func (c *Client) startChallengeServer(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/acme-challenge/", func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
		token = strings.Trim(token, "/")
		if v, ok := c.lookupToken(token); ok {
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte(v))
			logger.Info("acme", "响应 HTTP-01 挑战: %s", token)
			return
		}
		http.NotFound(w, r)
	})
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return fmt.Errorf("监听 %d 端口失败(HTTP-01 需要 80 端口): %w", port, err)
	}
	c.challengeMux = mux
	c.challengeSrv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go c.challengeSrv.Serve(ln)
	logger.Info("acme", "HTTP-01 挑战服务已启动于 :%d", port)
	return nil
}

func (c *Client) stopChallengeServer() {
	if c.challengeSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		c.challengeSrv.Shutdown(ctx)
		c.challengeSrv = nil
	}
}

// SavePEM 将证书与私钥写入指定文件
func SavePEM(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// CertExpire 读取证书文件的过期时间
func CertExpire(certPath string) (time.Time, error) {
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return time.Time{}, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return time.Time{}, fmt.Errorf("证书文件格式不正确")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return cert.NotAfter, nil
}
