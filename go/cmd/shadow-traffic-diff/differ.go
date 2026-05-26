package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"sonarr.tv/sonarr/internal/normalize"
)

// RedactConfig wires endpoint-path patterns to JSONPath redact lists.
// The matching order is: first matching prefix wins; an empty Path
// matches all endpoints (use this for cross-cutting redactions like
// `$.timestamp` that appear in many shapes).
//
// JSON file shape:
//
//	{
//	  "rules": [
//	    {"path_prefix": "/api/v3/system/task",   "redact_paths": ["$[*].lastDuration", "$.lastDuration"]},
//	    {"path_prefix": "/api/v3/command",       "redact_paths": ["$[*].queued", "$[*].started", "$[*].ended"]},
//	    {"path_prefix": "",                      "redact_paths": ["$.timestamp"]}
//	  ]
//	}
type RedactConfig struct {
	Rules []RedactRule `json:"rules"`
}

// RedactRule is one matcher in the redact config.
type RedactRule struct {
	PathPrefix  string   `json:"path_prefix"`
	RedactPaths []string `json:"redact_paths"`
}

// LoadRedactConfig parses a JSON config file.
func LoadRedactConfig(path string) (*RedactConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read redact-config: %w", err)
	}
	var cfg RedactConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse redact-config: %w", err)
	}
	return &cfg, nil
}

// DefaultRedactConfig returns a baseline config covering the dynamic
// fields surfaced by the J234 route coverage audit + J231 lastDuration
// redaction work. The list is conservative — most shadow-traffic noise
// can be silenced by appending here rather than hand-editing per-host
// config.
func DefaultRedactConfig() *RedactConfig {
	return &RedactConfig{
		Rules: []RedactRule{
			{
				PathPrefix:  "/api/v3/system/task",
				RedactPaths: []string{"$[*].lastDuration", "$.lastDuration", "$[*].nextExecution", "$.nextExecution", "$[*].lastExecution", "$.lastExecution", "$[*].lastStartTime", "$.lastStartTime"},
			},
			{
				PathPrefix:  "/api/v3/command",
				RedactPaths: []string{"$[*].queued", "$[*].started", "$[*].ended", "$[*].duration", "$[*].stateChangeTime", "$[*].lastExecutionTime", "$.queued", "$.started", "$.ended", "$.duration", "$.stateChangeTime", "$.lastExecutionTime"},
			},
			{
				PathPrefix:  "/api/v3/health",
				RedactPaths: []string{"$[*].source"},
			},
			{
				PathPrefix:  "/api/v3/log",
				RedactPaths: []string{"$.records[*].time", "$.records[*].exception", "$.records[*].id"},
			},
			{
				PathPrefix:  "/api/v3/system/status",
				RedactPaths: []string{"$.startTime", "$.buildTime", "$.runtimeVersion", "$.version", "$.osVersion", "$.osName", "$.architecture"},
			},
			{
				PathPrefix:  "/api/v3/diskspace",
				RedactPaths: []string{"$[*].freeSpace", "$[*].totalSpace"},
			},
		},
	}
}

// RedactPathsFor returns the merged redact list for an endpoint path.
// Rules whose PathPrefix is empty always contribute; rules whose
// PathPrefix matches the start of the endpoint also contribute. The
// concatenation is stable in declaration order.
func (c *RedactConfig) RedactPathsFor(endpointPath string) []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, r := range c.Rules {
		if r.PathPrefix == "" || strings.HasPrefix(endpointPath, r.PathPrefix) {
			out = append(out, r.RedactPaths...)
		}
	}
	return out
}

// Differ wraps the test/contract/internal/normalize machinery so callers
// see a single Diff method.
type Differ struct {
	cfg *RedactConfig
}

// NewDiffer constructs a Differ. cfg may be nil — equivalent to no
// redactions (every difference surfaces).
func NewDiffer(cfg *RedactConfig) *Differ {
	return &Differ{cfg: cfg}
}

// Diff applies the endpoint-specific redactions to both bodies, then
// runs the JSON diff. Returns a flat list of human-readable diff
// strings (one per Difference) + a Severity classification.
//
// Severity rules:
//
//   - both bodies parse + diff produces only KindValueMismatch entries
//     → SeverityLow (likely an environmental-noise field not yet
//     redacted; safe to triage in the daily report).
//   - any KindMissingInA, KindMissingInB, KindTypeMismatch present →
//     SeverityHigh (structural divergence — a real port gap).
//   - one or both bodies fail to parse as JSON →
//     SeverityHigh (almost always means a content-type mismatch or a
//     500-style HTML error from one side).
func (d *Differ) Diff(endpointPath string, primary, secondary []byte) ([]string, Severity, error) {
	paths := d.cfg.RedactPathsFor(endpointPath)
	a, err := normalize.JSON(primary, paths)
	if err != nil {
		// Primary is not JSON or is unparseable.
		// Fall back to byte-equality.
		if string(primary) == string(secondary) {
			return nil, SeverityOK, nil
		}
		return []string{"non-JSON body mismatch (primary unparseable: " + err.Error() + ")"}, SeverityHigh, nil
	}
	b, err := normalize.JSON(secondary, paths)
	if err != nil {
		if string(a) == string(secondary) {
			return nil, SeverityOK, nil
		}
		return []string{"non-JSON body mismatch (secondary unparseable: " + err.Error() + ")"}, SeverityHigh, nil
	}
	diffs, err := normalize.JSONDiff(a, b)
	if err != nil {
		return nil, SeverityHigh, err
	}
	if len(diffs) == 0 {
		return nil, SeverityOK, nil
	}

	sev := SeverityLow
	out := make([]string, 0, len(diffs))
	for _, dd := range diffs {
		if dd.Kind != normalize.KindValueMismatch {
			sev = SeverityHigh
		}
		out = append(out, fmt.Sprintf("%s @ %s: A=%v B=%v", dd.Kind, dd.Path, dd.A, dd.B))
	}
	return out, sev, nil
}
