package normalize

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Difference is one path-keyed mismatch between two JSON documents.
// Kind classifies the mismatch shape so callers can render or filter
// without re-deriving it from the values.
//
//   - KindMissingInA — Path exists in B but not A.
//   - KindMissingInB — Path exists in A but not B.
//   - KindTypeMismatch — both sides have the path but with different
//     JSON kinds (object vs array, number vs string, ...).
//   - KindValueMismatch — same kind, different scalar value (or array
//     length mismatch with same element types).
//
// Path uses JSONPath-ish dotted/bracketed notation: "$", "$.foo",
// "$.foo.bar", "$[2]", "$.items[0].id".
type Difference struct {
	Path string
	Kind DiffKind
	A    any
	B    any
}

// DiffKind classifies a Difference. See [Difference] for semantics.
type DiffKind int

const (
	// KindValueMismatch is the default — same JSON kind at Path on
	// both sides, but the scalar values (or array length) differ.
	KindValueMismatch DiffKind = iota
	// KindTypeMismatch means A and B carry incompatible JSON kinds
	// at Path (object vs array, number vs string, etc.).
	KindTypeMismatch
	// KindMissingInA means Path exists in B but not A.
	KindMissingInA
	// KindMissingInB means Path exists in A but not B.
	KindMissingInB
)

func (k DiffKind) String() string {
	switch k {
	case KindValueMismatch:
		return "value"
	case KindTypeMismatch:
		return "type"
	case KindMissingInA:
		return "missingInA"
	case KindMissingInB:
		return "missingInB"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// JSONDiff parses two JSON documents and returns the path-keyed
// differences between them. Returns nil + nil when the documents are
// structurally equal after JSON parsing (whitespace + key ordering
// don't matter).
//
// Use cases (Phase 6 shadow-traffic):
//
//   - Drift detector: run the same request against C# + Go binaries,
//     pass both bodies through normalize.JSON with the corpus
//     redact_paths, then JSONDiff — non-empty output means real
//     drift that survives redaction.
//   - Contract bake: while still catching bit-for-bit drift via
//     byte-equality, JSONDiff lets failing tests report the *what*
//     (rather than just "bodies differ at byte N").
//
// Empty inputs are treated as JSON `null`. A failed unmarshal is
// returned as an error — JSONDiff doesn't fall back to byte compare.
func JSONDiff(a, b []byte) ([]Difference, error) {
	var av, bv any
	if len(a) > 0 {
		if err := json.Unmarshal(a, &av); err != nil {
			return nil, fmt.Errorf("normalize: parse A: %w", err)
		}
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &bv); err != nil {
			return nil, fmt.Errorf("normalize: parse B: %w", err)
		}
	}
	var out []Difference
	diffWalk("$", av, bv, &out)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// diffWalk is the recursive comparator. Appends to *out instead of
// returning so deeply-nested mismatch lists don't pay per-frame slice
// allocation cost.
func diffWalk(path string, a, b any, out *[]Difference) {
	// Both nil → equal. nil vs anything else → handled below as
	// type/value mismatch since reflect would surface them as
	// (untyped nil, typed thing).
	if a == nil && b == nil {
		return
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok {
			*out = append(*out, Difference{Path: path, Kind: KindTypeMismatch, A: a, B: b})
			return
		}
		// Sort keys so the diff output is stable across runs —
		// JSON map iteration order is randomised by encoding/json.
		keys := make([]string, 0, len(av)+len(bv))
		seen := make(map[string]struct{}, len(av)+len(bv))
		for k := range av {
			if _, dup := seen[k]; !dup {
				keys = append(keys, k)
				seen[k] = struct{}{}
			}
		}
		for k := range bv {
			if _, dup := seen[k]; !dup {
				keys = append(keys, k)
				seen[k] = struct{}{}
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			aVal, aHas := av[k]
			bVal, bHas := bv[k]
			child := path + "." + k
			switch {
			case aHas && !bHas:
				*out = append(*out, Difference{Path: child, Kind: KindMissingInB, A: aVal, B: nil})
			case !aHas && bHas:
				*out = append(*out, Difference{Path: child, Kind: KindMissingInA, A: nil, B: bVal})
			default:
				diffWalk(child, aVal, bVal, out)
			}
		}
	case []any:
		bv, ok := b.([]any)
		if !ok {
			*out = append(*out, Difference{Path: path, Kind: KindTypeMismatch, A: a, B: b})
			return
		}
		if len(av) != len(bv) {
			// Surface length mismatch ONCE at the array path, then
			// recurse pairwise on the shared prefix so per-index
			// content diffs still surface for callers that want to
			// see them. Tail elements present in only one side are
			// reported via diffWalk's nil-vs-typed asymmetry.
			*out = append(*out, Difference{Path: path, Kind: KindValueMismatch, A: len(av), B: len(bv)})
		}
		shared := len(av)
		if len(bv) < shared {
			shared = len(bv)
		}
		for i := 0; i < shared; i++ {
			diffWalk(fmt.Sprintf("%s[%d]", path, i), av[i], bv[i], out)
		}
		// Report tail elements via "missing on the shorter side"
		// entries so callers see the actual values rather than
		// having to re-derive them from the length count.
		for i := shared; i < len(av); i++ {
			*out = append(*out, Difference{Path: fmt.Sprintf("%s[%d]", path, i), Kind: KindMissingInB, A: av[i]})
		}
		for i := shared; i < len(bv); i++ {
			*out = append(*out, Difference{Path: fmt.Sprintf("%s[%d]", path, i), Kind: KindMissingInA, B: bv[i]})
		}
	default:
		// Scalars — string, float64 (JSON numbers), bool, nil. Use
		// %v to compare since == on `any` works for these basic
		// kinds. If the JSON kinds disagree (e.g. number vs string)
		// we surface it as a type mismatch.
		if a == nil || b == nil {
			if a != b {
				*out = append(*out, Difference{Path: path, Kind: KindValueMismatch, A: a, B: b})
			}
			return
		}
		if fmt.Sprintf("%T", a) != fmt.Sprintf("%T", b) {
			*out = append(*out, Difference{Path: path, Kind: KindTypeMismatch, A: a, B: b})
			return
		}
		if a != b {
			*out = append(*out, Difference{Path: path, Kind: KindValueMismatch, A: a, B: b})
		}
	}
}
