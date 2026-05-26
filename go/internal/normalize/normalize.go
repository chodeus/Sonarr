// Package normalize applies redactions to a recorded HTTP response so that
// values which legitimately differ between runs (timestamps, generated IDs,
// random tokens) do not cause spurious contract diffs.
//
// The supported JSONPath subset is intentionally tiny: a single recursive
// descent and explicit nested keys, with a "[*]" wildcard for slices.
// Examples:
//
//	$.lastInfoSync         → top-level field
//	$[*].added             → array of objects, "added" on each
//	$.images[*].url        → nested array
//
// Anything more exotic should be handled by adding the field to the redact
// list at the leaf path it actually appears under, not by extending the
// expression language.
package normalize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// RedactedPlaceholder is substituted for every redacted value.
const RedactedPlaceholder = "<redacted>"

// JSON applies the given JSONPath redactions to a JSON document and returns
// the canonicalized (indented) output. Unknown paths are silently ignored —
// the corpus is allowed to redact fields that are not present in every
// response variant (e.g. an optional field).
func JSON(body []byte, paths []string) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("normalize: parse json: %w", err)
	}
	for _, p := range paths {
		segments, err := parsePath(p)
		if err != nil {
			return nil, fmt.Errorf("normalize: %w", err)
		}
		root = redact(root, segments)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("normalize: marshal: %w", err)
	}
	// json.Encoder always appends '\n'; trim so callers control trailing newline.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Headers returns a copy of h with the named keys removed (case-insensitive).
func Headers(h map[string][]string, drop []string) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	dropSet := make(map[string]struct{}, len(drop))
	for _, k := range drop {
		dropSet[strings.ToLower(k)] = struct{}{}
	}
	out := make(map[string][]string, len(h))
	for k, v := range h {
		if _, skip := dropSet[strings.ToLower(k)]; skip {
			continue
		}
		out[k] = append([]string(nil), v...)
	}
	return out
}

// --- internal ---

type segment struct {
	key      string // field name; "" if wildcard
	wildcard bool
}

func parsePath(p string) ([]segment, error) {
	if !strings.HasPrefix(p, "$") {
		return nil, fmt.Errorf("path %q must start with $", p)
	}
	body := p[1:]
	var segs []segment
	for body != "" {
		switch body[0] {
		case '.':
			body = body[1:]
			i := 0
			for i < len(body) && body[i] != '.' && body[i] != '[' {
				i++
			}
			if i == 0 {
				return nil, fmt.Errorf("path %q: empty key after dot", p)
			}
			segs = append(segs, segment{key: body[:i]})
			body = body[i:]
		case '[':
			end := strings.Index(body, "]")
			if end < 0 {
				return nil, fmt.Errorf("path %q: unmatched [", p)
			}
			inner := body[1:end]
			if inner != "*" {
				return nil, fmt.Errorf("path %q: only [*] supported, got [%s]", p, inner)
			}
			segs = append(segs, segment{wildcard: true})
			body = body[end+1:]
		default:
			return nil, fmt.Errorf("path %q: unexpected %q", p, body[0])
		}
	}
	return segs, nil
}

func redact(node any, segs []segment) any {
	if len(segs) == 0 {
		return RedactedPlaceholder
	}
	head, rest := segs[0], segs[1:]
	switch v := node.(type) {
	case map[string]any:
		if head.wildcard {
			for k, child := range v {
				v[k] = redact(child, rest)
			}
			return v
		}
		if _, ok := v[head.key]; ok {
			v[head.key] = redact(v[head.key], rest)
		}
		return v
	case []any:
		if !head.wildcard {
			return v
		}
		for i, child := range v {
			v[i] = redact(child, rest)
		}
		return v
	default:
		return v
	}
}
