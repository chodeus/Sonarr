// Package hostbootstrap holds the contract-level test for the Phase 5
// slice 4.DF endpoint surface (`/ping`). It boots the Go host package
// directly via httptest and replays the corpus through the same
// runner.Client + spec.Golden machinery the cross-binary harness uses.
//
// Why a separate test rather than the existing replayer? The cross-
// binary replayer self-skips unless -contract.base is set, which is
// the right default for endpoints whose golden files were recorded
// against the C# binary. The host-bootstrap goldens are bootstrap
// references — there is no C# /ping golden yet because the recorder
// has never been pointed at the C# host (Phase 0.5 deferred). This
// test pins the Go behaviour to the shape the C# controller produces
// per direct source read; when the recorder lands the C# capture, the
// goldens are regenerated and this test continues to gate Go drift.
package hostbootstrap

import (
	"bytes"
	"context"
	"flag"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/host"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// updateFlag rewrites the goldens on disk when set. Run with
// `go test ./test/contract/hostbootstrap/... -hostbootstrap.update`
// after a deliberate change. Without the flag the test asserts
// byte-equality and fails on drift.
var updateFlag = flag.Bool("hostbootstrap.update", false, "rewrite host-bootstrap golden files")

func TestHostBootstrapContract(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	corpusPath := filepath.Join(repoRoot, "test", "contract", "corpus", "host-bootstrap.json")
	goldenDir := filepath.Join(repoRoot, "test", "contract", "golden", "host-bootstrap")

	corpus, err := spec.LoadCorpus(corpusPath)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	const testKey = "host-bootstrap-test-key"
	srv := host.New(host.Config{}, logger, okProbe{}, fixedAPIKey(testKey))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	base, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}
	client := &runner.Client{Base: base, APIKey: testKey}

	for _, e := range corpus.Entries {
		t.Run(e.ID, func(t *testing.T) {
			got, err := client.Run(context.Background(), e)
			if err != nil {
				t.Fatalf("client.Run: %v", err)
			}
			// Headers vary by Go stdlib version (Date, Content-Length);
			// the cross-binary replayer's compare() ignores them too.
			got.Headers = nil

			goldenPath := filepath.Join(goldenDir, e.ID+".golden.json")
			if *updateFlag {
				if err := os.MkdirAll(goldenDir, 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := spec.SaveGolden(goldenPath, got); err != nil {
					t.Fatalf("save golden: %v", err)
				}
				return
			}

			want, err := spec.LoadGolden(goldenPath)
			if err != nil {
				t.Fatalf("load golden %s: %v (re-run with -hostbootstrap.update to bootstrap)", goldenPath, err)
			}
			if got.Status != want.Status {
				t.Errorf("status: got %d want %d", got.Status, want.Status)
			}
			if got.BodyIsJSON != want.BodyIsJSON {
				t.Errorf("body kind: got json=%t want json=%t", got.BodyIsJSON, want.BodyIsJSON)
			}
			if !bytes.Equal(spec.CompactJSON(got.BodyJSON), spec.CompactJSON(want.BodyJSON)) {
				t.Errorf("body json: got %q want %q", got.BodyJSON, want.BodyJSON)
			}
			if !bytes.Equal(got.BodyRaw, want.BodyRaw) {
				t.Errorf("body raw differs (%d vs %d bytes)", len(got.BodyRaw), len(want.BodyRaw))
			}
		})
	}
}

// okProbe satisfies host.ConfigProbe with an always-OK signal — the
// golden corresponds to the happy-path /ping shape.
type okProbe struct{}

func (okProbe) All(context.Context) error { return nil }

// fixedAPIKey satisfies host.APIKeyResolver with a constant value.
type fixedAPIKey string

func (k fixedAPIKey) APIKey(context.Context) (string, error) { return string(k), nil }

// findRepoRoot walks upward from the test's CWD until it finds the
// go.mod that declares this module. Test working directory is the
// test's package directory, which is several levels under go/.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
