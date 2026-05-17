// Command diff replays a corpus against two HTTP backends and reports
// per-entry drift between their responses. Slice 4.FM landed the
// JSONDiff primitive; this slice ships the corpus-driver wrapper.
//
// Usage:
//
//	go run ./test/contract/diff \
//	    -primary   http://127.0.0.1:8989 \
//	    -secondary http://127.0.0.1:8990 \
//	    -primary-key   "$PRIMARY_API_KEY" \
//	    -secondary-key "$SECONDARY_API_KEY" \
//	    -corpus    ./test/contract/corpus/v3-empty-state.json \
//	    -v
//
// The two backends typically are a C# Sonarr (-primary) and a Go
// Sonarr (-secondary). With -secondary-key omitted, primary-key is
// reused for both — the usual case when both binaries share the same
// staging DB.
//
// Exit codes: 0 (no drift), 1 (drift found), 2 (flag misuse).
//
// PHASE_6_SCOPE.md identifies this binary as one of the prerequisites
// for the 4-week shadow-traffic period. Future slices can extend it
// with daily-report aggregation, parallel replay, or a long-poll mode
// that diffs live mirrored traffic instead of a frozen corpus.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"time"

	"sonarr.tv/sonarr/test/contract/internal/normalize"
	"sonarr.tv/sonarr/test/contract/internal/runner"
	"sonarr.tv/sonarr/test/contract/internal/spec"
)

// Result records the per-entry comparison outcome. Status / Diffs are
// populated for non-erroring entries; Err carries either backend's
// network/parse error (whichever surfaced first).
type Result struct {
	ID           string
	StatusA      int
	StatusB      int
	Diffs        []normalize.Difference
	Err          error
	BothJSON     bool // true when both responses were Content-Type JSON
	StatusMatch  bool
	BodyMismatch bool
}

// Summary aggregates Result counts across the corpus replay.
type Summary struct {
	Total      int
	Matched    int // status equal AND (no diff OR no body diff to check)
	Mismatched int // status differs OR JSONDiff returned non-empty
	Errored    int // network/parse error on either backend
}

// Options carries the diffCorpus inputs. Lifted out of main() so tests
// can drive the package without a process spawn.
type Options struct {
	Primary, Secondary       *url.URL
	PrimaryKey, SecondaryKey string
	Corpus                   *spec.Corpus
	Timeout                  time.Duration
}

// diffCorpus walks every corpus entry, hits both backends, and
// returns the per-entry results + the summary. Errors out only on
// catastrophic misconfiguration (nil corpus / nil base URLs); per-
// entry failures land in Result.Err so the summary still reports a
// full pass.
func diffCorpus(ctx context.Context, opts Options) ([]Result, Summary, error) {
	if opts.Corpus == nil {
		return nil, Summary{}, errors.New("diff: nil corpus")
	}
	if opts.Primary == nil || opts.Secondary == nil {
		return nil, Summary{}, errors.New("diff: primary and secondary base URLs required")
	}
	pClient := &runner.Client{Base: opts.Primary, APIKey: opts.PrimaryKey}
	sClient := &runner.Client{Base: opts.Secondary, APIKey: opts.SecondaryKey}

	results := make([]Result, 0, len(opts.Corpus.Entries))
	var summary Summary
	summary.Total = len(opts.Corpus.Entries)

	for _, e := range opts.Corpus.Entries {
		entryCtx := ctx
		if opts.Timeout > 0 {
			var cancel context.CancelFunc
			entryCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
			defer cancel()
		}
		r := Result{ID: e.ID}
		pG, pErr := pClient.Run(entryCtx, e)
		sG, sErr := sClient.Run(entryCtx, e)
		switch {
		case pErr != nil && sErr != nil:
			r.Err = fmt.Errorf("both backends failed: primary=%w secondary=%w", pErr, sErr)
		case pErr != nil:
			r.Err = fmt.Errorf("primary failed: %w", pErr)
		case sErr != nil:
			r.Err = fmt.Errorf("secondary failed: %w", sErr)
		}
		if r.Err != nil {
			summary.Errored++
			results = append(results, r)
			continue
		}

		r.StatusA = pG.Status
		r.StatusB = sG.Status
		r.StatusMatch = pG.Status == sG.Status
		r.BothJSON = pG.BodyIsJSON && sG.BodyIsJSON
		if r.BothJSON {
			diffs, derr := normalize.JSONDiff(pG.BodyJSON, sG.BodyJSON)
			if derr != nil {
				r.Err = fmt.Errorf("diff %s: %w", e.ID, derr)
				summary.Errored++
				results = append(results, r)
				continue
			}
			r.Diffs = diffs
			r.BodyMismatch = len(diffs) > 0
		} else if pG.BodyIsJSON != sG.BodyIsJSON {
			// One side returned JSON, the other didn't. Treat as a
			// content-type drift — count as mismatch.
			r.BodyMismatch = true
		}
		if !r.StatusMatch || r.BodyMismatch {
			summary.Mismatched++
		} else {
			summary.Matched++
		}
		results = append(results, r)
	}
	return results, summary, nil
}

