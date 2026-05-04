package quality_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/quality"
)

func TestDefaultDefinitions_Count(t *testing.T) {
	t.Parallel()
	defs := quality.DefaultDefinitions()
	// 22 entries — Unknown + 21 active qualities (Id 11 retired).
	if len(defs) != 22 {
		t.Errorf("DefaultDefinitions: got %d, want 22", len(defs))
	}
}

func TestDefaultDefinitions_WeightsMatchModelCompare(t *testing.T) {
	t.Parallel()
	// The runtime QualityWeight (used by Model.Compare) and the
	// default-definitions table must agree. If they ever drift,
	// upgrade decisions diverge from the static defaults.
	for _, d := range quality.DefaultDefinitions() {
		got := quality.QualityWeight(d.Quality)
		if got != d.Weight {
			t.Errorf("Quality %s (id=%d): default Weight=%d but QualityWeight=%d",
				d.Quality.Name, d.Quality.ID, d.Weight, got)
		}
	}
}

func TestDefaultDefinitions_TitleEqualsName(t *testing.T) {
	t.Parallel()
	// QualityDefinition.cs:25 sets Title = quality.Name on construction.
	// The default-definitions table mirrors that.
	for _, d := range quality.DefaultDefinitions() {
		if d.Title != d.Quality.Name {
			t.Errorf("Quality %s: Title=%q, want %q (Title must == Quality.Name)",
				d.Quality.Name, d.Title, d.Quality.Name)
		}
	}
}

func TestDefaultDefinitions_GroupNameSharing(t *testing.T) {
	t.Parallel()
	// Six WEB qualities share three GroupNames: "WEB 480p", "WEB 720p",
	// "WEB 1080p", "WEB 2160p". Each pair must share the same Weight
	// (so the upgrade path doesn't keep flipping between visually-
	// equivalent sources).
	groups := map[string][]quality.DefaultDefinition{}
	for _, d := range quality.DefaultDefinitions() {
		if d.GroupName == "" {
			continue
		}
		groups[d.GroupName] = append(groups[d.GroupName], d)
	}
	if len(groups) != 4 {
		t.Errorf("expected 4 group names (WEB 480p/720p/1080p/2160p), got %d: %v", len(groups), keys(groups))
	}
	for name, members := range groups {
		if len(members) != 2 {
			t.Errorf("group %q: %d members, want 2", name, len(members))
			continue
		}
		if members[0].Weight != members[1].Weight {
			t.Errorf("group %q: weights diverge %d vs %d",
				name, members[0].Weight, members[1].Weight)
		}
	}
}

func TestDefaultDefinitionFor(t *testing.T) {
	t.Parallel()
	d, ok := quality.DefaultDefinitionFor(quality.WEBDL1080p)
	if !ok {
		t.Fatal("DefaultDefinitionFor(WEBDL1080p) not found")
	}
	if d.Weight != 12 || d.GroupName != "WEB 1080p" {
		t.Errorf("WEBDL1080p default: %+v, want Weight=12 GroupName=\"WEB 1080p\"", d)
	}

	// A Quality not in the default table should return ok=false.
	bogus := quality.Quality{ID: 999, Name: "Bogus"}
	if _, ok := quality.DefaultDefinitionFor(bogus); ok {
		t.Errorf("DefaultDefinitionFor(bogus) should return ok=false")
	}
}

func keys(m map[string][]quality.DefaultDefinition) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
