package v3

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"

	"golang.org/x/crypto/pkcs12"
)

// CertValidator reports whether the SSL certificate at certPath loads
// successfully — as a PEM certificate (with an optional separate keyPath)
// or as a PKCS#12 / PFX bundle (with password). Returns nil when valid,
// otherwise an error whose message is surfaced in the validation failure.
// Mirrors the C# `CertificateValidator`
// (`src/Sonarr.Api.V3/Config/CertificateValidator.cs`): detect the cert
// content type, then attempt the matching load.
type CertValidator func(certPath, keyPath, password string) error

// osValidateCertificate is the production CertValidator. It reads the
// file and parses it as PEM (when it carries a PEM armor) or otherwise as
// binary PKCS#12 — matching C# `X509Certificate2.GetCertContentType`
// dispatch to `CreateFromPemFile` vs `LoadPkcs12FromFile`.
func osValidateCertificate(certPath, keyPath, password string) error {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("unable to read certificate: %w", err)
	}
	if bytes.Contains(data, []byte("-----BEGIN")) {
		return validatePEMCert(certPath, data, keyPath)
	}
	// Binary form → PKCS#12 (PFX). Decode with the supplied password.
	if _, _, err := pkcs12.Decode(data, password); err != nil {
		return err
	}
	return nil
}

// validatePEMCert validates a PEM certificate. When keyPath is set, the
// cert + key are loaded as a pair (verifying they match, like C#
// CreateFromPemFile(certPath, keyPath)); otherwise at least one
// CERTIFICATE block must parse.
func validatePEMCert(certPath string, certPEM []byte, keyPath string) error {
	if keyPath != "" {
		if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
			return err
		}
		return nil
	}
	for rest := certPEM; len(rest) > 0; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return err
			}
			return nil
		}
	}
	return errors.New("no CERTIFICATE block found in PEM file")
}
