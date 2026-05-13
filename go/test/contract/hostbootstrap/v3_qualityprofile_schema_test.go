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
	"sonarr.tv/sonarr/internal/customformats"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/internal/messaging"
	"sonarr.tv/sonarr/internal/qualityprofiles"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// TestV3QualityProfileSchemaContract pins /api/v3/qualityprofile/schema
// to the C#-recorded golden — a single template QualityProfileResource
// with allowed=false everywhere, no cutoff (Cutoff=0), and the same
// per-quality MinSize/MaxSize/PreferredSize values the qualityprofile
// list endpoint emits for the seed defaults.
//
// Strategy mirrors the qualityprofile list contract test (slice 4.EE):
// stand up real SQLite + messaging.Bus + qualityprofiles.Service +
// customformats.Service, wire SetFormatProvider, then serve the
// schema handler. SeedDefaults is NOT called — the schema endpoint
// produces its own per-request default profile via GetDefaultProfile,
// not from persisted rows.
func TestV3QualityProfileSchemaContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.qualityprofile.schema"

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

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	db := datastoretest.OpenSQLite(t)
	bus := messaging.New(logger)

	qpRepo, err := qualityprofiles.NewRepo(db)
	if err != nil {
		t.Fatalf("qualityprofiles.NewRepo: %v", err)
	}
	qpSvc, err := qualityprofiles.NewService(qpRepo, bus)
	if err != nil {
		t.Fatalf("qualityprofiles.NewService: %v", err)
	}
	cfRepo, err := customformats.NewRepo(db)
	if err != nil {
		t.Fatalf("customformats.NewRepo: %v", err)
	}
	cfSvc := customformats.NewService(cfRepo, bus)
	qpSvc.SetFormatProvider(cfSvc)

	const testKey = "v3-qualityprofile-schema-key"
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: entry.Request.Path,
			Handler: apiv3.QualityProfileSchemaHandler(qpSvc, cfSvc),
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
