package v3

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

func TestIndexerSchema_PinnedCount(t *testing.T) {
	t.Parallel()
	if got := len(indexerSchemas()); got != 10 {
		t.Errorf("indexerSchemas count: got %d want 10", got)
	}
}

func TestIndexerSchema_OrderedByImplementationName(t *testing.T) {
	t.Parallel()
	names := []string{}
	for _, s := range indexerSchemas() {
		names = append(names, s.ImplementationName)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("schema order not sorted: %v", names)
	}
}

func TestIndexerSchema_MultiLanguageOptions_OriginalFirstAndExcludesUnknown(t *testing.T) {
	t.Parallel()
	opts := indexerMultiLanguageOptions()
	if len(opts) == 0 {
		t.Fatal("empty options")
	}
	if opts[0].Value != -2 || opts[0].Name != "Original" {
		t.Errorf("first option: got %+v want Original (-2)", opts[0])
	}
	for _, o := range opts {
		if o.Value == 0 {
			t.Errorf("Unknown (id=0) must be excluded: %+v", o)
		}
	}
	// Remaining (after Original) should be sorted alphabetical by Name.
	rest := opts[1:]
	for i := 1; i < len(rest); i++ {
		if rest[i-1].Name > rest[i].Name {
			t.Errorf("not alphabetical at %d: %q > %q", i, rest[i-1].Name, rest[i].Name)
		}
	}
}

func TestIndexerSchema_NewznabNameIsEmpty_OthersMatchImplementation(t *testing.T) {
	t.Parallel()
	for _, s := range indexerSchemas() {
		if s.Implementation == "Newznab" {
			if s.Name != "" {
				t.Errorf("Newznab top-level name: got %q want \"\"", s.Name)
			}
			continue
		}
		if s.Name != s.Implementation {
			t.Errorf("%s: name=%q must equal implementation=%q", s.Implementation, s.Name, s.Implementation)
		}
	}
}

func TestIndexerSchema_NewznabPresetsShape(t *testing.T) {
	t.Parallel()
	var newznab IndexerSchemaResource
	for _, s := range indexerSchemas() {
		if s.Implementation == "Newznab" {
			newznab = s
			break
		}
	}
	if newznab.Implementation == "" {
		t.Fatal("Newznab missing")
	}
	if newznab.Presets == nil {
		t.Fatal("Newznab Presets must be non-nil")
	}
	if got := len(*newznab.Presets); got != 9 {
		t.Errorf("Newznab preset count: got %d want 9", got)
	}
	for _, p := range *newznab.Presets {
		if p.ImplementationName != "" {
			t.Errorf("preset %q must omit implementationName, got %q", p.Name, p.ImplementationName)
		}
		if p.Presets != nil {
			t.Errorf("preset %q must omit presets, got non-nil", p.Name)
		}
		if p.Implementation != "Newznab" {
			t.Errorf("preset %q: implementation must be Newznab, got %q", p.Name, p.Implementation)
		}
		if p.ConfigContract != "NewznabSettings" {
			t.Errorf("preset %q: configContract must be NewznabSettings, got %q", p.Name, p.ConfigContract)
		}
	}
}

func TestIndexerSchema_TopLevelPresetsAlwaysEmits(t *testing.T) {
	t.Parallel()
	// Every top-level row carries presets — empty `[]` for the 9 non-
	// Newznab indexers, populated for Newznab. Wire-shape check via
	// JSON round-trip.
	rr := httptest.NewRecorder()
	IndexerSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/indexer/schema", nil))
	var rows []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, r := range rows {
		p, ok := r["presets"]
		if !ok {
			t.Errorf("%v: top-level row missing presets key", r["implementation"])
			continue
		}
		arr, isArr := p.([]any)
		if !isArr {
			t.Errorf("%v: presets must be array, got %T", r["implementation"], p)
			continue
		}
		want := 0
		if r["implementation"] == "Newznab" {
			want = 9
		}
		if len(arr) != want {
			t.Errorf("%v: presets length got %d want %d", r["implementation"], len(arr), want)
		}
	}
}

func TestIndexerSchema_TorrentIndexersHaveSeedCriteriaQuintet(t *testing.T) {
	t.Parallel()
	// Every torrent indexer carries the 5-field seedCriteria block —
	// catches accidental field omission in any per-impl ctor.
	wantSeed := map[string]bool{
		"seedCriteria.seedRatio":           true,
		"seedCriteria.seedTime":            true,
		"seedCriteria.seasonPackSeedGoal":  true,
		"seedCriteria.seasonPackSeedRatio": true,
		"seedCriteria.seasonPackSeedTime":  true,
	}
	for _, s := range indexerSchemas() {
		if s.Protocol != "torrent" {
			continue
		}
		seen := map[string]bool{}
		for _, f := range s.Fields {
			if wantSeed[f.Name] {
				seen[f.Name] = true
			}
		}
		if len(seen) != len(wantSeed) {
			t.Errorf("%s: torrent indexer missing seedCriteria fields, saw %v", s.Implementation, seen)
		}
	}
}

func TestIndexerSchema_IPTorrentsSupportsSearchFalse(t *testing.T) {
	t.Parallel()
	// IPTorrents and TorrentRssIndexer + Torrentleech have
	// supportsSearch=false (per the C# IndexerSettings flag).
	want := map[string]bool{"IPTorrents": false, "TorrentRssIndexer": false, "Torrentleech": false}
	for _, s := range indexerSchemas() {
		w, ok := want[s.Implementation]
		if !ok {
			continue
		}
		if s.SupportsSearch != w {
			t.Errorf("%s.supportsSearch: got %v want %v", s.Implementation, s.SupportsSearch, w)
		}
	}
}

func TestIndexerSchema_ContentTypeAndStatus(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	IndexerSchemaHandler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/indexer/schema", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
}
