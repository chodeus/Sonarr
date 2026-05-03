package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// apiKeyBytes is the entropy budget for newly generated API keys.
// Per SEC-Q5 (analysis/questions.md), 32 bytes from crypto/rand —
// hex-encoded to 64 chars — replaces the C# `Guid.NewGuid().ToString()
// .Replace("-", "")` pattern, which yields only 122 bits and 32 chars.
// Existing C# keys keep working because comparisons are constant-time
// over the stored key value, whatever its length.
const apiKeyBytes = 32

// GenerateAPIKey returns a fresh hex-encoded API key (64 chars,
// 256 bits of entropy). Returns the key and any rand.Read error.
func GenerateAPIKey() (string, error) {
	buf := make([]byte, apiKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: read random apikey: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// EqualAPIKey reports whether two keys are byte-equal in constant
// time. Use this for every API key comparison on the server side —
// the regular == operator leaks key length and prefix matches via
// short-circuit timing.
//
// subtle.ConstantTimeCompare returns 0 when lengths differ, so this
// also does a length-safe compare. Empty strings on either side
// return false (an empty key never matches an empty key — both must
// fail the same authenticated-only path).
func EqualAPIKey(stored, presented string) bool {
	if stored == "" || presented == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(presented)) == 1
}
