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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync"
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

// goldenSource is the per-entry reference response the primary
// (live Go backend) is diffed against. Two production
// implementations:
//
//   - liveSource wraps a runner.Client → hits a second backend
//     (typically a running C# Sonarr on staging).
//   - diskSource wraps spec.LoadGolden → reads a recorded golden
//     from disk so an operator can diagnose drift without a running
//     reference backend.
//
// The interface is unexported because diff is a binary, not a
// library — external consumers either invoke the CLI or import the
// underlying spec.LoadGolden / runner.Client directly.
type goldenSource interface {
	Fetch(ctx context.Context, e spec.Entry) (*spec.Golden, error)
}

// liveSource hits a live backend via runner.Client.
type liveSource struct{ client *runner.Client }

func (l liveSource) Fetch(ctx context.Context, e spec.Entry) (*spec.Golden, error) {
	return l.client.Run(ctx, e)
}

// diskSource resolves <dir>/<entryID>.golden.json via spec.LoadGolden.
// Path layout mirrors the contract-replayer (replayer.go:77) so the
// existing per-corpus golden trees Just Work.
type diskSource struct{ dir string }

func (d diskSource) Fetch(_ context.Context, e spec.Entry) (*spec.Golden, error) {
	// filepath.FromSlash keeps Windows operators happy when entry
	// IDs contain forward slashes (uncommon today; defensive).
	return spec.LoadGolden(filepath.Join(d.dir, filepath.FromSlash(e.ID)+".golden.json"))
}

// Options carries the diffCorpus inputs. Lifted out of main() so tests
// can drive the package without a process spawn.
//
// Either Secondary OR GoldenDir must be set — not both. The CLI
// translates -secondary / -golden into the corresponding source
// internally; tests can set either field directly.
type Options struct {
	Primary, Secondary       *url.URL
	PrimaryKey, SecondaryKey string
	GoldenDir                string
	Corpus                   *spec.Corpus
	Timeout                  time.Duration
	// Parallelism caps in-flight entries. 0 or 1 → serial replay
	// (the original 4.FN behaviour). >1 → bounded worker pool.
	// Reasonable production default for a typical 51-entry corpus
	// with ~100ms round-trip is 4 — caps wall-clock at ~corpus/4
	// without slamming the upstream with bursts.
	Parallelism int
}

// diffCorpus walks every corpus entry, hits both backends, and
// returns the per-entry results + the summary. Errors out only on
// catastrophic misconfiguration (nil corpus / nil base URLs); per-
// entry failures land in Result.Err so the summary still reports a
// full pass.
//
// When Options.Parallelism > 1, entries are processed by a bounded
// worker pool; the returned Results slice is still ordered by the
// original corpus order (sorting for display happens in sortedByID
// / the report printers). Per-entry timeouts apply per-entry — a
// slow request from one worker doesn't extend the budget for any
// other worker.
func diffCorpus(ctx context.Context, opts Options) ([]Result, Summary, error) {
	if opts.Corpus == nil {
		return nil, Summary{}, errors.New("diff: nil corpus")
	}
	if opts.Primary == nil {
		return nil, Summary{}, errors.New("diff: primary base URL required")
	}
	if (opts.Secondary == nil) == (opts.GoldenDir == "") {
		return nil, Summary{}, errors.New("diff: exactly one of secondary URL or GoldenDir must be set")
	}
	// HTTP is set up-front (not via runner.Client's lazy default)
	// so concurrent Run() calls don't race on the lazy-init write
	// to c.HTTP. The race detector catches this even though
	// repeated nil→DefaultClient assignments would converge.
	httpClient := http.DefaultClient
	pClient := &runner.Client{Base: opts.Primary, APIKey: opts.PrimaryKey, HTTP: httpClient}
	var bSource goldenSource = liveSource{client: pClient} // shadowed below
	if opts.Secondary != nil {
		bSource = liveSource{client: &runner.Client{Base: opts.Secondary, APIKey: opts.SecondaryKey, HTTP: httpClient}}
	} else {
		bSource = diskSource{dir: opts.GoldenDir}
	}

	results := make([]Result, len(opts.Corpus.Entries))
	var summary Summary
	summary.Total = len(opts.Corpus.Entries)

	// Bounded-concurrency semaphore. The token channel doubles as
	// a wait-for-completion barrier (drain by acquiring N tokens
	// at the end).
	parallelism := opts.Parallelism
	if parallelism < 1 {
		parallelism = 1
	}
	sem := make(chan struct{}, parallelism)
	var wg sync.WaitGroup
	for i, e := range opts.Corpus.Entries {
		wg.Add(1)
		go func(i int, e spec.Entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = runEntry(ctx, e, opts.Timeout, pClient, bSource)
		}(i, e)
	}
	wg.Wait()

	// Aggregate the summary after all workers complete — avoids
	// the lock-per-entry overhead a streaming counter would need
	// and keeps the result type free of synchronisation guts.
	for _, r := range results {
		switch {
		case r.Err != nil:
			summary.Errored++
		case !r.StatusMatch || r.BodyMismatch:
			summary.Mismatched++
		default:
			summary.Matched++
		}
	}
	return results, summary, nil
}

