package rsakey_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"sili-smart-hr/backend/internal/pkg/rsakey"
)

// newTestManager 起一个内存 Redis 并构造 Manager，返回三者供测试驱动。
func newTestManager(t *testing.T, ttl time.Duration) (*rsakey.Manager, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis run: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rsakey.NewManager(rdb, ttl), rdb, mr
}

// encryptWithPEM 用公钥 PEM 做 RSA-OAEP/SHA-256 加密，返回 base64 密文，与前端约定一致。
func encryptWithPEM(t *testing.T, pubPEM, plaintext string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		t.Fatal("invalid public key PEM")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse PKIX public key: %v", err)
	}
	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("not an RSA public key: %T", pubAny)
	}
	cipherBytes, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, []byte(plaintext), nil)
	if err != nil {
		t.Fatalf("encrypt oaep: %v", err)
	}
	return base64.StdEncoding.EncodeToString(cipherBytes)
}

// TestManager_GenerateAndDecrypt 覆盖核心断言：公钥 PEM 含标头、keyID 32 字符、
// 加密 hzwlsoft.com 后解密回原文、解密后 Redis 键已删（一次性）。
func TestManager_GenerateAndDecrypt(t *testing.T) {
	ctx := context.Background()
	m, rdb, _ := newTestManager(t, 5*time.Minute)

	pubPEM, keyID, err := m.Generate(ctx)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(pubPEM, "BEGIN PUBLIC KEY") {
		t.Fatalf("publicKeyPEM missing PUBLIC KEY header: %q", pubPEM)
	}
	if len(keyID) != 32 {
		t.Fatalf("keyID length = %d, want 32", len(keyID))
	}

	cipher := encryptWithPEM(t, pubPEM, "hzwlsoft.com")

	plain, err := m.Decrypt(ctx, keyID, cipher)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain != "hzwlsoft.com" {
		t.Fatalf("plaintext = %q, want hzwlsoft.com", plain)
	}

	// 解密成功即删，键应已不存在。
	_, err = rdb.Get(ctx, "sili-smart-hr:auth:rsa:"+keyID).Result()
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("expected redis.Nil after decrypt, got err=%v", err)
	}
}

// TestManager_DecryptUnknownKey 覆盖未知 keyID 返回 ErrKeyNotFound。
func TestManager_DecryptUnknownKey(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newTestManager(t, 5*time.Minute)

	_, err := m.Decrypt(ctx, "nonexistenthex1234567890", "x")
	if !errors.Is(err, rsakey.ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

// TestManager_DecryptReuseFails 验证一次性语义：同一 keyID 解密成功后再次解密应返 ErrKeyNotFound。
func TestManager_DecryptReuseFails(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newTestManager(t, 5*time.Minute)

	pubPEM, keyID, err := m.Generate(ctx)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	cipher := encryptWithPEM(t, pubPEM, "hzwlsoft.com")

	if _, err := m.Decrypt(ctx, keyID, cipher); err != nil {
		t.Fatalf("first decrypt: %v", err)
	}
	if _, err := m.Decrypt(ctx, keyID, cipher); !errors.Is(err, rsakey.ErrKeyNotFound) {
		t.Fatalf("second decrypt expected ErrKeyNotFound, got %v", err)
	}
}

// TestManager_DecryptInvalidCiphertext 验证密文损坏分支：key 存在但密文非法，
// 解密应失败，且失败后键也应被清理防残留（04_model_interface 失效路径约定）。
func TestManager_DecryptInvalidCiphertext(t *testing.T) {
	ctx := context.Background()
	m, rdb, _ := newTestManager(t, 5*time.Minute)

	_, keyID, err := m.Generate(ctx)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// 合法 base64 但解不开的密文。
	if _, err := m.Decrypt(ctx, keyID, "AAAAAAAAAAAAAAAAAAAAAA=="); err == nil {
		t.Fatal("expected decrypt error for invalid ciphertext")
	}
	// 解密失败也尝试删除防残留。
	_, err = rdb.Get(ctx, "sili-smart-hr:auth:rsa:"+keyID).Result()
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("expected redis.Nil after failed decrypt, got err=%v", err)
	}
}

// TestManager_DecryptInvalidBase64 验证非 base64 输入走解码失败分支并被清理。
func TestManager_DecryptInvalidBase64(t *testing.T) {
	ctx := context.Background()
	m, rdb, _ := newTestManager(t, 5*time.Minute)

	_, keyID, err := m.Generate(ctx)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := m.Decrypt(ctx, keyID, "not-base64!!!"); err == nil {
		t.Fatal("expected error for non-base64 ciphertext")
	}
	_, err = rdb.Get(ctx, "sili-smart-hr:auth:rsa:"+keyID).Result()
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("expected redis.Nil after failed decrypt, got err=%v", err)
	}
}

// TestRSA3072 验证密钥位长升到 3072：公钥解析出的 N.BitLen()==3072，
// 且 RSA-OAEP-SHA256 单块明文上限（384-64-2=318 字节）覆盖 specs §2.5
// 约定的密钥类字段 ≤200 字符。用 200 字符明文做完整加解密往返断言。
func TestRSA3072(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newTestManager(t, 5*time.Minute)

	pubPEM, keyID, err := m.Generate(ctx)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	// 断言位长 3072：解析 PEM 拿公钥，校验 modulus 位长。
	block, _ := pem.Decode([]byte(pubPEM))
	if block == nil {
		t.Fatal("invalid public key PEM")
	}
	pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse PKIX public key: %v", err)
	}
	pub, ok := pubAny.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("not an RSA public key: %T", pubAny)
	}
	if got := pub.N.BitLen(); got != 3072 {
		t.Fatalf("public key bitlen = %d, want 3072", got)
	}

	// 200 字符明文完整加解密往返：RSA-2048 上限 190 字节会加密失败，3072 覆盖。
	plaintext := strings.Repeat("a", 200)
	cipher := encryptWithPEM(t, pubPEM, plaintext)

	plain, err := m.Decrypt(ctx, keyID, cipher)
	if err != nil {
		t.Fatalf("decrypt 200-byte plaintext: %v", err)
	}
	if plain != plaintext {
		t.Fatalf("plaintext round-trip mismatch: got len=%d, want 200", len(plain))
	}
}

// TestManager_KeyIDHexOnly 验证 keyID 是合法 hex（防注入与拼写偏差）。
func TestManager_KeyIDHexOnly(t *testing.T) {
	ctx := context.Background()
	m, _, _ := newTestManager(t, 5*time.Minute)

	for i := 0; i < 8; i++ {
		_, keyID, err := m.Generate(ctx)
		if err != nil {
			t.Fatalf("generate #%d: %v", i, err)
		}
		if len(keyID) != 32 {
			t.Fatalf("keyID length = %d, want 32", len(keyID))
		}
		for _, c := range keyID {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Fatalf("keyID %q contains non-hex char %q", keyID, c)
			}
		}
	}
}
