package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

const maxOpaqueBytes = 4096

// RandomOpaque returns prefix followed by bytes of cryptographically random,
// unpadded base64url data.
func RandomOpaque(prefix string, bytes int) (string, error) {
	if bytes <= 0 || bytes > maxOpaqueBytes || len(prefix) > 64 {
		return "", errors.New("invalid opaque token size")
	}
	random := make([]byte, bytes)
	if _, err := rand.Read(random); err != nil {
		return "", errors.New("generate opaque token")
	}
	return prefix + base64.RawURLEncoding.EncodeToString(random), nil
}

// SHA256 returns a new SHA-256 digest slice for value.
func SHA256(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}
