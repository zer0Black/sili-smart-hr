package crypto_test

import (
	"strings"
	"testing"

	"sili-smart-hr/backend/internal/pkg/crypto"
)

// TestDeriveKey_LengthAndDeterminism 覆盖 DeriveKey：
// 返回 32 字节满足 AES-256；同一 secret 派生稳定；不同 secret 派生不同。
func TestDeriveKey_LengthAndDeterminism(t *testing.T) {
	k1 := crypto.DeriveKey("any-secret")
	if len(k1) != 32 {
		t.Fatalf("DeriveKey length = %d, want 32 (AES-256)", len(k1))
	}

	k1Again := crypto.DeriveKey("any-secret")
	if string(k1) != string(k1Again) {
		t.Fatal("DeriveKey not deterministic for same secret")
	}

	k2 := crypto.DeriveKey("other-secret")
	if string(k1) == string(k2) {
		t.Fatal("DeriveKey collided for different secrets")
	}
}

// TestEncryptDecrypt_RoundTrip 覆盖核心契约：Encrypt 后 Decrypt 还原明文。
func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := crypto.DeriveKey("any-secret")
	pt := "sk-1a2b3c4d5e6f7890"

	ct, err := crypto.Encrypt(key, pt)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	got, err := crypto.Decrypt(key, ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != pt {
		t.Fatalf("round trip = %q, want %q", got, pt)
	}
}

// TestEncrypt_NonceRandomness 验证同一明文两次加密产生不同密文（nonce 随机）。
func TestEncrypt_NonceRandomness(t *testing.T) {
	key := crypto.DeriveKey("any-secret")
	pt := "sk-1a2b3c4d5e6f7890"

	ct1, err := crypto.Encrypt(key, pt)
	if err != nil {
		t.Fatalf("Encrypt #1: %v", err)
	}
	ct2, err := crypto.Encrypt(key, pt)
	if err != nil {
		t.Fatalf("Encrypt #2: %v", err)
	}
	if ct1 == ct2 {
		t.Fatalf("expected different ciphertexts due to random nonce, got identical %q", ct1)
	}

	// 两份密文都应能解回同一明文，进一步证明 nonce 差异非损坏。
	if got, _ := crypto.Decrypt(key, ct1); got != pt {
		t.Fatalf("ct1 decrypt = %q, want %q", got, pt)
	}
	if got, _ := crypto.Decrypt(key, ct2); got != pt {
		t.Fatalf("ct2 decrypt = %q, want %q", got, pt)
	}
}

// TestDecrypt_WrongKeyFails 验证错误 key 解密失败返回非 nil error（GCM 校验）。
func TestDecrypt_WrongKeyFails(t *testing.T) {
	key1 := crypto.DeriveKey("secret-one")
	key2 := crypto.DeriveKey("secret-two")
	pt := "sk-1a2b3c4d5e6f7890"

	ct, err := crypto.Encrypt(key1, pt)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := crypto.Decrypt(key2, ct); err == nil {
		t.Fatal("expected Decrypt with wrong key to fail, got nil")
	}
}

// TestDecrypt_InvalidBase64 验证非 base64 输入返 error。
func TestDecrypt_InvalidBase64(t *testing.T) {
	key := crypto.DeriveKey("any-secret")
	if _, err := crypto.Decrypt(key, "not-base64!!!"); err == nil {
		t.Fatal("expected error for non-base64 ciphertext, got nil")
	}
}

// TestDecrypt_TamperedCiphertext 验证密文被篡改后 GCM 校验失败。
func TestDecrypt_TamperedCiphertext(t *testing.T) {
	key := crypto.DeriveKey("any-secret")
	ct, err := crypto.Encrypt(key, "sk-1a2b3c4d5e6f7890")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// 翻转最后一位字符破坏密文（保持 base64 合法）。
	tampered := ct[:len(ct)-2] + "AA"
	if tampered == ct {
		tampered = ct[:len(ct)-2] + "BB"
	}
	if _, err := crypto.Decrypt(key, tampered); err == nil {
		t.Fatal("expected error for tampered ciphertext, got nil")
	}
}

