package pushover

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

// 64-char hex test key — matches the C# validation regex.
const testEncryptionKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestValidateEncryptionKey_EmptyIsValid(t *testing.T) {
	t.Parallel()
	if err := validateEncryptionKey(""); err != nil {
		t.Errorf("empty key should be valid (encryption is optional): %v", err)
	}
}

func TestValidateEncryptionKey_ValidHex(t *testing.T) {
	t.Parallel()
	if err := validateEncryptionKey(testEncryptionKey); err != nil {
		t.Errorf("valid 64-hex key should pass: %v", err)
	}
	// Mixed case also accepted per the C# regex `[0-9a-fA-F]`.
	if err := validateEncryptionKey("ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"); err != nil {
		t.Errorf("uppercase hex should pass: %v", err)
	}
}

func TestValidateEncryptionKey_InvalidPatterns(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		key  string
	}{
		{"too-short", "0123abcd"},
		{"too-long-65", testEncryptionKey + "0"},
		{"too-long-128", testEncryptionKey + testEncryptionKey},
		{"non-hex-char", "g123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		{"contains-space", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validateEncryptionKey(tc.key); err == nil {
				t.Errorf("key %q should be rejected, got nil", tc.key)
			}
		})
	}
}

func TestHexStringToBytes_ValidProducesCorrectLength(t *testing.T) {
	t.Parallel()
	b, err := hexStringToBytes(testEncryptionKey)
	if err != nil {
		t.Fatalf("hexStringToBytes: %v", err)
	}
	if len(b) != 32 {
		t.Errorf("byte length: got %d, want 32", len(b))
	}
}

func TestPkcs7Pad_AppendsFullBlockWhenAlreadyAligned(t *testing.T) {
	t.Parallel()
	in := bytes.Repeat([]byte{0xaa}, aes.BlockSize) // exactly one block
	out := pkcs7Pad(in, aes.BlockSize)
	if len(out) != 2*aes.BlockSize {
		t.Fatalf("padded length: got %d, want %d (full extra block)", len(out), 2*aes.BlockSize)
	}
	// Verify the trailing block is all-block-size bytes (PKCS#7).
	for i := aes.BlockSize; i < 2*aes.BlockSize; i++ {
		if out[i] != byte(aes.BlockSize) {
			t.Errorf("pad byte[%d]: got %x, want %x", i, out[i], aes.BlockSize)
		}
	}
}

func TestPkcs7Pad_PartialBlock(t *testing.T) {
	t.Parallel()
	in := []byte{1, 2, 3} // 3 bytes → pad with 13 bytes of 0x0d
	out := pkcs7Pad(in, aes.BlockSize)
	if len(out) != aes.BlockSize {
		t.Fatalf("padded length: got %d, want 16", len(out))
	}
	for i := 3; i < aes.BlockSize; i++ {
		if out[i] != 0x0d {
			t.Errorf("pad byte[%d]: got %x, want 0x0d", i, out[i])
		}
	}
}

// TestEncryptField_RoundTripsCleanly is the critical end-to-end correctness
// test. The C# upstream's Pushover client decrypts the envelope by:
//
//  1. Base64-decoding the payload
//  2. Splitting into IV (16B) + ciphertext + MAC (32B)
//  3. Verifying HMAC-SHA256(key, IV||ciphertext) == MAC
//  4. AES-256-CBC decrypting ciphertext with IV
//  5. PKCS#7 un-padding
//  6. GZip decompressing
//
// This test inlines the inverse to verify the envelope round-trips
// cleanly — Pushover's server-side decryption uses the same crypto
// primitives so a successful round-trip here pins the wire format.
func TestEncryptField_RoundTripsCleanly(t *testing.T) {
	t.Parallel()
	key, err := hexStringToBytes(testEncryptionKey)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	plaintexts := []string{
		"Hello, Pushover!",
		"",                        // empty plaintext round-trips per C# behaviour
		strings.Repeat("a", 1000), // large compressible
		"Multi-line\nwith\nnewlines",
		"日本語 utf-8 multibyte ✓",
	}
	for _, pt := range plaintexts {
		t.Run(pt, func(t *testing.T) {
			t.Parallel()
			payload, err := encryptField(pt, key)
			if err != nil {
				t.Fatalf("encryptField: %v", err)
			}
			raw, err := base64.StdEncoding.DecodeString(payload)
			if err != nil {
				t.Fatalf("base64: %v", err)
			}
			// IV (16) + ciphertext (multiple of 16) + MAC (32).
			if len(raw) < aes.BlockSize+sha256.Size+aes.BlockSize {
				t.Fatalf("payload too short: %d", len(raw))
			}
			iv := raw[:aes.BlockSize]
			tag := raw[len(raw)-sha256.Size:]
			ct := raw[aes.BlockSize : len(raw)-sha256.Size]

			// Verify HMAC.
			mac := hmac.New(sha256.New, key)
			mac.Write(raw[:len(raw)-sha256.Size])
			if !hmac.Equal(mac.Sum(nil), tag) {
				t.Fatal("HMAC mismatch — Pushover client would reject the envelope")
			}

			// Decrypt.
			block, err := aes.NewCipher(key)
			if err != nil {
				t.Fatalf("aes: %v", err)
			}
			padded := make([]byte, len(ct))
			cipher.NewCBCDecrypter(block, iv).CryptBlocks(padded, ct)

			// PKCS#7 unpad.
			padLen := int(padded[len(padded)-1])
			if padLen < 1 || padLen > aes.BlockSize {
				t.Fatalf("bad pad len: %d", padLen)
			}
			compressed := padded[:len(padded)-padLen]

			// Gunzip.
			r, err := gzip.NewReader(bytes.NewReader(compressed))
			if err != nil {
				t.Fatalf("gzip reader: %v", err)
			}
			decompressed, err := io.ReadAll(r)
			if err != nil {
				t.Fatalf("gunzip: %v", err)
			}
			_ = r.Close()
			if string(decompressed) != pt {
				t.Errorf("round-trip mismatch:\ngot:  %q\nwant: %q", string(decompressed), pt)
			}
		})
	}
}

func TestEncryptField_DifferentIVEachCall(t *testing.T) {
	t.Parallel()
	key, _ := hexStringToBytes(testEncryptionKey)
	a, err := encryptField("same plaintext", key)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := encryptField("same plaintext", key)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a == b {
		t.Error("two encryptions of the same plaintext produced identical ciphertext — IV must be random per call")
	}
}

func TestEncryptField_WrongKeyLength_Errors(t *testing.T) {
	t.Parallel()
	if _, err := encryptField("hi", []byte("too-short")); err == nil {
		t.Fatal("8-byte key: want error")
	}
}
