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

// TestV3QualityProfileContract pins the Go /api/v3/qualityprofile
// output to the C#-recorded golden. The recording captures the 6
// default profiles SeedDefaults inserts on a fresh DB (Any / SD /
// HD-720p / HD-1080p / Ultra-HD / HD - 720p/1080p) — the same set
// the production Go binary seeds at startup (slice 4.EE wires
// qualityProfilesSvc.SeedDefaults into cmd/sonarr/main.go).
//
// Strategy: stand up a real SQLite via datastoretest.OpenSQLite,
// construct the full qualityprofiles.Service + customformats.Service
// stack against the same shared messaging.Bus production uses, run
// SeedDefaults, then serve the handler. This is the only contract
// test that touches a real DB — every other byte-eq gate has used
// either fixed-shape goldens or hand-built fixture listers. The
// QualityProfile seed produces nested QualityItem groups (id ≥ 1000)
// with hand-rolled wire shape that's impractical to mirror as a
// static fixture, so the test reuses the production code path
// instead.
func TestV3QualityProfileContract(t *testing.T) {
	t.Parallel()
	const entryID = "v3.qualityprofile"

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
		t.Fatalf("entry %q missing from corpus", entryID)
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

	if err := qpSvc.SeedDefaults(t.Context()); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}

	const testKey = "v3-qualityprofile-test-key"
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey),
		host.WithRoutes(host.Route{
			Method: "GET", Path: "/api/v3/qualityprofile",
			Handler: apiv3.QualityProfileListHandler(qpSvc, cfSvc),
		}))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	base, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
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
