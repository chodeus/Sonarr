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
	"time"

	apiv3 "sonarr.tv/sonarr/internal/api/v3"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3SystemStatusContract pins /api/v3/system/status to the C#-
// recorded golden. The recording host was a macOS debug build —
// fixture pins the platform flags and the cfg-derived auth/branch/
// instanceName/packageUpdateMechanism to match. The corpus redacts
// every host-specific field (appData/buildTime/database*/os*/
// runtime*/startTime/startupPath/urlBase/version) so the fixture
// values for those don't affect byte-eq.
func TestV3SystemStatusContract(t *testing.T) {
	const entryID = "v3.system.status"

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

	static := apiv3.SystemStatusContext{
		Version:           "redact-me",
		BuildTime:         time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		StartTime:         time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC),
		StartupPath:       "redact-me",
		AppData:           "redact-me",
		OsName:            "redact-me",
		OsVersion:         "redact-me",
		RuntimeName:       "redact-me",
		RuntimeVersion:    "redact-me",
		DatabaseType:      "redact-me",
		DatabaseVersion:   "redact-me",
		MigrationVersion:  230,
		IsDebug:           true,
		IsProduction:      false,
		IsAdmin:           false,
		IsUserInteractive: true,
		IsLinux:           false,
		IsOsx:             true,
		IsWindows:         false,
		IsDocker:          false,
		IsContainerized:   false,
		Mode:              "console",
	}
	getter := recordedStatusGetter{}

	const testKey = "v3-system-status-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/system/status",
			Handler: apiv3.SystemStatusHandler(static, getter),
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

// recordedStatusGetter replicates the cfg-derived fields for the
// recording host: AuthenticationMethod="None" (camelCased to "none"),
// Branch="main", InstanceName="Sonarr", URLBase=anything (redacted),
// UpdateMechanism="BuiltIn" (camelCased to "builtIn").
type recordedStatusGetter struct{}

func (recordedStatusGetter) AuthenticationMethod(context.Context) (string, error) {
	return "None", nil
}
func (recordedStatusGetter) Branch(context.Context) (string, error)       { return "main", nil }
func (recordedStatusGetter) InstanceName(context.Context) (string, error) { return "Sonarr", nil }
func (recordedStatusGetter) URLBase(context.Context) (string, error)      { return "redact-me", nil }
func (recordedStatusGetter) UpdateMechanism(context.Context) (string, error) {
	return "BuiltIn", nil
}