// TestDecrypt_TruncatedPayload 验证负载短于 nonce 长度时返 error。
func TestDecrypt_TruncatedPayload(t *testing.T) {
	key := crypto.DeriveKey("any-secret")
	// 5 字节 base64 解码后远短于 12 字节 nonce。
	if _, err := crypto.Decrypt(key, "AAAAAAA"); err == nil {
		t.Fatal("expected error for payload shorter than nonce, got nil")
	}
}

// TestEncrypt_EmptyPlaintext 验证空串仍可加解密（GCM 允许零长度明文）。
func TestEncrypt_EmptyPlaintext(t *testing.T) {
	key := crypto.DeriveKey("any-secret")
	ct, err := crypto.Encrypt(key, "")
	if err != nil {
		t.Fatalf("Encrypt empty: %v", err)
	}
	got, err := crypto.Decrypt(key, ct)
	if err != nil {
		t.Fatalf("Decrypt empty: %v", err)
	}
	if got != "" {
		t.Fatalf("empty round trip = %q, want empty", got)
	}
}

// TestMask_Table 覆盖 Mask 三类分支：长串前4末4保留、短串全掩码、空串。
func TestMask_Table(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "long retains first4 and last4",
			in:   "sk-1a2b3c4d5e6f7890ab12",
			want: "sk-1" + strings.Repeat("*", len("sk-1a2b3c4d5e6f7890ab12")-8) + "ab12",
		},
		{
			name: "short masked fully",
			in:   "short",
			want: "*****",
		},
		{
			name: "empty returns empty",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := crypto.Mask(tc.in); got != tc.want {
				t.Fatalf("Mask(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestMask_BoundaryAtEight 验证 len==8 边界：恰好 8 字符时全掩码，
// 避免前 4 + 空 + 末 4 等于原文导致脱敏失效。
func TestMask_BoundaryAtEight(t *testing.T) {
	in := "12345678"
	want := "********"
	if got := crypto.Mask(in); got != want {
		t.Fatalf("Mask(len==8) = %q, want %q (fully masked)", got, want)
	}
}

// TestMask_PreservesLength 验证掩码不改变长度（前4末4保留语义下的不变量）。
func TestMask_PreservesLength(t *testing.T) {
	for _, in := range []string{"sk-1a2b3c4d5e6f7890ab12", "abcdef", "abcdefgh"} {
		if got := crypto.Mask(in); len(got) != len(in) {
			t.Fatalf("Mask(%q) length = %d, want %d", in, len(got), len(in))
		}
	}
}

// TestMask_MultibyteRunes 验证多字节字符按 rune 计数切片，不因 UTF-8 字节边界切断产生乱码。
// 8 个中文字符（24 字节）按 rune 计数 n=8，走全掩码分支；长度按 rune 数计。
func TestMask_MultibyteRunes(t *testing.T) {
	in := "一二三四五六七八"
	got := crypto.Mask(in)
	// 按 rune 数计 n=8，走全掩码分支。
	want := strings.Repeat("*", 8)
	if got != want {
		t.Fatalf("Mask(8 runes) = %q, want %q", got, want)
	}
	// 10 个中文字符：前4末4保留，中段 2 个星号（按 rune 数）。
	in2 := "一二三四五六七八九十"
	got2 := crypto.Mask(in2)
	want2 := "一二三四" + "**" + "七八九十"
	if got2 != want2 {
		t.Fatalf("Mask(10 runes) = %q, want %q", got2, want2)
	}
	// 结果字符数应等于原 rune 数，而非字节数。
	if ruCount([]rune(got2)) != ruCount([]rune(in2)) {
		t.Fatalf("rune count changed: got %d want %d", ruCount([]rune(got2)), ruCount([]rune(in2)))
	}
}

func ruCount(rs []rune) int { return len(rs) }
