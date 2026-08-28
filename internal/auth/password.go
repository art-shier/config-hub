package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argon2Version = 19
	argon2Memory  = 64 * 1024
	argon2Time    = 3
	argon2Threads = 2
	saltLength    = 16
	hashLength    = 32
)

var errEmptyPassword = errors.New("password must not be empty")

// HashPassword returns a versioned Argon2id password hash.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errEmptyPassword
	}

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, hashLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version, argon2Memory, argon2Time, argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

// VerifyPassword reports whether password matches a ConfigHub Argon2id hash.
func VerifyPassword(encoded, password string) bool {
	if password == "" {
		return false
	}
	salt, expectedHash, ok := parsePasswordHash(encoded)
	if !ok {
		return false
	}
	actualHash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Memory, argon2Threads, hashLength)
	return subtle.ConstantTimeCompare(expectedHash, actualHash) == 1
}

func parsePasswordHash(encoded string) ([]byte, []byte, bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=2" {
		return nil, nil, false
	}
	salt, err := decodeCanonicalRawBase64(parts[4])
	if err != nil || len(salt) != saltLength {
		return nil, nil, false
	}
	hash, err := decodeCanonicalRawBase64(parts[5])
	if err != nil || len(hash) != hashLength {
		return nil, nil, false
	}
	return salt, hash, true
}

func decodeCanonicalRawBase64(encoded string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
	if err != nil || base64.RawStdEncoding.EncodeToString(decoded) != encoded {
		return nil, errors.New("invalid raw base64")
	}
	return decoded, nil
}
