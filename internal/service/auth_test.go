package service

import (
	"strings"
	"testing"
)

func TestPasswordHashing(t *testing.T) {
	svc := &AuthService{}

	hash, err := svc.HashPassword("rahasia12345")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if hash == "rahasia12345" {
		t.Fatal("password hash must not equal plaintext")
	}
	if !strings.HasPrefix(hash, "$2a$12$") && !strings.HasPrefix(hash, "$2b$12$") {
		t.Fatalf("unexpected bcrypt hash prefix: %s", hash)
	}
	if !svc.CheckPassword(hash, "rahasia12345") {
		t.Fatal("correct password should match")
	}
	if svc.CheckPassword(hash, "rahasia12346") {
		t.Fatal("wrong password must not match")
	}
}

func TestHashTokenDeterministic(t *testing.T) {
	a := hashToken("token-abc")
	b := hashToken("token-abc")
	c := hashToken("token-abd")
	if a != b {
		t.Fatal("same token must produce same hash")
	}
	if a == c {
		t.Fatal("different token must produce different hash")
	}
	if len(a) != 64 {
		t.Fatalf("sha256 hex length = %d, want 64", len(a))
	}
}
