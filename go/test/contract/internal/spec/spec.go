// Package spec defines the on-disk format for the contract-test corpus and
// golden files used to verify byte-equality between the C# v5 backend and
// the Go port.
package spec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// Corpus is a single corpus file — a slice of named request specs.
type Corpus struct {
	Entries []Entry `json:"entries"`
}

// Entry pairs a request with optional normalization applied to its response
// before equality is checked.
type Entry struct {
	ID        string    `json:"id"`
	Comment   string    `json:"comment,omitempty"`
	Request   Request   `json:"request"`
	Normalize Normalize `json:"normalize,omitempty"`
}

// Request describes a single HTTP request to issue against either backend.
type Request struct {
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   map[string][]string `json:"query,omitempty"`
	Headers map[string]string   `json:"headers,omitempty"`
	// Body is a JSON-encodable value. nil means no body.
	Body any `json:"body,omitempty"`
}

// Normalize controls which parts of a recorded response are masked before
// comparison. Every redaction must be justified by a Comment.
type Normalize struct {
	// RedactJSONPaths is a list of JSONPath expressions whose values are
	// replaced with the literal string "<redacted>" before diff.
	RedactJSONPaths []string `json:"redact_paths,omitempty"`
	// RedactHeaders is a list of response headers stripped before diff.
	RedactHeaders []string `json:"redact_headers,omitempty"`
}

// Golden is the recorded response for an Entry, committed to the repository.
type Golden struct {
	EntryID    string              `json:"entry_id"`
	Status     int                 `json:"status"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyJSON   json.RawMessage     `json:"body_json,omitempty"`
	BodyRaw    []byte              `json:"body_raw,omitempty"`
	BodyIsJSON bool                `json:"body_is_json"`
}

// LoadCorpus reads and parses a corpus file from disk.
func LoadCorpus(path string) (*Corpus, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read corpus %s: %w", path, err)
	}
	var c Corpus
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse corpus %s: %w", path, err)
	}
	for i, e := range c.Entries {
		if e.ID == "" {
			return nil, fmt.Errorf("corpus %s: entry %d missing id", path, i)
		}
		if e.Request.Method == "" || e.Request.Path == "" {
			return nil, fmt.Errorf("corpus %s entry %s: request method/path required", path, e.ID)
		}
		if !validMethod(e.Request.Method) {
			return nil, fmt.Errorf("corpus %s entry %s: invalid method %q", path, e.ID, e.Request.Method)
		}
	}
	return &c, nil
}

// SaveGolden writes a golden file in stable, pretty-printed form. HTML
// escaping is disabled so substituted values like "<redacted>" round-trip
// exactly — golden files are diffed byte-by-byte and any escaping change
// would break that contract.
func SaveGolden(path string, g *Golden) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(g); err != nil {
		return fmt.Errorf("marshal golden %s: %w", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write golden %s: %w", path, err)
	}
	return nil
}

// LoadGolden reads a golden file from disk.
func LoadGolden(path string) (*Golden, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read golden %s: %w", path, err)
	}
	var g Golden
	if err := json.Unmarshal(b, &g); err != nil {
		return nil, fmt.Errorf("parse golden %s: %w", path, err)
	}
	return &g, nil
}

func validMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}
