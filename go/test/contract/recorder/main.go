// Command recorder drives a running C# Sonarr backend with a corpus of
// requests and writes the responses as golden files.
//
// Usage:
//
//	go run ./test/contract/recorder \
//	    -base http://127.0.0.1:8989 \
//	    -api-key "$SONARR_API_KEY" \
//	    -corpus ./test/contract/corpus \
//	    -out ./test/contract/golden
//
// The recorder is intentionally minimal: it walks the corpus tree, issues
// each request, and writes one golden file per entry mirroring the corpus
// path. It does not retry, parallelize, or coalesce — predictable, single-
// threaded recording is the priority so reviewers can reason about diffs.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "recorder: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	base := flag.String("base", "http://127.0.0.1:8989", "base URL of the running Sonarr backend")
	apiKey := flag.String("api-key", "", "value of X-Api-Key header (required)")
	corpusDir := flag.String("corpus", "./test/contract/corpus", "directory tree of corpus *.json files")
	outDir := flag.String("out", "./test/contract/golden", "directory tree to write golden files into")
	timeout := flag.Duration("timeout", 30*time.Second, "per-request timeout")
	flag.Parse()

	if *apiKey == "" {
		return fmt.Errorf("-api-key is required")
	}
	baseURL, err := url.Parse(*base)
	if err != nil {
		return fmt.Errorf("parse -base: %w", err)
	}

	client := &runner.Client{Base: baseURL, APIKey: *apiKey}

	count := 0
	err = filepath.WalkDir(*corpusDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".json") {
			return nil
		}
		corpus, err := spec.LoadCorpus(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(*corpusDir, path)
		if err != nil {
			return err
		}
		for _, e := range corpus.Entries {
			ctx, cancel := context.WithTimeout(context.Background(), *timeout)
			g, err := client.Run(ctx, e)
			cancel()
			if err != nil {
				return fmt.Errorf("record %s/%s: %w", rel, e.ID, err)
			}
			outPath := filepath.Join(*outDir, strings.TrimSuffix(rel, ".json"), filepath.FromSlash(e.ID)+".golden.json")
			if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(outPath), err)
			}
			if err := spec.SaveGolden(outPath, g); err != nil {
				return err
			}
			count++
			fmt.Printf("recorded %s\n", outPath)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("recorded %d entries\n", count)
	return nil
}
