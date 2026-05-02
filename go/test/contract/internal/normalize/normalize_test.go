package normalize

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONRedaction(t *testing.T) {
	tests := []struct {
		name  string
		input string
		paths []string
		want  []string // substrings that must appear in the output
		nope  []string // substrings that must NOT appear
	}{
		{
			name:  "top-level scalar",
			input: `{"id":1,"lastInfoSync":"2025-01-01T00:00:00Z","title":"x"}`,
			paths: []string{"$.lastInfoSync"},
			want:  []string{`"id": 1`, `"<redacted>"`, `"title": "x"`},
			nope:  []string{`"2025-01-01T00:00:00Z"`},
		},
		{
			name:  "wildcard over array",
			input: `[{"id":1,"added":"a"},{"id":2,"added":"b"}]`,
			paths: []string{"$[*].added"},
			want:  []string{`"id": 1`, `"id": 2`, `"<redacted>"`},
			nope:  []string{`"a"`, `"b"`},
		},
		{
			name:  "missing field is ignored",
			input: `{"id":1}`,
			paths: []string{"$.lastInfoSync"},
			want:  []string{`"id": 1`},
			nope:  []string{`"<redacted>"`},
		},
		{
			name:  "nested",
			input: `{"images":[{"url":"http://x"},{"url":"http://y"}]}`,
			paths: []string{"$.images[*].url"},
			want:  []string{`"<redacted>"`},
			nope:  []string{`"http://x"`, `"http://y"`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := JSON([]byte(tc.input), tc.paths)
			if err != nil {
				t.Fatalf("JSON: %v", err)
			}
			s := string(out)
			for _, want := range tc.want {
				if !strings.Contains(s, want) {
					t.Errorf("output missing %q\n%s", want, s)
				}
			}
			for _, nope := range tc.nope {
				if strings.Contains(s, nope) {
					t.Errorf("output unexpectedly contains %q\n%s", nope, s)
				}
			}
			// Output must remain valid JSON.
			var sink any
			if err := json.Unmarshal(out, &sink); err != nil {
				t.Errorf("output invalid json: %v\n%s", err, s)
			}
		})
	}
}

func TestJSONInvalidPath(t *testing.T) {
	cases := []string{"foo", "$[abc]", "$.", "$["}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			_, err := JSON([]byte(`{}`), []string{p})
			if err == nil {
				t.Fatalf("expected error for path %q", p)
			}
		})
	}
}

func TestHeadersDrop(t *testing.T) {
	in := map[string][]string{
		"Date":         {"yesterday"},
		"Server":       {"kestrel"},
		"Content-Type": {"application/json"},
	}
	out := Headers(in, []string{"date", "Server"})
	if _, ok := out["Date"]; ok {
		t.Errorf("Date not dropped")
	}
	if _, ok := out["Server"]; ok {
		t.Errorf("Server not dropped")
	}
	if got := out["Content-Type"]; len(got) != 1 || got[0] != "application/json" {
		t.Errorf("Content-Type lost: %v", got)
	}
}
