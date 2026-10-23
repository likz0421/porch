package lkcf

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

// crc16 与实现保持一致（测试内独立实现，用于构造样本）
func crc16(b []byte) uint16 {
	var crc uint16
	for _, c := range b {
		crc ^= uint16(c) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// encryptV2 按 Lucky 3.x 的 LKCF V2 格式封装
func encryptV2(t *testing.T, plain []byte) []byte {
	t.Helper()
	key := make([]byte, 32)
	nonce := make([]byte, 12)
	rand.Read(key)
	rand.Read(nonce)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nil, nonce, plain, nil)

	payload := append(append(append([]byte{}, key...), nonce...), ct...)

	out := []byte("LKCF")
	out = append(out, 0x02, 0x00, 0x00, 0x00) // 版本、保留、标志位(未压缩)
	lb := make([]byte, 4)
	binary.BigEndian.PutUint32(lb, uint32(len(payload)))
	out = append(out, lb...)
	binary.BigEndian.PutUint32(lb, uint32(len(plain)))
	out = append(out, lb...)
	cb := make([]byte, 2)
	binary.BigEndian.PutUint16(cb, crc16(out[:16]))
	out = append(out, cb...)
	c32 := make([]byte, 4)
	binary.BigEndian.PutUint32(c32, crc32.ChecksumIEEE(payload))
	out = append(out, c32...)
	out = append(out, 0x00, 0x00)
	return append(out, payload...)
}

// encryptV1 按 Lucky 2.x 的 V1 格式封装（AES-256-CBC + PKCS#7）
func encryptV1(t *testing.T, plain []byte) []byte {
	t.Helper()
	key := make([]byte, 32)
	iv := make([]byte, 16)
	rand.Read(key)
	rand.Read(iv)
	pad := aes.BlockSize - len(plain)%aes.BlockSize
	padded := append(append([]byte{}, plain...), make([]byte, pad)...)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(pad)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	out := []byte{0x01}
	out = append(out, key...)
	out = append(out, iv...)
	return append(out, ct...)
}

func TestDecryptV2(t *testing.T) {
	plain := []byte(`{"DDNSTaskList":[]}`)
	got, err := Decrypt(encryptV2(t, plain))
	if err != nil {
		t.Fatalf("V2 解密失败: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("V2 明文不一致:\n got=%s\nwant=%s", got, plain)
	}
}

func TestDecryptV1(t *testing.T) {
	plain := []byte(`{"SSLCertficateList":[]}`)
	got, err := Decrypt(encryptV1(t, plain))
	if err != nil {
		t.Fatalf("V1 解密失败: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("V1 明文不一致:\n got=%s\nwant=%s", got, plain)
	}
}

func TestDecryptPlainJSON(t *testing.T) {
	raw := []byte(`{"DDNSTaskList":[]}`)
	got, err := Decrypt(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatal("明文 JSON 应原样返回")
	}
}

func TestDecryptBadCRC(t *testing.T) {
	data := encryptV2(t, []byte(`{}`))
	data[len(data)-1] ^= 0xff // 破坏密文，触发 GCM 校验失败
	if _, err := Decrypt(data); err == nil {
		t.Fatal("篡改后的数据应解密失败")
	}
}

const sampleDDNS = `{
  "DDNSTaskList": [
    {
      "TaskName": "家里宽带",
      "Enable": true,
      "TaskType": "IPv4",
      "Intervals": 600,
      "V4QueryIPEnable": true,
      "V4QueryIPType": "netInterface",
      "V4NetInterface": "pppoe-wan",
      "Records": [
        {"Key":"r1","Disable":false,"SyncRecordData":{"DomainName":"example.com","SubDomainName":"home","type":"A","ttl":600}}
      ],
      "DNS": {"Name":"cloudflare","ID":"","Secret":"cf-token","ForceInterval":24},
      "WebhookEnable": true,
      "WebhookURL": "https://hook.example.com/notify",
      "WebhookMethod": "POST"
    }
  ]
}`

func TestImportDDNS(t *testing.T) {
	doc, err := Parse(encryptV2(t, []byte(sampleDDNS)))
	if err != nil {
		t.Fatal(err)
	}
	tasks, rep := ImportDDNS(doc, false)
	if rep == nil || rep.Found != 1 {
		t.Fatalf("应发现 1 条任务，实际 %+v", rep)
	}
	if len(tasks) != 1 {
		t.Fatalf("应转换出 1 条任务，实际 %d", len(tasks))
	}
	task := tasks[0]
	if task.Domain != "example.com" || task.SubDomain != "home" {
		t.Fatalf("域名解析错误: %+v", task)
	}
	if task.Provider != "cloudflare" || task.AccessKey != "cf-token" {
		t.Fatalf("服务商或凭据错误: %+v", task)
	}
	if task.IPSource != "interface" || task.InterfaceName != "pppoe-wan" {
		t.Fatalf("IP 来源转换错误: %+v", task)
	}
	if task.CheckInterval != 600 || task.ForceInterval != 24 || task.TTL != 600 {
		t.Fatalf("间隔/TTL 转换错误: %+v", task)
	}
	if task.WebhookURL != "POST https://hook.example.com/notify" {
		t.Fatalf("Webhook 转换错误: %q", task.WebhookURL)
	}
	if task.Enable {
		t.Fatal("keepEnabled=false 时导入的任务应停用")
	}
	// keepEnabled=true 且 Lucky 中启用 → 保持启用
	_, rep2 := ImportDDNS(doc, true)
	tasks2, _ := ImportDDNS(doc, true)
	if len(tasks2) != 1 || !tasks2[0].Enable {
		t.Fatalf("keepEnabled=true 时应保持启用: %+v", tasks2)
	}
	_ = rep2
}

func TestUnknownProviderWarning(t *testing.T) {
	raw := `{"DDNSTaskList":[{"TaskName":"x","Enable":true,"DNS":{"Name":"someprovider"},"Records":[{"SyncRecordData":{"DomainName":"a.com","SubDomainName":"b","type":"A"}}]}]}`
	doc, err := Parse(encryptV2(t, []byte(raw)))
	if err != nil {
		t.Fatal(err)
	}
	_, rep := ImportDDNS(doc, true)
	if rep == nil || len(rep.Warnings) == 0 {
		t.Fatal("未知服务商应产生告警")
	}
}

func TestSplitFQDN(t *testing.T) {
	cases := map[string][2]string{
		"home.example.com": {"home", "example.com"},
		"example.com":      {"", "example.com"},
		"a.b.example.com":  {"a.b", "example.com"},
	}
	for in, want := range cases {
		sub, zone := splitFQDN(in)
		if sub != want[0] || zone != want[1] {
			t.Fatalf("%s -> (%s,%s)，期望 (%s,%s)", in, sub, zone, want[0], want[1])
		}
	}
}
