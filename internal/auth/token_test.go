package auth

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestRandomOpaqueAndSHA256(t *testing.T) {
	first, err := RandomOpaque("tok_", 32)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RandomOpaque("tok_", 32)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "tok_") {
		t.Fatalf("opaque values are not distinct/prefixed")
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(first, "tok_"))
	if err != nil || len(raw) != 32 {
		t.Fatalf("opaque value is not canonical 32-byte base64url: len=%d err=%v", len(raw), err)
	}
	if got := SHA256("known"); len(got) != 32 || string(got) == "known" {
		t.Fatalf("unexpected digest")
	}
}

func TestRandomOpaqueRejectsUnsafeSizes(t *testing.T) {
	for _, size := range []int{-1, 0, maxOpaqueBytes + 1} {
		if value, err := RandomOpaque("", size); err == nil || value != "" {
			t.Fatalf("size=%d value=%q err=%v", size, value, err)
		}
	}
}
