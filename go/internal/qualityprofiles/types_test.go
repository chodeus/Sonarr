package qualityprofiles_test

import (
	"encoding/json"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/quality"
	"sonarr.tv/sonarr/internal/qualityprofiles"
)

// Wire-shape parity: a Quality field encodes as just the int Id, per
// the C# QualityIntConverter. Any embedded `id` field is omitted when
// zero, per [JsonIgnore(WhenWritingDefault)].
func TestQualityItem_MarshalUsesIntQuality(t *testing.T) {
	t.Parallel()
	item := qualityprofiles.QualityItem{
		Quality: quality.SDTV,
		Allowed: true,
	}
	out, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `"quality":1`) {
		t.Errorf("Quality should encode as int 1, got %s", got)
	}
	if strings.Contains(got, `"id"`) {
		t.Errorf("Id should be omitted when zero, got %s", got)
	}
	if !strings.Contains(got, `"items":[]`) {
		t.Errorf("Items should always emit []; got %s", got)
	}
}

// Group entries persist with a non-zero Id + Name + nested Items, but
// the Quality field is zero (the C# null). On decode that resolves to
// quality.Unknown — the IsGroup signal.
func TestQualityItem_GroupRoundTrip(t *testing.T) {
	t.Parallel()
	src := qualityprofiles.QualityItem{
		ID:      1000,
		Name:    "WEB 720p",
		Allowed: true,
		Items: []qualityprofiles.QualityItem{
			{Quality: quality.WEBRip720p, Allowed: true},
			{Quality: quality.WEBDL720p, Allowed: true},
		},
	}
	out, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Round-trip back through Unmarshal.
	var got qualityprofiles.QualityItem
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.ID != 1000 || got.Name != "WEB 720p" {
		t.Errorf("group fields lost: %+v", got)
	}
	if got.Quality.ID != 0 {
		t.Errorf("group Quality should round-trip to Unknown, got %+v", got.Quality)
	}
	if !got.IsGroup() {
		t.Errorf("expected IsGroup=true, got false")
	}
	if len(got.Items) != 2 {
		t.Fatalf("expected 2 nested items, got %d", len(got.Items))
	}
	if got.Items[0].Quality.ID != quality.WEBRip720p.ID {
		t.Errorf("nested[0] Quality lost: %+v", got.Items[0])
	}
}

// Quality entries persist with the typed Quality and no nested Items.
func TestQualityItem_QualityRoundTrip(t *testing.T) {
	t.Parallel()
	src := qualityprofiles.QualityItem{
		Quality: quality.HDTV1080p,
		Allowed: true,
	}
	out, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got qualityprofiles.QualityItem
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Quality.ID != quality.HDTV1080p.ID {
		t.Errorf("Quality round-trip lost: got %+v want %+v", got.Quality, quality.HDTV1080p)
	}
	if got.IsGroup() {
		t.Errorf("expected IsGroup=false, got true (quality entry)")
	}
}

// MinSize/MaxSize/PreferredSize round-trip as nullable doubles.
func TestQualityItem_SizeFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	min := 0.0
	max := 199.9
	preferred := 100.5
	src := qualityprofiles.QualityItem{
		Quality:       quality.SDTV,
		Allowed:       true,
		MinSize:       &min,
		MaxSize:       &max,
		PreferredSize: &preferred,
	}
	out, _ := json.Marshal(src)
	var got qualityprofiles.QualityItem
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.MinSize == nil || *got.MinSize != 0.0 {
		t.Errorf("MinSize round-trip: %v", got.MinSize)
	}
	if got.MaxSize == nil || *got.MaxSize != 199.9 {
		t.Errorf("MaxSize round-trip: %v", got.MaxSize)
	}
	if got.PreferredSize == nil || *got.PreferredSize != 100.5 {
		t.Errorf("PreferredSize round-trip: %v", got.PreferredSize)
	}
}

