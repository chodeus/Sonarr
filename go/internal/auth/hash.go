// Package auth is the Go port of NzbDrone.Core.Authentication +
// the bits of NzbDrone.Common / Sonarr.Http that own the password,
// API key, and cookie-name primitives. Phase 1 ships the data-shape
// portion (hash format, key entropy, cookie name derivation, lazy
// SHA-256 → PBKDF2 upgrade-on-login). The HTTP-handler glue
// (cookie issuance, ApiKeyAuthenticationHandler, Forms vs External
// vs None scheme dispatch) lands in Phase 5 once the HTTP surface
// exists.
//
// # Hash format parity
//
// The C# tree (UserService.cs:142-150) calls
// Microsoft.AspNetCore.Cryptography.KeyDerivation.Pbkdf2 with:
//
//	prf:                HMAC-SHA512
//	iterationCount:     10000
//	saltSize:           16 bytes (random per user)
//	numBytesRequested:  32 bytes
//	salt encoding:      base64 (RFC 4648 standard alphabet)
//	hash encoding:      base64 (same alphabet)
//
// 10k iterations is far below modern guidance (NIST 2023+: 600k+ for
// SHA-512), but the port matches C# byte-for-byte so user databases
// written by Sonarr v3/v4/v5 keep validating. SEC-Q3 / AUTH-Q1 in
// analysis/questions.md track raising the iteration count as a
// post-cutover follow-up — bumping it requires the same lazy-upgrade
// pattern the SHA-256 → PBKDF2 path already uses.
//
// # SHA-256 legacy
//
// Pre-PBKDF2 Sonarr stored bare SHA-256 hashes (UTF-8 input → SHA-256
// → lowercase hex). Old user rows have Salt/Iterations NULL and
// Password = SHA-256 hex. On the next successful login the service
// re-hashes with PBKDF2 and persists. AUTH-Q1 resolution: keep this
// lazy upgrade behaviour exactly.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	// pbkdf2Iterations matches UserService.ITERATIONS. Frozen for byte-
	// equivalence with existing user DBs.
	pbkdf2Iterations = 10000
	// pbkdf2SaltSize matches UserService.SALT_SIZE (128 bits / 8).
	pbkdf2SaltSize = 16
	// pbkdf2KeyLength matches UserService.NUMBER_OF_BYTES (256 bits / 8).
	pbkdf2KeyLength = 32
)

// Hash is the PBKDF2 result for one user. Salt and Hash are stored in
// the Users.Salt and Users.Password columns respectively, both as
// base64-encoded strings. Iterations is stored in Users.Iterations
// (int).
type Hash struct {
	Salt       string
	Iterations int
	Hash       string
}

// HashPassword computes a fresh PBKDF2 hash for password using a new
// 16-byte random salt and the canonical iteration count. Returns the
// triple that maps directly onto the Users.{Salt, Iterations,
// Password} columns.
func HashPassword(password string) (Hash, error) {
	salt := make([]byte, pbkdf2SaltSize)
	if _, err := rand.Read(salt); err != nil {
		return Hash{}, fmt.Errorf("auth: read random salt: %w", err)
	}
	digest, err := pbkdf2Hash(password, salt, pbkdf2Iterations)
	if err != nil {
		return Hash{}, err
	}
	return Hash{
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Iterations: pbkdf2Iterations,
		Hash:       base64.StdEncoding.EncodeToString(digest),
	}, nil
}

// VerifyPassword reports whether password matches the stored Hash.
// Comparison is constant-time. Returns false (not an error) on any
// decoding or compute failure — leaking error detail on a login path
// is a side-channel the C# code also avoids.
func VerifyPassword(stored Hash, password string) bool {
	salt, err := base64.StdEncoding.DecodeString(stored.Salt)
	if err != nil {
		return false
	}
	digest, err := pbkdf2Hash(password, salt, stored.Iterations)
	if err != nil {
		return false
	}
	candidate := base64.StdEncoding.EncodeToString(digest)
	// Constant-time compare on the base64 strings (matches what C# does
	// when it compares hash strings via string equality — except C#'s
	// string == is NOT constant-time, so this is also a SEC-Q2 fix).
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(stored.Hash)) == 1
}

// SHA256Hex matches NzbDrone.Core.Hashing.SHA256Hash(string): UTF-8
// bytes of input → SHA-256 → lowercase hex. Used only for the lazy
// upgrade path — when a row's Salt is NULL, the legacy hex hash is
// the recorded Password value, and the candidate password's SHA256Hex
// is what we compare against to decide whether to upgrade.
func SHA256Hex(input string) string {
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}

// pbkdf2Hash runs PBKDF2-HMAC-SHA512 with the parameters Sonarr stores.
// Errors only on malformed input (negative iterations); modern Go
// pbkdf2 is panic-free for sane parameter shapes.
func pbkdf2Hash(password string, salt []byte, iterations int) ([]byte, error) {
	if iterations <= 0 {
		return nil, errors.New("auth: non-positive iterations")
	}
	return pbkdf2.Key(sha512.New, password, salt, iterations, pbkdf2KeyLength)
}
