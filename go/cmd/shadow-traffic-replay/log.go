// Package main — log.go defines the on-disk JSONL log format consumed
// by this binary and the parser that streams it.
//
// One line = one captured HTTP request. The shape mirrors what a
// reverse-proxy / tee tool would emit during a recording run. Reading
// is streaming (not slurp-all) so a multi-GB capture from a real
// Sonarr install replays without loading the whole file into memory.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// LogEntry is one captured request from a recording.
//
// All fields are optional except Method + Path. An entry with no
// Timestamp replays as fast as the speed multiplier allows (no gap).
// An entry with no Headers replays with just the headers this binary
// injects (Content-Type from Body presence + User-Agent).
type LogEntry struct {
	// Timestamp is when the original request was captured. Used to
	// pace replay (next-entry-Timestamp - prev-entry-Timestamp =
	// sleep duration, scaled by Speed).
	Timestamp time.Time `json:"ts,omitzero"`
	// Method is the HTTP method. Required.
	Method string `json:"method"`
	// Path is the request path including any query string. Required.
	Path string `json:"path"`
	// Headers carries inbound headers. Hop-by-hop headers and Host
	// are stripped at replay time.
	Headers map[string]string `json:"headers,omitempty"`
	// Body is the request body. Raw bytes — if the recording was
	// JSON, it's the JSON string verbatim. Empty for GET/DELETE.
	Body string `json:"body,omitempty"`
	// Status is the recorded response status (for filtering — the
	// replay doesn't re-emit responses, it just dispatches).
	Status int `json:"status,omitempty"`
}

// LogReader streams LogEntry values from a JSONL source.
type LogReader struct {
	scanner *bufio.Scanner
	line    int
}

// NewLogReader wraps an io.Reader. The caller retains ownership of
// the underlying reader.
func NewLogReader(r io.Reader) *LogReader {
	s := bufio.NewScanner(r)
	// Allow lines up to 4 MiB — captures with large POST bodies
	// (custom-format bulk, manualimport) need more than the default
	// 64 KiB.
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return &LogReader{scanner: s}
}

// Next reads one LogEntry. Returns io.EOF when the stream is drained.
// Malformed lines return an error with the line number for the
// operator to triage.
func (r *LogReader) Next() (LogEntry, error) {
	for {
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				return LogEntry{}, fmt.Errorf("log read line %d: %w", r.line+1, err)
			}
			return LogEntry{}, io.EOF
		}
		r.line++
		raw := strings.TrimSpace(r.scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			// Allow blank lines and # comments for hand-edited logs.
			continue
		}
		var e LogEntry
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			return LogEntry{}, fmt.Errorf("log parse line %d: %w", r.line, err)
		}
		if e.Method == "" || e.Path == "" {
			return LogEntry{}, fmt.Errorf("log line %d: method and path are required", r.line)
		}
		return e, nil
	}
}

// OpenLogFile is a convenience wrapper around os.Open + NewLogReader.
// The caller is responsible for closing the returned file.
func OpenLogFile(path string) (*os.File, *LogReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open log: %w", err)
	}
	return f, NewLogReader(f), nil
}

// Filter narrows the stream by method / path-prefix / status.
//
// Empty fields are wildcards. Method match is exact-uppercase. Path
// match is prefix (any entry whose Path starts with one of the
// configured prefixes passes). Status match accepts either an exact
// code or a class (e.g. "2xx", "4xx").
type Filter struct {
	Methods       []string
	PathPrefixes  []string
	StatusCodes   []int
	StatusClasses []string // e.g. "2xx", "4xx", "5xx"
}

// Match returns true if e passes all configured filters. An empty
// Filter passes every entry.
func (f Filter) Match(e LogEntry) bool {
	if len(f.Methods) > 0 {
		hit := false
		for _, m := range f.Methods {
			if strings.EqualFold(m, e.Method) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(f.PathPrefixes) > 0 {
		hit := false
		for _, p := range f.PathPrefixes {
			if strings.HasPrefix(e.Path, p) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(f.StatusCodes) > 0 || len(f.StatusClasses) > 0 {
		hit := false
		for _, c := range f.StatusCodes {
			if c == e.Status {
				hit = true
				break
			}
		}
		if !hit {
			for _, c := range f.StatusClasses {
				if statusClassMatch(c, e.Status) {
					hit = true
					break
				}
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// statusClassMatch returns true if class (e.g. "2xx") matches code.
// Empty class or non-conforming string never matches.
func statusClassMatch(class string, code int) bool {
	if len(class) != 3 || class[1] != 'x' || class[2] != 'x' {
		return false
	}
	if class[0] < '1' || class[0] > '5' {
		return false
	}
	base := int(class[0]-'0') * 100
	return code >= base && code < base+100
}

// ParseStatusFilter splits a comma-separated string like
// "200,4xx,503" into ([]int, []string). Errors on malformed tokens.
func ParseStatusFilter(s string) ([]int, []string, error) {
	if s == "" {
		return nil, nil, nil
	}
	var codes []int
	var classes []string
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if len(tok) == 3 && tok[1] == 'x' && tok[2] == 'x' {
			classes = append(classes, tok)
			continue
		}
		var n int
		if _, err := fmt.Sscanf(tok, "%d", &n); err != nil || n < 100 || n > 599 {
			return nil, nil, fmt.Errorf("bad status filter token %q (want 2xx-style class or 3-digit code)", tok)
		}
		codes = append(codes, n)
	}
	return codes, classes, nil
}

// ErrLogClosed signals callers that further Next() calls will fail
// because the underlying reader is gone.
var ErrLogClosed = errors.New("log stream closed")
