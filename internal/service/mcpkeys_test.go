package service

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestGenerateMCPKeyFormat(t *testing.T) {
	generated, err := generateMCPKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	prefix, secret, ok := ParseMCPToken(generated.Token)
	if !ok {
		t.Fatalf("generated token %q is not parseable", generated.Token)
	}
	if prefix != generated.Prefix {
		t.Fatalf("prefix = %q, want %q", prefix, generated.Prefix)
	}
	if len(secret) != 43 {
		t.Fatalf("secret length = %d, want 43", len(secret))
	}
	if !strings.HasPrefix(generated.Token, "mcp_") {
		t.Fatalf("token = %q, want mcp_ prefix", generated.Token)
	}
	if got, want := hashMCPSecret(secret), generated.Hash; !bytes.Equal(got, want) {
		t.Fatalf("hash mismatch: got %x want %x", got, want)
	}
	if normalized, ok := NormalizeMCPKeyPrefix(generated.Prefix); !ok || normalized != generated.Prefix {
		t.Fatalf("NormalizeMCPKeyPrefix(%q) = %q, %t", generated.Prefix, normalized, ok)
	}
}

func TestGenerateMCPKeyIsUnique(t *testing.T) {
	seen := make(map[string]struct{}, 20)
	for i := 0; i < 20; i++ {
		generated, err := generateMCPKey()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if _, exists := seen[generated.Token]; exists {
			t.Fatalf("duplicate token generated: %q", generated.Token)
		}
		seen[generated.Token] = struct{}{}
	}
}

func TestParseMCPToken(t *testing.T) {
	valid := "mcp_0123456789ab_" + strings.Repeat("a", 43)
	prefix, secret, ok := ParseMCPToken("  " + valid + "  ")
	if !ok {
		t.Fatal("expected valid token to parse")
	}
	if prefix != "mcp_0123456789ab" {
		t.Fatalf("prefix = %q", prefix)
	}
	if secret != strings.Repeat("a", 43) {
		t.Fatalf("secret = %q", secret)
	}

	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"missing prefix", "0123456789ab_secret"},
		{"wrong prefix", "key_0123456789ab_secret"},
		{"short hex", "mcp_0123456789_secret"},
		{"long hex", "mcp_0123456789abc_secret"},
		{"non hex", "mcp_zzzzzzzzzzzz_secret"},
		{"missing separator", "mcp_0123456789ab"},
		{"empty secret", "mcp_0123456789ab_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := ParseMCPToken(tc.token); ok {
				t.Fatalf("expected %q to be rejected", tc.token)
			}
		})
	}
}

func TestNormalizeMCPKeyPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"mcp_0123456789ab", "mcp_0123456789ab", true},
		{"MCP_0123456789AB", "mcp_0123456789ab", true},
		{"0123456789ab", "mcp_0123456789ab", true},
		{"  mcp_0123456789ab  ", "mcp_0123456789ab", true},
		{"", "", false},
		{"mcp_", "", false},
		{"mcp_0123", "", false},
		{"mcp_zzzzzzzzzzzz", "", false},
		{"mcp_0123456789ab_extra", "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizeMCPKeyPrefix(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("NormalizeMCPKeyPrefix(%q) = %q, %t; want %q, %t", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestNormalizeMCPKeyName(t *testing.T) {
	if got, err := normalizeMCPKeyName("  agen lapangan  "); err != nil || got != "agen lapangan" {
		t.Fatalf("name = %q, err = %v", got, err)
	}
	if _, err := normalizeMCPKeyName("   "); err == nil {
		t.Fatal("expected error for blank name")
	}
	if _, err := normalizeMCPKeyName(strings.Repeat("a", mcpKeyNameMaxLen+1)); err == nil {
		t.Fatal("expected error for too long name")
	}
	if _, err := normalizeMCPKeyName(strings.Repeat("a", mcpKeyNameMaxLen)); err != nil {
		t.Fatalf("max length name rejected: %v", err)
	}
}

func TestNormalizeMCPKeyExpiry(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	if got, err := normalizeMCPKeyExpiry(nil, now); err != nil || got != nil {
		t.Fatalf("nil expiry = %v, %v", got, err)
	}

	past := now.Add(-time.Hour)
	if _, err := normalizeMCPKeyExpiry(&past, now); err == nil {
		t.Fatal("expected error for past expiry")
	}

	future := now.Add(time.Hour).In(time.FixedZone("WIB", 7*60*60))
	got, err := normalizeMCPKeyExpiry(&future, now)
	if err != nil {
		t.Fatalf("future expiry: %v", err)
	}
	if got.Location() != time.UTC {
		t.Fatalf("expiry location = %v, want UTC", got.Location())
	}
	if !got.Equal(future) {
		t.Fatalf("expiry = %v, want %v", got, future)
	}
}
