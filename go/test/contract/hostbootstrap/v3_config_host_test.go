package hostbootstrap

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3ConfigHostContract pins /api/v3/config/host to the C#-recorded
// golden. The recording host had a few non-default values
// (port=9898, launchBrowser=false, analyticsEnabled=false) that the
// fixture replicates verbatim; the corpus redacts apiKey +
// instanceName, so the runner normalises both sides past those.
func TestV3ConfigHostContract(t *testing.T) {
	const entryID = "v3.config.host"

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	corpusPath := filepath.Join(repoRoot, "test", "contract", "corpus", "v3-empty-state.json")
	goldenPath := filepath.Join(repoRoot, "test", "contract", "golden", "v3-empty-state", entryID+".golden.json")

	corpus, err := spec.LoadCorpus(corpusPath)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	var entry spec.Entry
	for _, e := range corpus.Entries {
		if e.ID == entryID {
			entry = e
			break
		}
	}
	if entry.ID == "" {
		t.Fatalf("entry %q missing", entryID)
	}

	const testKey = "v3-config-host-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/config/host",
			Handler: apiv3.HostConfigHandler(recordedHostConfig{}),
		}))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	base, _ := url.Parse(ts.URL)
	client := &runner.Client{Base: base, APIKey: testKey}
	got, err := client.Run(context.Background(), entry)
	if err != nil {
		t.Fatalf("client.Run: %v", err)
	}
	want, err := spec.LoadGolden(goldenPath)
	if err != nil {
		t.Fatalf("load golden: %v", err)
	}
	if got.Status != want.Status {
		t.Errorf("status: got %d want %d", got.Status, want.Status)
	}
	if !bytes.Equal(spec.CompactJSON(got.BodyJSON), spec.CompactJSON(want.BodyJSON)) {
		t.Errorf("body drift:\n got=%s\n want=%s", got.BodyJSON, want.BodyJSON)
	}
}

// recordedHostConfig replicates the values the recording host
// emitted exactly. port=9898 + analyticsEnabled=false +
// launchBrowser=false are non-default; the rest match the Go provider
// defaults. apiKey + instanceName carry "redact-me" placeholders —
// the corpus normalizer rewrites them to "<redacted>" before
// comparison.
type recordedHostConfig struct{}

func (recordedHostConfig) BindAddress(context.Context) (string, error)          { return "*", nil }
func (recordedHostConfig) Port(context.Context) (int, error)                    { return 9898, nil }
func (recordedHostConfig) SslPort(context.Context) (int, error)                 { return 9898, nil }
func (recordedHostConfig) EnableSsl(context.Context) (bool, error)              { return false, nil }
func (recordedHostConfig) LaunchBrowser(context.Context) (bool, error)          { return false, nil }
func (recordedHostConfig) AuthenticationMethod(context.Context) (string, error) { return "None", nil }
func (recordedHostConfig) AuthenticationRequired(context.Context) (string, error) {
	return "Enabled", nil
}
func (recordedHostConfig) AnalyticsEnabled(context.Context) (bool, error)      { return false, nil }
func (recordedHostConfig) LogLevel(context.Context) (string, error)            { return "debug", nil }
func (recordedHostConfig) LogSizeLimit(context.Context) (int, error)           { return 1, nil }
func (recordedHostConfig) ConsoleLogLevel(context.Context) (string, error)     { return "", nil }
func (recordedHostConfig) Branch(context.Context) (string, error)              { return "main", nil }
func (recordedHostConfig) APIKey(context.Context) (string, error)              { return "redact-me", nil }
func (recordedHostConfig) SslCertPath(context.Context) (string, error)         { return "", nil }
func (recordedHostConfig) SslKeyPath(context.Context) (string, error)          { return "", nil }
func (recordedHostConfig) SslCertPassword(context.Context) (string, error)     { return "", nil }
func (recordedHostConfig) URLBase(context.Context) (string, error)             { return "", nil }
func (recordedHostConfig) InstanceName(context.Context) (string, error)        { return "redact-me", nil }
func (recordedHostConfig) TrustCgnatIpAddresses(context.Context) (bool, error) { return false, nil }
func (recordedHostConfig) UpdateAutomatically(context.Context) (bool, error)   { return false, nil }
func (recordedHostConfig) UpdateMechanism(context.Context) (string, error)     { return "BuiltIn", nil }
func (recordedHostConfig) UpdateScriptPath(context.Context) (string, error)    { return "", nil }
func (recordedHostConfig) ApplicationUrl() string                              { return "" }
func (recordedHostConfig) BackupFolder() string                                { return "Backups" }
func (recordedHostConfig) BackupInterval() int                                 { return 7 }
func (recordedHostConfig) BackupRetention() int                                { return 28 }
func (recordedHostConfig) CertificateValidation() string                       { return "Enabled" }
func (recordedHostConfig) ProxyEnabled() bool                                  { return false }
func (recordedHostConfig) ProxyType() string                                   { return "Http" }
func (recordedHostConfig) ProxyHostname() string                               { return "" }
func (recordedHostConfig) ProxyPort() int                                      { return 8080 }
func (recordedHostConfig) ProxyUsername() string                               { return "" }
func (recordedHostConfig) ProxyPassword() string                               { return "" }
func (recordedHostConfig) ProxyBypassFilter() string                           { return "" }
func (recordedHostConfig) ProxyBypassLocalAddresses() bool                     { return true }
