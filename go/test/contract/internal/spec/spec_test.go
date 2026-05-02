package spec

import (
	"path/filepath"
	"testing"
)

func TestLoadCorpusValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.json")
	if err := writeJSON(t, path, Corpus{Entries: []Entry{{
		ID: "series/list",
		Request: Request{
			Method: "GET",
			Path:   "/api/v3/series",
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCorpus(path)
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(c.Entries) != 1 || c.Entries[0].ID != "series/list" {
		t.Fatalf("unexpected entries: %+v", c.Entries)
	}
}

func TestLoadCorpusInvalid(t *testing.T) {
	cases := []struct {
		name   string
		corpus Corpus
		want   string
	}{
		{
			name: "missing id",
			corpus: Corpus{Entries: []Entry{{
				Request: Request{Method: "GET", Path: "/x"},
			}}},
			want: "missing id",
		},
		{
			name: "missing method",
			corpus: Corpus{Entries: []Entry{{
				ID:      "x",
				Request: Request{Path: "/x"},
			}}},
			want: "method/path required",
		},
		{
			name: "invalid method",
			corpus: Corpus{Entries: []Entry{{
				ID:      "x",
				Request: Request{Method: "TRACE", Path: "/x"},
			}}},
			want: "invalid method",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.json")
			if err := writeJSON(t, path, tc.corpus); err != nil {
				t.Fatal(err)
			}
			_, err := LoadCorpus(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestSaveAndLoadGolden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.json")
	in := &Golden{
		EntryID:    "series/list",
		Status:     200,
		BodyIsJSON: true,
		BodyJSON:   []byte(`{"k":"v"}`),
	}
	if err := SaveGolden(path, in); err != nil {
		t.Fatalf("SaveGolden: %v", err)
	}
	out, err := LoadGolden(path)
	if err != nil {
		t.Fatalf("LoadGolden: %v", err)
	}
	if out.EntryID != in.EntryID || out.Status != in.Status || !out.BodyIsJSON {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}

// helpers

func writeJSON(t *testing.T, path string, v any) error {
	t.Helper()
	g, err := marshalIndent(v)
	if err != nil {
		return err
	}
	return writeFile(path, g)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
