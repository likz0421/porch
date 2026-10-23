// Package lkcf 解析 Lucky 导出的 .lkcf 配置文件，用于把 Lucky 的动态域名与证书任务迁移到 Porch。
//
// 支持两种格式（均为"密钥随文件保存"，无需用户口令即可解密）：
//
//	V1（2.x）：[0]=0x01 | [1:33] AES-256 密钥 | [33:49] IV | [49:] AES-256-CBC 密文，可选 gzip
//	V2（3.x）：[0:4]="LKCF" | [4]=0x02 | [6] 标志位(bit0=zstd) | [8:12] 载荷长度 |
//	           [16:18] 头部 CRC16 | [18:22] 载荷 CRC32 | [24:] 载荷(密钥32 + GCM随机数12 + 密文与标签)
//
// 格式说明依据公开可观察的样本推导，本实现为独立编写。
package lkcf

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os/exec"
)

const (
	versionV1 = 0x01
	versionV2 = 0x02
	keySize   = 32
	ivSize    = aes.BlockSize
	headerV1  = 1 + keySize + ivSize
	headerV2  = 24
	nonceV2   = 12
	flagZstd  = 0x01
	maxPlain  = 64 << 20
)

// Document 解密后的 Lucky 配置，按顶层字段拆分
type Document map[string]json.RawMessage

// Decrypt 解密 .lkcf 内容并返回 JSON 明文；输入本身是 JSON 时原样返回
func Decrypt(data []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return trimmed, nil
	}
	if len(trimmed) >= 4 && string(trimmed[:4]) == "LKCF" {
		plain, err := decryptV2(trimmed)
		if err != nil {
			return nil, err
		}
		return finalize(plain)
	}
	// V1: AES-256-CBC
	if len(data) < headerV1+aes.BlockSize {
		return nil, errors.New("文件太短，不是有效的 lkcf")
	}
	if data[0] != versionV1 {
		return nil, fmt.Errorf("不支持的 lkcf 版本 %d", data[0])
	}
	key := data[1 : 1+keySize]
	iv := data[1+keySize : headerV1]
	ct := data[headerV1:]
	if len(ct)%aes.BlockSize != 0 {
		return nil, errors.New("密文长度不是分组大小的整数倍")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	plain, err = pkcs7Unpad(plain)
	if err != nil {
		return nil, err
	}
	return finalize(plain)
}

func finalize(plain []byte) ([]byte, error) {
	out, err := decompress(plain)
	if err != nil {
		return nil, err
	}
	if !json.Valid(bytes.TrimSpace(out)) {
		return nil, errors.New("解密结果不是 JSON，文件可能已损坏")
	}
	return bytes.TrimSpace(out), nil
}

func decompress(b []byte) ([]byte, error) {
	if len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(io.LimitReader(zr, maxPlain))
	}
	if len(b) >= 4 && b[0] == 0x28 && b[1] == 0xb5 && b[2] == 0x2f && b[3] == 0xfd {
		return zstdDecompress(b)
	}
	return b, nil
}

// zstdDecompress 解压 zstd 数据。标准库不含 zstd，优先调用系统中的 zstd 命令，
// 找不到时返回明确提示，让用户自行安装或用明文 JSON 导入。
func zstdDecompress(b []byte) ([]byte, error) {
	zstdBin, err := exec.LookPath("zstd")
	if err != nil {
		return nil, errors.New("该 lkcf 使用 zstd 压缩：请先在设备上安装 zstd（opkg install zstd）后重试，或改用 Lucky 导出的明文 JSON")
	}
	cmd := exec.Command(zstdBin, "-d", "-c")
	cmd.Stdin = bytes.NewReader(b)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("zstd 解压失败: %v", err)
	}
	return out.Bytes(), nil
}

func crc16CCITT(b []byte) uint16 {
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

func decryptV2(data []byte) ([]byte, error) {
	if len(data) < headerV2+keySize+nonceV2+16 {
		return nil, errors.New("文件太短，不是有效的 lkcf V2")
	}
	if crc16CCITT(data[:16]) != binary.BigEndian.Uint16(data[16:18]) {
		return nil, errors.New("lkcf 头部校验失败，文件可能已损坏")
	}
	if data[4] != versionV2 {
		return nil, fmt.Errorf("不支持的 lkcf V2 版本 %d", data[4])
	}
	n := uint64(binary.BigEndian.Uint32(data[8:12]))
	if n < keySize+nonceV2+16 || n > uint64(len(data)-headerV2) {
		return nil, errors.New("lkcf 载荷长度无效")
	}
	payload := data[headerV2 : headerV2+int(n)]
	if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(data[18:22]) {
		return nil, errors.New("lkcf 载荷校验失败，文件可能已损坏")
	}
	block, err := aes.NewCipher(payload[:keySize])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := payload[keySize : keySize+nonceV2]
	plain, err := gcm.Open(nil, nonce, payload[keySize+nonceV2:], nil)
	if err != nil {
		return nil, fmt.Errorf("lkcf 解密失败: %w", err)
	}
	return plain, nil
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("明文为空")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize || n > len(b) {
		return nil, errors.New("PKCS#7 填充无效")
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errors.New("PKCS#7 填充无效")
		}
	}
	return b[:len(b)-n], nil
}

// Parse 解密并拆分顶层字段
func Parse(data []byte) (Document, error) {
	plain, err := Decrypt(data)
	if err != nil {
		return nil, err
	}
	var doc Document
	if err := json.Unmarshal(plain, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}
