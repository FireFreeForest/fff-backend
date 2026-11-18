package repository

import "testing"

func TestIlikePatternEscapesWildcards(t *testing.T) {
	tests := []struct {
		query string
		want  string
	}{
		{"hutan", "%hutan%"},
		{"100%", `%100\%%`},
		{"a_b", `%a\_b%`},
		{`a\b`, `%a\\b%`},
	}
	for _, tt := range tests {
		if got := ilikePattern(tt.query); got != tt.want {
			t.Fatalf("ilikePattern(%q) = %q, want %q", tt.query, got, tt.want)
		}
	}
}
