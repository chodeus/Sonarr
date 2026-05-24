// Pushover end-to-end message encryption. Ports the
// `EncryptField` + `GzipCompress` + `HexStringToBytes` helpers added
// by C# upstream commit `e79077d5f` at
// `Notifications/Pushover/PushoverProxy.cs:97-156`.
//
// The encryption envelope (verbatim from upstream):
//
//  1. GZip-compress the plaintext UTF-8 bytes.
//  2. AES-256-CBC encrypt the compressed bytes with a random IV +
//     PKCS#7 padding. Key is the 64-hex-char setting parsed as 32
//     bytes.
//  3. Compute HMAC-SHA256 over (IV || ciphertext) using the same key.
//  4. Base64-encode the concatenated (IV || ciphertext || MAC).
//
// The Pushover client receives the base64 payload + the
// `encrypted=1` form flag and decrypts on-device using the same key.
//
// Slice 5.J219 (audit row C.1.f).

package pushover

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

// encryptionKeyPattern mirrors the C# FluentValidation rule at
// `PushoverSettings.cs:17` — exactly 64 hex characters (case-insensitive),
// which decodes to a 32-byte AES-256 key.
var encryptionKeyPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// encryptField ports C# `PushoverProxy.cs:99-130 EncryptField`. Returns
// the base64-encoded envelope ready to ship as the title/message form
// value when `encrypted=1` is set.
//
// Per the C# upstream behaviour, a nil/empty plaintext encrypts cleanly
// (the gzip compresses an empty buffer, AES encrypts the result). Tests
// pin this since it's user-observable for empty notification bodies.
func encryptField(plaintext string, key []byte) (string, error) {
	if len(key) != 32 {
		return "", fmt.Errorf("pushover: encryption key must be 32 bytes, got %d", len(key))
	}

	compressed, err := gzipCompress([]byte(plaintext))
	if err != nil {
		return "", fmt.Errorf("pushover: gzip: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("pushover: aes.NewCipher: %w", err)
	}

	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("pushover: random IV: %w", err)
	}

	// PKCS#7 pad the compressed plaintext up to the AES block size.
	padded := pkcs7Pad(compressed, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	mode := cipher.NewCBCEncrypter(block, iv)
	mode.CryptBlocks(ciphertext, padded)

	ivAndCiphertext := make([]byte, 0, len(iv)+len(ciphertext))
	ivAndCiphertext = append(ivAndCiphertext, iv...)
	ivAndCiphertext = append(ivAndCiphertext, ciphertext...)

	mac := hmac.New(sha256.New, key)
	mac.Write(ivAndCiphertext)
	tag := mac.Sum(nil)

	payload := make([]byte, 0, len(ivAndCiphertext)+len(tag))
	payload = append(payload, ivAndCiphertext...)
	payload = append(payload, tag...)
	return base64.StdEncoding.EncodeToString(payload), nil
}

// gzipCompress ports C# `PushoverProxy.cs:132-141 GzipCompress`. The
// upstream uses `CompressionLevel.Optimal`; Go's gzip.DefaultCompression
// maps to the same effective behaviour (zlib level 6 in both runtimes
// for "optimal/default"). The on-the-wire byte sequence isn't required
// to match C# exactly — only the round-trip through Pushover's
// decryptor matters, and decompression accepts any valid gzip stream.
func gzipCompress(data []byte) ([]byte, error) {
	var out bytes.Buffer
	gz, err := gzip.NewWriterLevel(&out, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	if _, err := gz.Write(data); err != nil {
		_ = gz.Close()
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// pkcs7Pad applies PKCS#7 padding to make `src` an exact multiple of
// `blockSize`. A full block of padding is appended when `len(src)` is
// already a multiple — required by the PKCS#7 spec so decoders can
// always strip the trailing pad. Mirrors the C# `aes.Padding =
// PaddingMode.PKCS7` semantics.
func pkcs7Pad(src []byte, blockSize int) []byte {
	pad := blockSize - len(src)%blockSize
	out := make([]byte, len(src)+pad)
	copy(out, src)
	for i := len(src); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

// hexStringToBytes ports C# `PushoverProxy.cs:143-152 HexStringToBytes`.
// Returns an error on odd-length input or non-hex chars; the regex
// validator on EncryptionKey gates against both at the settings layer
// but we keep the parse defensive in case the value bypasses Validate.
func hexStringToBytes(s string) ([]byte, error) {
	out, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("pushover: hex decode: %w", err)
	}
	return out, nil
}

// validateEncryptionKey reports whether the supplied key matches the
// 64-hex-char rule from C# `PushoverSettingsValidator.cs:17`. Empty
// values pass through (encryption is optional); a non-empty value
// that doesn't match the pattern is rejected.
func validateEncryptionKey(key string) error {
	if key == "" {
		return nil
	}
	if !encryptionKeyPattern.MatchString(key) {
		return errors.New("EncryptionKey must be exactly 64 hexadecimal characters")
	}
	return nil
}