// FormatItem encodes Format as just the int id, per
// CustomFormatIntConverter.
func TestFormatItem_EncodesFormatAsInt(t *testing.T) {
	t.Parallel()
	src := qualityprofiles.FormatItem{FormatID: 42, Score: 100}
	out, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	if got != `{"format":42,"score":100}` {
		t.Errorf("got %s want compact {format,score}", got)
	}

	var back qualityprofiles.FormatItem
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != src {
		t.Errorf("round-trip mismatch: %+v vs %+v", back, src)
	}
}

func TestParseItems_EmptyAndNullSurfaces(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "null", "[]"} {
		got, err := qualityprofiles.ParseItems(in)
		if err != nil {
			t.Fatalf("ParseItems(%q): %v", in, err)
		}
		if got == nil {
			t.Errorf("ParseItems(%q) returned nil; want non-nil empty", in)
		}
		if len(got) != 0 {
			t.Errorf("ParseItems(%q) len=%d; want 0", in, len(got))
		}
	}
}

func TestParsedItems_RoundTripsViaProfile(t *testing.T) {
	t.Parallel()
	items := []qualityprofiles.QualityItem{
		{Quality: quality.SDTV, Allowed: true},
		{
			ID:      1000,
			Name:    "WEB 480p",
			Allowed: true,
			Items: []qualityprofiles.QualityItem{
				{Quality: quality.WEBRip480p, Allowed: true},
				{Quality: quality.WEBDL480p, Allowed: true},
			},
		},
	}
	p := &qualityprofiles.Profile{}
	if err := p.SetItems(items); err != nil {
		t.Fatalf("SetItems: %v", err)
	}
	got, err := p.ParsedItems()
	if err != nil {
		t.Fatalf("ParsedItems: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
	if got[0].Quality.ID != quality.SDTV.ID {
		t.Errorf("item 0 Quality lost: %+v", got[0])
	}
	if !got[1].IsGroup() || got[1].ID != 1000 || len(got[1].Items) != 2 {
		t.Errorf("item 1 group lost: %+v", got[1])
	}
}

func TestParsedFormatItems_RoundTripsViaProfile(t *testing.T) {
	t.Parallel()
	items := []qualityprofiles.FormatItem{
		{FormatID: 1, Score: 100},
		{FormatID: 2, Score: -50},
	}
	p := &qualityprofiles.Profile{}
	if err := p.SetFormatItems(items); err != nil {
		t.Fatalf("SetFormatItems: %v", err)
	}
	got, err := p.ParsedFormatItems()
	if err != nil {
		t.Fatalf("ParsedFormatItems: %v", err)
	}
	if len(got) != 2 || got[0] != items[0] || got[1] != items[1] {
		t.Errorf("FormatItems round-trip mismatch: %+v vs %+v", got, items)
	}
}

func TestLookupIndex_DirectQuality(t *testing.T) {
	t.Parallel()
	items := []qualityprofiles.QualityItem{
		{Quality: quality.SDTV, Allowed: true},
		{Quality: quality.HDTV720p, Allowed: true},
	}
	idx, ok := qualityprofiles.LookupIndex(items, quality.HDTV720p, false)
	if !ok || idx.Index != 1 || idx.GroupIndex != 0 {
		t.Errorf("LookupIndex(HDTV720p): %+v ok=%v want {1,0} true", idx, ok)
	}
}

func TestLookupIndex_GroupedQuality(t *testing.T) {
	t.Parallel()
	items := []qualityprofiles.QualityItem{
		{Quality: quality.SDTV, Allowed: true},
		{
			ID: 1000, Name: "WEB 720p",
			Items: []qualityprofiles.QualityItem{
				{Quality: quality.WEBRip720p, Allowed: true},
				{Quality: quality.WEBDL720p, Allowed: true},
			},
		},
	}
	// respectGroupOrder=false collapses to top-level position only.
	idx, ok := qualityprofiles.LookupIndex(items, quality.WEBDL720p, false)
	if !ok || idx.Index != 1 || idx.GroupIndex != 0 {
		t.Errorf("respectGroupOrder=false: %+v want {1,0}", idx)
	}
	// respectGroupOrder=true reports the position within the group.
	idx2, ok2 := qualityprofiles.LookupIndex(items, quality.WEBDL720p, true)
	if !ok2 || idx2.Index != 1 || idx2.GroupIndex != 1 {
		t.Errorf("respectGroupOrder=true: %+v want {1,1}", idx2)
	}
}

func TestLookupIndex_MissingQuality(t *testing.T) {
	t.Parallel()
	items := []qualityprofiles.QualityItem{
		{Quality: quality.SDTV, Allowed: true},
	}
	_, ok := qualityprofiles.LookupIndex(items, quality.HDTV2160p, false)
	if ok {
		t.Errorf("missing quality should return found=false")
	}
}

func TestLookupIndexByID_GroupIDMatch(t *testing.T) {
	t.Parallel()
	items := []qualityprofiles.QualityItem{
		{Quality: quality.SDTV, Allowed: true},
		{
			ID: 1234, Name: "Custom Group",
			Items: []qualityprofiles.QualityItem{
				{Quality: quality.WEBRip720p, Allowed: true},
			},
		},
	}
	idx, ok := qualityprofiles.LookupIndexByID(items, 1234, false)
	if !ok || idx.Index != 1 {
		t.Errorf("group-id lookup: %+v ok=%v want {1,0} true", idx, ok)
	}
}

func TestCalculateCustomFormatScore(t *testing.T) {
	t.Parallel()
	items := []qualityprofiles.FormatItem{
		{FormatID: 1, Score: 100},
		{FormatID: 2, Score: -50},
		{FormatID: 3, Score: 25},
	}
	cases := []struct {
		name    string
		present map[int]bool
		want    int
	}{
		{"none present", map[int]bool{}, 0},
		{"one present", map[int]bool{1: true}, 100},
		{"two present", map[int]bool{1: true, 3: true}, 125},
		{"all present", map[int]bool{1: true, 2: true, 3: true}, 75},
		{"unknown id ignored", map[int]bool{99: true}, 0},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := qualityprofiles.CalculateCustomFormatScore(items, tc.present); got != tc.want {
				t.Errorf("got %d want %d", got, tc.want)
			}
		})
	}
}

