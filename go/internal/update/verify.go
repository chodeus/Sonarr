package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

// Verify ports C# `UpdateVerification.Verify` (`Update/UpdateVerification.cs:20-28`):
// SHA256-hashes the downloaded package file and compares the hex digest
// to the package's expected Hash, case-insensitively. This is the
// integrity check the update feed publishes — NOT a GPG/signature check
// (the C# `IVerifyUpdates` is a plain hash compare). A blank expected
// Hash never matches (returns false) so a feed that omits the hash can't
// silently pass verification.
func Verify(pkg *Package, packagePath string) (bool, error) {
	if pkg == nil {
		return false, nil
	}
	want := strings.TrimSpace(pkg.Hash)
	if want == "" {
		return false, nil
	}
	f, err := os.Open(packagePath)
	if err != nil {
		return false, fmt.Errorf("update: Verify: open %q: %w", packagePath, err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, fmt.Errorf("update: Verify: hash %q: %w", packagePath, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	return strings.EqualFold(got, want), nil
}
