package normalize_test

import (
	"strings"
	"testing"

	"sonarr.tv/sonarr/test/contract/internal/normalize"
)

func TestJSONDiff_Equal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, a, b string
	}{
		{"identical objects", `{"a":1,"b":"two"}`, `{"a":1,"b":"two"}`},
		{"key order ignored", `{"a":1,"b":2}`, `{"b":2,"a":1}`},
		{"whitespace ignored", `{"a":1}`, `{"a": 1   }`},
		{"identical arrays", `[1,2,3]`, `[1,2,3]`},
		{"nested equal", `{"x":{"y":[1,{"z":true}]}}`, `{"x":{"y":[1,{"z":true}]}}`},
		{"both empty", ``, ``},
		{"both null", `null`, `null`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			diffs, err := normalize.JSONDiff([]byte(c.a), []byte(c.b))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if len(diffs) != 0 {
				t.Errorf("expected no diffs; got %+v", diffs)
			}
		})
	}
}

func TestJSONDiff_ScalarMismatch(t *testing.T) {
	t.Parallel()
	diffs, err := normalize.JSONDiff([]byte(`{"a":1}`), []byte(`{"a":2}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff; got %d: %+v", len(diffs), diffs)
	}
	d := diffs[0]
	if d.Path != "$.a" {
		t.Errorf("path: %q", d.Path)
	}
	if d.Kind != normalize.KindValueMismatch {
		t.Errorf("kind: %v", d.Kind)
	}
}

func TestJSONDiff_TypeMismatch(t *testing.T) {
	t.Parallel()
	diffs, err := normalize.JSONDiff([]byte(`{"a":1}`), []byte(`{"a":"one"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(diffs) != 1 || diffs[0].Kind != normalize.KindTypeMismatch {
		t.Fatalf("want one type mismatch; got %+v", diffs)
	}
	if diffs[0].Path != "$.a" {
		t.Errorf("path: %q", diffs[0].Path)
	}
}

func TestJSONDiff_KeyMissing(t *testing.T) {
	t.Parallel()
	diffs, err := normalize.JSONDiff([]byte(`{"a":1,"b":2}`), []byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff; got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Path != "$.b" || diffs[0].Kind != normalize.KindMissingInB {
		t.Errorf("got %+v", diffs[0])
	}
}

func TestJSONDiff_KeyAdded(t *testing.T) {
	t.Parallel()
	diffs, err := normalize.JSONDiff([]byte(`{"a":1}`), []byte(`{"a":1,"c":3}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff; got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Path != "$.c" || diffs[0].Kind != normalize.KindMissingInA {
		t.Errorf("got %+v", diffs[0])
	}
}

func TestJSONDiff_ArrayLength(t *testing.T) {
	t.Parallel()
	diffs, err := normalize.JSONDiff([]byte(`[1,2,3]`), []byte(`[1,2]`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Expect: 1 length-mismatch at $, 1 missing-in-B at $[2].
	if len(diffs) != 2 {
		t.Fatalf("want 2 diffs; got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Path != "$" || diffs[0].Kind != normalize.KindValueMismatch {
		t.Errorf("length mismatch: %+v", diffs[0])
	}
	if diffs[1].Path != "$[2]" || diffs[1].Kind != normalize.KindMissingInB {
		t.Errorf("missing tail: %+v", diffs[1])
	}
}

func TestJSONDiff_ArrayLengthBigger(t *testing.T) {
	t.Parallel()
	diffs, err := normalize.JSONDiff([]byte(`[1]`), []byte(`[1,2,3]`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(diffs) != 3 {
		t.Fatalf("want 3 diffs; got %d: %+v", len(diffs), diffs)
	}
	if diffs[0].Path != "$" || diffs[0].Kind != normalize.KindValueMismatch {
		t.Errorf("length mismatch: %+v", diffs[0])
	}
	if diffs[1].Path != "$[1]" || diffs[1].Kind != normalize.KindMissingInA {
		t.Errorf("tail[1]: %+v", diffs[1])
	}
	if diffs[2].Path != "$[2]" || diffs[2].Kind != normalize.KindMissingInA {
		t.Errorf("tail[2]: %+v", diffs[2])
	}
}

func TestJSONDiff_Nested(t *testing.T) {
	t.Parallel()
	a := `{"series":[{"id":1,"title":"A"},{"id":2,"title":"B"}]}`
	b := `{"series":[{"id":1,"title":"A"},{"id":2,"title":"C"}]}`
	diffs, err := normalize.JSONDiff([]byte(a), []byte(b))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(diffs) != 1 {
		t.Fatalf("want 1 diff; got %+v", diffs)
	}
	if diffs[0].Path != "$.series[1].title" {
		t.Errorf("nested path: %q", diffs[0].Path)
	}
}

func TestJSONDiff_TypeMismatchObjectVsArray(t *testing.T) {
	t.Parallel()
	diffs, err := normalize.JSONDiff([]byte(`{"x":{"a":1}}`), []byte(`{"x":[1,2]}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(diffs) != 1 || diffs[0].Path != "$.x" || diffs[0].Kind != normalize.KindTypeMismatch {
		t.Errorf("object-vs-array mismatch: %+v", diffs)
	}
}

func TestJSONDiff_ParseErrorSurfaces(t *testing.T) {
	t.Parallel()
	_, err := normalize.JSONDiff([]byte(`{not json`), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "parse A") {
		t.Errorf("want parse A error; got %v", err)
	}
	_, err = normalize.JSONDiff([]byte(`{}`), []byte(`{not json`))
	if err == nil || !strings.Contains(err.Error(), "parse B") {
		t.Errorf("want parse B error; got %v", err)
	}
}

func TestJSONDiff_StableOrdering(t *testing.T) {
	t.Parallel()
	// Many keys differ; assert the same diff list comes back across
	// repeated calls (Go map iteration is randomised, so without
	// the sort in diffWalk this test would be flaky).
	a := `{"z":1,"y":2,"x":3,"w":4,"v":5,"u":6}`
	b := `{"z":2,"y":3,"x":4,"w":5,"v":6,"u":7}`
	prev, _ := normalize.JSONDiff([]byte(a), []byte(b))
	for range 5 {
		got, _ := normalize.JSONDiff([]byte(a), []byte(b))
		if len(got) != len(prev) {
			t.Fatalf("len differs: %d vs %d", len(got), len(prev))
		}
		for i := range got {
			if got[i].Path != prev[i].Path {
				t.Errorf("path[%d] differs: %q vs %q", i, got[i].Path, prev[i].Path)
			}
		}
	}
}

func TestDiffKind_String(t *testing.T) {
	t.Parallel()
	cases := map[normalize.DiffKind]string{
		normalize.KindValueMismatch: "value",
		normalize.KindTypeMismatch:  "type",
		normalize.KindMissingInA:    "missingInA",
		normalize.KindMissingInB:    "missingInB",
	}
	for k, want := range cases {
		if got := k.String(); got != want {
			t.Errorf("%d: got %q want %q", k, got, want)
		}
	}
}
