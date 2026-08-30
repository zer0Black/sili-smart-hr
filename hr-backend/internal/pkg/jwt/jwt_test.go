package jwt_test

import (
	"testing"
	"time"

	"sili-smart-hr/backend/internal/pkg/jwt"
)

func TestGenerateAndParseRoundTrip(t *testing.T) {
	m := jwt.NewManager("topsecret", time.Hour)
	tok, err := m.Generate(42, "admin")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if tok == "" {
		t.Fatal("empty token")
	}
	c, err := m.Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.AccountID != 42 || c.Username != "admin" {
		t.Fatalf("unexpected claims: %+v", c)
	}
}

func TestParseExpired(t *testing.T) {
	m := jwt.NewManager("topsecret", -time.Hour) // 签发即过期
	tok, err := m.Generate(1, "admin")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := m.Parse(tok); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestParseTampered(t *testing.T) {
	m := jwt.NewManager("topsecret", time.Hour)
	tok, _ := m.Generate(1, "admin")
	if _, err := m.Parse(tok + "tamper"); err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestParseWrongSecret(t *testing.T) {
	signer := jwt.NewManager("topsecret", time.Hour)
	verifier := jwt.NewManager("other", time.Hour)
	tok, _ := signer.Generate(1, "admin")
	if _, err := verifier.Parse(tok); err == nil {
		t.Fatal("expected signature mismatch error")
	}
}