// printReport renders the per-entry result list + a final summary
// line to w. verbose=true expands every Diff into a path-by-path
// listing; otherwise only the count surfaces per entry.
func printReport(w io.Writer, results []Result, summary Summary, verbose bool) {
	// Sort by entry ID so the report is deterministic regardless of
	// corpus order. Stable across runs makes the report greppable.
	sorted := make([]Result, len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	for _, r := range sorted {
		switch {
		case r.Err != nil:
			_, _ = fmt.Fprintf(w, "ERROR  %s: %v\n", r.ID, r.Err)
		case !r.StatusMatch:
			_, _ = fmt.Fprintf(w, "STATUS %s: primary=%d secondary=%d\n", r.ID, r.StatusA, r.StatusB)
		case r.BodyMismatch && len(r.Diffs) > 0:
			_, _ = fmt.Fprintf(w, "DIFF   %s: %d diff(s)\n", r.ID, len(r.Diffs))
			if verbose {
				for _, d := range r.Diffs {
					_, _ = fmt.Fprintf(w, "         %-10s %s: %v != %v\n", d.Kind, d.Path, d.A, d.B)
				}
			}
		case r.BodyMismatch:
			_, _ = fmt.Fprintf(w, "DIFF   %s: content-type drift (primary JSON=%v secondary JSON=%v)\n",
				r.ID, r.StatusA, r.StatusB)
		case verbose:
			_, _ = fmt.Fprintf(w, "OK     %s\n", r.ID)
		}
	}
	_, _ = fmt.Fprintf(w, "\nSummary: %d total, %d matched, %d mismatched, %d errored\n",
		summary.Total, summary.Matched, summary.Mismatched, summary.Errored)
}

func main() {
	primary := flag.String("primary", "", "Primary backend URL (e.g. http://localhost:8989)")
	secondary := flag.String("secondary", "", "Secondary backend URL (e.g. http://localhost:8990)")
	primaryKey := flag.String("primary-key", "", "X-Api-Key for primary backend")
	secondaryKey := flag.String("secondary-key", "", "X-Api-Key for secondary; defaults to primary-key when blank")
	corpusPath := flag.String("corpus", "", "Path to corpus JSON file")
	timeout := flag.Duration("timeout", 30*time.Second, "Per-entry request timeout")
	verbose := flag.Bool("v", false, "Print per-entry OK lines + expand each diff path")
	flag.Parse()

	if *primary == "" || *secondary == "" || *corpusPath == "" {
		fmt.Fprintln(os.Stderr, "diff: -primary, -secondary, and -corpus are required")
		flag.Usage()
		os.Exit(2)
	}
	pURL, err := url.Parse(*primary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diff: parse -primary: %v\n", err)
		os.Exit(2)
	}
	sURL, err := url.Parse(*secondary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diff: parse -secondary: %v\n", err)
		os.Exit(2)
	}
	corpus, err := spec.LoadCorpus(*corpusPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diff: load corpus: %v\n", err)
		os.Exit(1)
	}
	skey := *secondaryKey
	if skey == "" {
		skey = *primaryKey
	}

	results, summary, err := diffCorpus(context.Background(), Options{
		Primary:      pURL,
		Secondary:    sURL,
		PrimaryKey:   *primaryKey,
		SecondaryKey: skey,
		Corpus:       corpus,
		Timeout:      *timeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "diff: %v\n", err)
		os.Exit(1)
	}

	printReport(os.Stdout, results, summary, *verbose)
	if summary.Mismatched > 0 || summary.Errored > 0 {
		os.Exit(1)
	}
}