// runEntry processes a single corpus entry against the primary +
// reference source and returns the populated Result. Extracted from
// diffCorpus so per-entry context cancellation lives in its own
// stack frame — no defer-in-loop accumulation.
func runEntry(ctx context.Context, e spec.Entry, timeout time.Duration, pClient *runner.Client, bSource goldenSource) Result {
	entryCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		entryCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	r := Result{ID: e.ID}
	pG, pErr := pClient.Run(entryCtx, e)
	sG, sErr := bSource.Fetch(entryCtx, e)
	switch {
	case pErr != nil && sErr != nil:
		r.Err = fmt.Errorf("both sides failed: primary=%w reference=%w", pErr, sErr)
	case pErr != nil:
		r.Err = fmt.Errorf("primary failed: %w", pErr)
	case sErr != nil:
		r.Err = fmt.Errorf("reference failed: %w", sErr)
	}
	if r.Err != nil {
		return r
	}

	r.StatusA = pG.Status
	r.StatusB = sG.Status
	r.StatusMatch = pG.Status == sG.Status
	r.BothJSON = pG.BodyIsJSON && sG.BodyIsJSON
	if r.BothJSON {
		diffs, derr := normalize.JSONDiff(pG.BodyJSON, sG.BodyJSON)
		if derr != nil {
			r.Err = fmt.Errorf("diff %s: %w", e.ID, derr)
			return r
		}
		r.Diffs = diffs
		r.BodyMismatch = len(diffs) > 0
	} else if pG.BodyIsJSON != sG.BodyIsJSON {
		// One side returned JSON, the other didn't. Treat as a
		// content-type drift — count as mismatch.
		r.BodyMismatch = true
	}
	return r
}

