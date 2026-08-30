// Package rsakey 封装登录密码传输用的 RSA-OAEP 动态密钥对管理。
//
// 私钥不落库不落盘，仅以 keyId 关联存 Redis 短期存在（specs §4.1.4 规则2、
// §5 归属边界、04_model_interface「Redis 键设计」）。每个 keyId 对应一次
// 提交，解密成功即删（GETDEL 一次性），TTL 到期自动清除。
package rsakey

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// keyPrefix 是 Redis 键的固定前缀，与 04_model_interface 键格式约定一致。
const keyPrefix = "sili-smart-hr:auth:rsa:"

// rsaKeyBits 是生成密钥对的位长。RSA-3072 的 RSA-OAEP-SHA256 单块明文上限
// 约 318 字节（keySizeBytes - 2*hashSize - 2 = 384 - 64 - 2），覆盖 specs §2.5
// 约定的密钥类字段明文 ≤200 字符；RSA-2048 上限 190 字节不足以覆盖 200 字符。
const rsaKeyBits = 3072

// ErrKeyNotFound 在 keyId 不存在或已过期（Redis 中无对应键）时返回。
var ErrKeyNotFound = errors.New("rsakey: key not found or expired")

// Manager 持有 Redis 客户端与私钥 TTL，负责密钥对生成与一次性解密。
type Manager struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewManager 构造一个 Manager，ttl 由调用方注入（登录场景 5 分钟）。
// rdb 复用 Asynq 已有的 Redis 实例，不单独建连接池（specs §5 归属边界）。
func NewManager(rdb *redis.Client, ttl time.Duration) *Manager {
	return &Manager{rdb: rdb, ttl: ttl}
}

// Generate 生成 RSA 密钥对（位长见 rsaKeyBits）：公钥 PEM 返回前端，私钥 PEM
// 以 keyId 关联写入 Redis。keyID 为 16 字节随机转 32 字符 hex。
func (m *Manager) Generate(ctx context.Context) (publicKeyPEM string, keyID string, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, rsaKeyBits)
	if err != nil {
		return "", "", fmt.Errorf("rsakey: generate key: %w", err)
	}

	keyIDBytes := make([]byte, 16)
	if _, err := rand.Read(keyIDBytes); err != nil {
		return "", "", fmt.Errorf("rsakey: rand key id: %w", err)
	}
	keyID = hex.EncodeToString(keyIDBytes)

	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", "", fmt.Errorf("rsakey: marshal pkcs8: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER}))

	if err := m.rdb.Set(ctx, keyPrefix+keyID, privPEM, m.ttl).Err(); err != nil {
		return "", "", fmt.Errorf("rsakey: set redis: %w", err)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("rsakey: marshal pkix: %w", err)
	}
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER}))

	return pubPEM, keyID, nil
}

// Decrypt 取 keyId 关联的私钥（GETDEL 一次性取出即删），base64 解码密文后
// RSA-OAEP/SHA-256 解密返回明文。keyId 不存在或已过期返回 ErrKeyNotFound；
// 解密失败分支也尝试 Del 该 key 防残留（specs 04_model_interface 失效路径）。
func (m *Manager) Decrypt(ctx context.Context, keyID string, ciphertextB64 string) (plaintext string, err error) {
	privPEM, err := m.rdb.GetDel(ctx, keyPrefix+keyID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", ErrKeyNotFound
		}
		return "", fmt.Errorf("rsakey: getdel: %w", err)
	}
	if privPEM == "" {
		return "", ErrKeyNotFound
	}

	block, _ := pem.Decode([]byte(privPEM))
	if block == nil {
		_ = m.rdb.Del(ctx, keyPrefix+keyID).Err()
		return "", errors.New("rsakey: invalid private key PEM in redis")
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		_ = m.rdb.Del(ctx, keyPrefix+keyID).Err()
		return "", fmt.Errorf("rsakey: parse pkcs8: %w", err)
	}
	priv, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		_ = m.rdb.Del(ctx, keyPrefix+keyID).Err()
		return "", fmt.Errorf("rsakey: stored key is not RSA: %T", keyAny)
	}

	cipherBytes, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		_ = m.rdb.Del(ctx, keyPrefix+keyID).Err()
		return "", fmt.Errorf("rsakey: base64 decode: %w", err)
	}

	plainBytes, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, cipherBytes, nil)
	if err != nil {
		_ = m.rdb.Del(ctx, keyPrefix+keyID).Err()
		return "", fmt.Errorf("rsakey: decrypt oaep: %w", err)
	}

	return string(plainBytes), nil
}
