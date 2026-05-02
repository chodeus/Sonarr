// Package replayer is a Go test helper that replays the corpus against a
// running Go Sonarr backend and asserts byte-equality with the recorded
// golden files.
//
// Tests opt in via flags:
//
//	go test ./test/contract/replayer/... \
//	    -contract.base=http://127.0.0.1:8989 \
//	    -contract.api-key=$SONARR_API_KEY
//
// When the flags are unset the test self-skips, so the package is safe to
// run as part of the default `go test ./...` lane in CI.
package replayer

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

var (
	flagBase    = flag.String("contract.base", "", "base URL of the Go Sonarr backend under test")
	flagAPIKey  = flag.String("contract.api-key", "", "X-Api-Key for the backend")
	flagCorpus  = flag.String("contract.corpus", "../corpus", "corpus directory")
	flagGolden  = flag.String("contract.golden", "../golden", "golden directory")
	flagTimeout = flag.Duration("contract.timeout", 30*time.Second, "per-request timeout")
)

// Replay walks the corpus and replays every entry against the configured
// backend. Each entry is a t.Run sub-test; failures report which field
// diverged from the recorded golden.
func Replay(t *testing.T) {
	t.Helper()
	if *flagBase == "" || *flagAPIKey == "" {
		t.Skip("contract.base and contract.api-key not set; skipping replay")
	}
	base, err := url.Parse(*flagBase)
	if err != nil {
		t.Fatalf("parse -contract.base: %v", err)
	}
	client := &runner.Client{Base: base, APIKey: *flagAPIKey}

	err = filepath.WalkDir(*flagCorpus, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		corpus, err := spec.LoadCorpus(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(*flagCorpus, path)
		if err != nil {
			return err
		}
		for _, e := range corpus.Entries {
			t.Run(rel+"/"+e.ID, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), *flagTimeout)
				defer cancel()
				got, err := client.Run(ctx, e)
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				goldenPath := filepath.Join(*flagGolden, strings.TrimSuffix(rel, ".json"), filepath.FromSlash(e.ID)+".golden.json")
				want, err := spec.LoadGolden(goldenPath)
				if err != nil {
					t.Fatalf("load golden %s: %v", goldenPath, err)
				}
				if diff := compare(got, want); diff != "" {
					t.Fatalf("contract drift for %s:\n%s", e.ID, diff)
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
}

func compare(got, want *spec.Golden) string {
	var b strings.Builder
	if got.Status != want.Status {
		fmt.Fprintf(&b, "  status: got %d want %d\n", got.Status, want.Status)
	}
	if got.BodyIsJSON != want.BodyIsJSON {
		fmt.Fprintf(&b, "  body kind: got json=%t want json=%t\n", got.BodyIsJSON, want.BodyIsJSON)
	}
	if !bytes.Equal(bytes.TrimSpace(got.BodyJSON), bytes.TrimSpace(want.BodyJSON)) {
		fmt.Fprintf(&b, "  body json:\n    got=%s\n    want=%s\n", got.BodyJSON, want.BodyJSON)
	}
	if !bytes.Equal(got.BodyRaw, want.BodyRaw) {
		fmt.Fprintf(&b, "  body raw differs (%d vs %d bytes)\n", len(got.BodyRaw), len(want.BodyRaw))
	}
	return b.String()
}

// SelfTestEnabled reports whether the replay flags are configured. Used in
// init paths that want to fail loud rather than skip.
func SelfTestEnabled() bool {
	return *flagBase != "" && *flagAPIKey != "" && fileExists(*flagGolden)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