// sortedByID returns a copy of results sorted alphabetically by ID
// so every render path emits in a deterministic order. The two
// printers (text + JSON) share this helper.
func sortedByID(results []Result) []Result {
	out := make([]Result, len(results))
	copy(out, results)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// printReport renders the per-entry result list + a final summary
// line to w. verbose=true expands every Diff into a path-by-path
// listing; otherwise only the count surfaces per entry.
func printReport(w io.Writer, results []Result, summary Summary, verbose bool) {
	for _, r := range sortedByID(results) {
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

// jsonReport mirrors printReport's structure but as a single JSON
// document — for nightly CI / dashboards / drift-trend analysis. The
// JSON shape is intentionally flat so jq + simple tools can hop in
// without a schema package.
//
// Per-entry result kinds (statusMismatch / bodyMismatch / err) are
// all rendered explicitly; consumers don't need to recompute them
// from raw fields. diffs is omitted (not null) when no diffs apply,
// keeping the document small for the typical clean-run case.
type jsonReport struct {
	Summary jsonSummary  `json:"summary"`
	Results []jsonResult `json:"results"`
}

type jsonSummary struct {
	Total      int `json:"total"`
	Matched    int `json:"matched"`
	Mismatched int `json:"mismatched"`
	Errored    int `json:"errored"`
}

type jsonResult struct {
	ID           string     `json:"id"`
	StatusA      int        `json:"statusA"`
	StatusB      int        `json:"statusB"`
	StatusMatch  bool       `json:"statusMatch"`
	BodyMismatch bool       `json:"bodyMismatch"`
	Diffs        []jsonDiff `json:"diffs,omitempty"`
	Err          string     `json:"err,omitempty"`
}

type jsonDiff struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	A    any    `json:"a,omitempty"`
	B    any    `json:"b,omitempty"`
}

// printReportJSON serialises a report as JSON. Pretty-printed (2sp
// indent) so the artefact is human-greppable as well as
// machine-parseable.
func printReportJSON(w io.Writer, results []Result, summary Summary) error {
	out := jsonReport{
		Summary: jsonSummary(summary),
		Results: make([]jsonResult, 0, len(results)),
	}
	for _, r := range sortedByID(results) {
		jr := jsonResult{
			ID:           r.ID,
			StatusA:      r.StatusA,
			StatusB:      r.StatusB,
			StatusMatch:  r.StatusMatch,
			BodyMismatch: r.BodyMismatch,
		}
		if r.Err != nil {
			jr.Err = r.Err.Error()
		}
		if len(r.Diffs) > 0 {
			jr.Diffs = make([]jsonDiff, 0, len(r.Diffs))
			for _, d := range r.Diffs {
				jr.Diffs = append(jr.Diffs, jsonDiff{
					Path: d.Path, Kind: d.Kind.String(), A: d.A, B: d.B,
				})
			}
		}
		out.Results = append(out.Results, jr)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func main() {
	primary := flag.String("primary", "", "Primary backend URL (e.g. http://localhost:8989)")
	secondary := flag.String("secondary", "", "Secondary backend URL — exactly one of -secondary or -golden required")
	golden := flag.String("golden", "", "Directory of recorded golden JSON files (e.g. ./test/contract/golden/v3-empty-state); the per-corpus subdir matching -corpus")
	primaryKey := flag.String("primary-key", "", "X-Api-Key for primary backend")
	secondaryKey := flag.String("secondary-key", "", "X-Api-Key for secondary; defaults to primary-key when blank (ignored when -golden is set)")
	corpusPath := flag.String("corpus", "", "Path to corpus JSON file")
	timeout := flag.Duration("timeout", 30*time.Second, "Per-entry request timeout")
	verbose := flag.Bool("v", false, "Print per-entry OK lines + expand each diff path (text format only)")
	format := flag.String("format", "text", "Output format: text | json")
	parallelism := flag.Int("parallelism", 4, "Max in-flight entries (1 = serial, matches the original 4.FN replay shape)")
	flag.Parse()

	if *format != "text" && *format != "json" {
		fmt.Fprintf(os.Stderr, "diff: -format must be text or json (got %q)\n", *format)
		os.Exit(2)
	}

	if *primary == "" || *corpusPath == "" {
		fmt.Fprintln(os.Stderr, "diff: -primary and -corpus are required")
		flag.Usage()
		os.Exit(2)
	}
	if (*secondary == "") == (*golden == "") {
		fmt.Fprintln(os.Stderr, "diff: exactly one of -secondary or -golden is required")
		flag.Usage()
		os.Exit(2)
	}
	pURL, err := url.Parse(*primary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "diff: parse -primary: %v\n", err)
		os.Exit(2)
	}
	var sURL *url.URL
	if *secondary != "" {
		sURL, err = url.Parse(*secondary)
		if err != nil {
			fmt.Fprintf(os.Stderr, "diff: parse -secondary: %v\n", err)
			os.Exit(2)
		}
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
		GoldenDir:    *golden,
		Corpus:       corpus,
		Timeout:      *timeout,
		Parallelism:  *parallelism,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "diff: %v\n", err)
		os.Exit(1)
	}

	switch *format {
	case "json":
		if err := printReportJSON(os.Stdout, results, summary); err != nil {
			fmt.Fprintf(os.Stderr, "diff: encode json report: %v\n", err)
			os.Exit(1)
		}
	default:
		printReport(os.Stdout, results, summary, *verbose)
	}
	if summary.Mismatched > 0 || summary.Errored > 0 {
		os.Exit(1)
	}
}
