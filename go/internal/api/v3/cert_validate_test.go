package v3

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSelfSignedPEM writes a self-signed cert (and optionally its key)
// to temp files and returns their paths.
func writeSelfSignedPEM(t *testing.T, withKey bool) (certPath, keyPath string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sonarr-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if withKey {
		keyDER, err := x509.MarshalECPrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		keyPath = filepath.Join(dir, "key.pem")
		if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return certPath, keyPath
}

// osValidateCertificate accepts a valid self-signed PEM cert (no key).
func TestOsValidateCertificate_ValidPEM_J413(t *testing.T) {
	t.Parallel()
	certPath, _ := writeSelfSignedPEM(t, false)
	if err := osValidateCertificate(certPath, "", ""); err != nil {
		t.Errorf("valid PEM cert rejected: %v", err)
	}
}

// A valid cert + matching key pair validates.
func TestOsValidateCertificate_ValidPEMWithKey_J413(t *testing.T) {
	t.Parallel()
	certPath, keyPath := writeSelfSignedPEM(t, true)
	if err := osValidateCertificate(certPath, keyPath, ""); err != nil {
		t.Errorf("valid PEM cert+key rejected: %v", err)
	}
}

// Garbage content (neither PEM nor PKCS#12) is rejected.
func TestOsValidateCertificate_Garbage_J413(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "bad.pfx")
	if err := os.WriteFile(p, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := osValidateCertificate(p, "", ""); err == nil {
		t.Error("garbage cert accepted, want error")
	}
}

// A missing file is rejected.
func TestOsValidateCertificate_Missing_J413(t *testing.T) {
	t.Parallel()
	if err := osValidateCertificate("/no/such/cert.pem", "", ""); err == nil {
		t.Error("missing cert accepted, want error")
	}
}

// Handler-level: SSL enabled + a real valid PEM cert → 202 (exercises the
// production osValidateCertificate end to end).
func TestHostConfigUpdateHandler_ValidCert_OK_J413(t *testing.T) {
	t.Parallel()
	certPath, _ := writeSelfSignedPEM(t, false)
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"enableSsl":false`, `"enableSsl":true`, 1)
	body = strings.Replace(body, `"sslCertPath":""`, `"sslCertPath":"`+certPath+`"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
}

// Handler-level: SSL enabled + an existing-but-invalid cert file → 400
// with the IsValidCertificate message.
func TestHostConfigUpdateHandler_InvalidCert_400_J413(t *testing.T) {
	t.Parallel()
	bad := filepath.Join(t.TempDir(), "cert.pfx")
	if err := os.WriteFile(bad, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	body := strings.Replace(hostBody(), `"enableSsl":false`, `"enableSsl":true`, 1)
	body = strings.Replace(body, `"sslCertPath":""`, `"sslCertPath":"`+bad+`"`, 1)
	HostConfigUpdateHandler(&fakeHostConfig{}).ServeHTTP(rr, putHostReq(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: %d; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Invalid SSL certificate") {
		t.Errorf("expected IsValidCertificate rejection; body=%s", rr.Body.String())
	}
}