func TestQualityIndex_CompareTo(t *testing.T) {
	t.Parallel()
	a := qualityprofiles.QualityIndex{Index: 1, GroupIndex: 0}
	b := qualityprofiles.QualityIndex{Index: 1, GroupIndex: 1}
	c := qualityprofiles.QualityIndex{Index: 2, GroupIndex: 0}

	if a.CompareTo(b, false) != 0 {
		t.Errorf("CompareTo(b, false) should ignore GroupIndex")
	}
	if a.CompareTo(b, true) != -1 {
		t.Errorf("CompareTo(b, true) should respect GroupIndex")
	}
	if a.CompareTo(c, false) != -1 {
		t.Errorf("CompareTo(c, false) by Index")
	}
	if c.CompareTo(a, true) != 1 {
		t.Errorf("CompareTo(a, true) reverse")
	}
}

func TestQualityItem_GetQualities(t *testing.T) {
	t.Parallel()
	single := qualityprofiles.QualityItem{Quality: quality.SDTV}
	if got := single.GetQualities(); len(got) != 1 || got[0].ID != quality.SDTV.ID {
		t.Errorf("quality entry: %+v", got)
	}

	group := qualityprofiles.QualityItem{
		ID: 1000,
		Items: []qualityprofiles.QualityItem{
			{Quality: quality.WEBRip720p},
			{Quality: quality.WEBDL720p},
		},
	}
	if got := group.GetQualities(); len(got) != 2 {
		t.Errorf("group entry: len=%d want 2", len(got))
	}
}
