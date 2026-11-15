package httpapi

import (
	"regexp"
	"testing"
)

func TestGenerateDeviceID(t *testing.T) {
	pattern := regexp.MustCompile(`^FFF-[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{8}$`)
	seen := make(map[string]struct{}, 256)
	for i := 0; i < 256; i++ {
		id, err := generateDeviceID()
		if err != nil {
			t.Fatalf("generateDeviceID: %v", err)
		}
		if !pattern.MatchString(id) {
			t.Fatalf("generated id %q does not match expected format", id)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("generated duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}
