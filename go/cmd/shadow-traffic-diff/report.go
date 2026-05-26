package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Severity classifies a single diff event.
type Severity int

// Severity values, ordered by escalating urgency.
const (
	SeverityOK Severity = iota
	SeverityLow
	SeverityHigh
)

// String returns the JSON-friendly tag for a Severity.
func (s Severity) String() string {
	switch s {
	case SeverityOK:
		return "ok"
	case SeverityLow:
		return "low"
	case SeverityHigh:
		return "high"
	}
	return "unknown"
}

// DiffRow is one recorded diff event. The shape mirrors what an
// operator wants to see in the daily report — endpoint + summary +
// timing.
type DiffRow struct {
	Timestamp        time.Time     `json:"timestamp"`
	Method           string        `json:"method"`
	Path             string        `json:"path"`
	PrimaryStatus    int           `json:"primary_status"`
	SecondaryStatus  int           `json:"secondary_status"`
	PrimaryLatency   time.Duration `json:"primary_latency"`
	SecondaryLatency time.Duration `json:"secondary_latency"`
	PrimaryLabel     string        `json:"primary_label"`
	SecondaryLabel   string        `json:"secondary_label"`
	Diffs            []string      `json:"diffs"`
	Severity         Severity      `json:"severity"`
}

// MarshalJSON serialises the severity as its string tag.
func (r DiffRow) MarshalJSON() ([]byte, error) {
	type alias DiffRow
	return json.Marshal(struct {
		alias
		Severity string `json:"severity"`
	}{alias: alias(r), Severity: r.Severity.String()})
}

// OKBucket aggregates byte-equal events so the operator can see how
// many requests passed without polluting the per-event log.
type OKBucket struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	Count      int    `json:"count"`
	LastSeenAt time.Time
}

// Report buffers diff events in memory and periodically flushes them
// to a date-stamped JSON file. The report is grouped by severity:
//
//	{report-dir}/2026-05-26.diffs.json    — high + low severity events
//	{report-dir}/2026-05-26.ok-summary.json — aggregated byte-equal counts
//
// The flush is safe to call concurrently with Record / RecordOK.
type Report struct {
	dir    string
	logger *slog.Logger

	mu        sync.Mutex
	rows      []DiffRow
	okBuckets map[string]*OKBucket // key: method+path+status
}

// NewReport constructs a Report writing to dir.
func NewReport(dir string, logger *slog.Logger) *Report {
	return &Report{
		dir:       dir,
		logger:    logger,
		okBuckets: make(map[string]*OKBucket),
	}
}

// Record appends a DiffRow to the in-memory buffer.
func (r *Report) Record(row DiffRow) {
	r.mu.Lock()
	r.rows = append(r.rows, row)
	r.mu.Unlock()
}

// RecordOK increments the byte-equal counter for the (method, path,
// status) tuple. OK events never hit the per-event log.
func (r *Report) RecordOK(method, path string, status int) {
	key := method + " " + path + " " + itoa(status)
	r.mu.Lock()
	defer r.mu.Unlock()
	bucket, ok := r.okBuckets[key]
	if !ok {
		bucket = &OKBucket{Method: method, Path: path, Status: status}
		r.okBuckets[key] = bucket
	}
	bucket.Count++
	bucket.LastSeenAt = time.Now().UTC()
}

// Flush writes the current buffer to disk. The buffer is reset on
// success; on failure the in-memory state is preserved for the next
// flush attempt.
//
// Both files are append-mode: re-running on the same day extends the
// existing report rather than truncating it. This makes a service
// restart non-destructive to the day's accumulated data.
func (r *Report) Flush() error {
	r.mu.Lock()
	rows := r.rows
	okBuckets := r.okBuckets
	r.rows = nil
	r.okBuckets = make(map[string]*OKBucket)
	r.mu.Unlock()

	if len(rows) == 0 && len(okBuckets) == 0 {
		return nil
	}

	date := time.Now().UTC().Format("2006-01-02")
	if err := r.appendDiffs(filepath.Join(r.dir, date+".diffs.jsonl"), rows); err != nil {
		// Put the data back so a future flush retries.
		r.mu.Lock()
		r.rows = append(rows, r.rows...)
		r.mu.Unlock()
		return fmt.Errorf("flush diffs: %w", err)
	}
	if err := r.appendOK(filepath.Join(r.dir, date+".ok-summary.json"), okBuckets); err != nil {
		// OK-summary failure: best-effort. Don't restore the
		// counts (operator will see a gap; better than blocking
		// the diff log).
		r.logger.Warn("flush ok-summary failed", "err", err)
	}
	return nil
}

func (r *Report) appendDiffs(path string, rows []DiffRow) error {
	if len(rows) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

func (r *Report) appendOK(path string, buckets map[string]*OKBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	// Load existing if present.
	existing := make(map[string]*OKBucket)
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		var raw []OKBucket
		if err := json.Unmarshal(b, &raw); err == nil {
			for i := range raw {
				bucket := raw[i]
				key := bucket.Method + " " + bucket.Path + " " + itoa(bucket.Status)
				existing[key] = &bucket
			}
		}
	}
	for k, v := range buckets {
		if e, ok := existing[k]; ok {
			e.Count += v.Count
			if v.LastSeenAt.After(e.LastSeenAt) {
				e.LastSeenAt = v.LastSeenAt
			}
		} else {
			existing[k] = v
		}
	}
	// Write back sorted for determinism.
	keys := make([]string, 0, len(existing))
	for k := range existing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make([]OKBucket, 0, len(keys))
	for _, k := range keys {
		ordered = append(ordered, *existing[k])
	}
	out, err := json.MarshalIndent(ordered, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}
