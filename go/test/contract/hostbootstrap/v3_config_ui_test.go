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

// TestV3ConfigUIContract pins /api/v3/config/ui to the C#-recorded
// golden — defaults from a fresh DB plus xml-backed Theme="auto".
func TestV3ConfigUIContract(t *testing.T) {
	const entryID = "v3.config.ui"

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

	const testKey = "v3-config-ui-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/config/ui",
			Handler: apiv3.UIConfigHandler(defaultsUIConfig{}),
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

// defaultsUIConfig replicates the empty-DB UI config defaults. Order
// matches the C# field declaration; values mirror
// internal/config/provider.go defaults + configfile/wellknown.go
// Theme default.
type defaultsUIConfig struct{}

func (defaultsUIConfig) FirstDayOfWeek() int                   { return 1 }
func (defaultsUIConfig) CalendarWeekColumnHeader() string      { return "ddd M/D" }
func (defaultsUIConfig) ShortDateFormat() string               { return "MMM D YYYY" }
func (defaultsUIConfig) LongDateFormat() string                { return "dddd, MMMM D YYYY" }
func (defaultsUIConfig) TimeFormat() string                    { return "h(:mm)a" }
func (defaultsUIConfig) TimeZone() string                      { return "" }
func (defaultsUIConfig) ShowRelativeDates() bool               { return true }
func (defaultsUIConfig) EnableColorImpairedMode() bool         { return false }
func (defaultsUIConfig) UILanguage() int                       { return 1 }
func (defaultsUIConfig) Theme(context.Context) (string, error) { return "auto", nil }
