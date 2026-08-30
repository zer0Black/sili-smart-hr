// Package crypto 封装敏感配置项的对称加解密与展示掩码。
//
// 大模型 API Key、第三方集成密钥等敏感串禁止明文落库（specs §7、
// 04_model_interface §1.6），统一经 AES-256-GCM 加密后以 base64 写 TEXT 列。
// DeriveKey 把 config.LLM.SecretKey 派生为 32 字节 key；Encrypt 每次随机
// nonce，输出单段 base64(nonce+ciphertext)；Mask 给前端/日志提供前4末4
// 保留、中段星号的掩码快照（specs §4.2.4 规则4、03_architecture §2.5）。
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// DeriveKey 把任意长度密钥字符串派生为 AES-256 的 32 字节 key（SHA-256）。
// 调用方从 config.LLM.SecretKey 取字符串传入。
func DeriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// Encrypt 用 AES-256-GCM 加密明文，返回 base64 编码的 nonce+ciphertext。
// nonce 每次随机生成 12 字节（gcm.NonceSize），与密文一起 base64 单段编码。
func Encrypt(key []byte, plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: new aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: new gcm: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("crypto: rand nonce: %w", err)
	}

	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt 解密 Encrypt 产出的 base64 串，返回明文。
// 格式不符（base64 失败或负载短于 nonce）或 GCM 校验失败（密文被篡改/key 不符）均返 error。
func Decrypt(key []byte, ciphertextB64 string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return "", fmt.Errorf("crypto: base64 decode: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("crypto: new aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: new gcm: %w", err)
	}

	if len(raw) < gcm.NonceSize() {
		return "", errors.New("crypto: ciphertext too short")
	}
	nonce := raw[:gcm.NonceSize()]
	ciphertext := raw[gcm.NonceSize():]

	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("crypto: gcm open: %w", err)
	}
	return string(plain), nil
}

// Mask 生成密钥掩码快照：保留前 4 位与末 4 位，中段以星号替代；
// 明文长度 <= 8 时全掩码（星号数为明文长度）；空串返回空串。
// 掩码只用于展示与日志，密文落库、掩码不参与加解密。
// 按 rune 计数与切片，多字节字符（如中文）不被切断产生乱码。
func Mask(plaintext string) string {
	runes := []rune(plaintext)
	n := len(runes)
	if n == 0 {
		return ""
	}
	if n <= 8 {
		return strings.Repeat("*", n)
	}
	return string(runes[:4]) + strings.Repeat("*", n-8) + string(runes[n-4:])
}
