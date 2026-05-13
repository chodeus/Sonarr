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
	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3ConfigMediaManagementContract pins /api/v3/config/mediamanagement
// to the C#-recorded golden. The 24-field singleton uses the
// fresh-DB defaults verbatim, with 5 enum fields camelCased at the
// API edge (downloadPropersAndRepacks, episodeTitleRequired,
// fileDate, rescanAfterRefresh, seasonPackUpgrade).
func TestV3ConfigMediaManagementContract(t *testing.T) {
	const entryID = "v3.config.mediamanagement"

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

	const testKey = "v3-config-mediamanagement-test-key"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/config/mediamanagement",
			Handler: apiv3.MediaManagementConfigHandler(defaultsMMConfig{}),
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

// defaultsMMConfig replicates the empty-DB mediamanagement defaults
// exactly. Each value comes from internal/config/provider.go's
// getDefault calls; the PascalCase enum strings (Always, None, All)
// pass through CamelCaseEnumName at the handler boundary.
type defaultsMMConfig struct{}

func (defaultsMMConfig) AutoUnmonitorPreviouslyDownloadedEpisodes() bool { return false }
func (defaultsMMConfig) ChmodFolder() string                             { return "755" }
func (defaultsMMConfig) ChownGroup() string                              { return "" }
func (defaultsMMConfig) CopyUsingHardlinks() bool                        { return true }
func (defaultsMMConfig) CreateEmptySeriesFolders() bool                  { return false }
func (defaultsMMConfig) DeleteEmptyFolders() bool                        { return false }
func (defaultsMMConfig) DownloadPropersAndRepacks() quality.ProperDownloadType {
	return quality.ProperPreferAndUpgrade
}
func (defaultsMMConfig) EnableMediaInfo() bool                 { return true }
func (defaultsMMConfig) EpisodeTitleRequired() string          { return "Always" }
func (defaultsMMConfig) ExtraFileExtensions() string           { return "srt" }
func (defaultsMMConfig) FileDate() string                      { return "None" }
func (defaultsMMConfig) ImportExtraFiles() bool                { return false }
func (defaultsMMConfig) MinimumFreeSpaceWhenImporting() int    { return 100 }
func (defaultsMMConfig) RecycleBin() string                    { return "" }
func (defaultsMMConfig) RecycleBinCleanupDays() int            { return 7 }
func (defaultsMMConfig) RescanAfterRefresh() string            { return "Always" }
func (defaultsMMConfig) ScriptImportPath() string              { return "" }
func (defaultsMMConfig) SeasonPackUpgrade() string             { return "All" }
func (defaultsMMConfig) SeasonPackUpgradeThreshold() float64   { return 100 }
func (defaultsMMConfig) SetPermissionsLinux() bool             { return false }
func (defaultsMMConfig) SkipFreeSpaceCheckWhenImporting() bool { return false }
func (defaultsMMConfig) UseScriptImport() bool                 { return false }
func (defaultsMMConfig) UserRejectedExtensions() string        { return "" }
