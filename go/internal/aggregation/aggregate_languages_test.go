package aggregation_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"sonarr.tv/sonarr/internal/aggregation"
	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/episodes"
	"sonarr.tv/sonarr/internal/language"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

// stubMultiLanguageLookup is a per-id / per-name pinned response
// implementation of aggregation.IndexerMultiLanguagesLookup.
type stubMultiLanguageLookup struct {
	byID        map[int][]language.Language
	byName      map[string][]language.Language
	idCalls     []int
	nameCalls   []string
	idMissCalls []int
	nameErr     error
	idErr       error
}

func (s *stubMultiLanguageLookup) FindByID(_ context.Context, indexerID int) ([]language.Language, bool, error) {
	s.idCalls = append(s.idCalls, indexerID)
	if s.idErr != nil {
		return nil, false, s.idErr
	}
	if v, ok := s.byID[indexerID]; ok {
		return v, true, nil
	}
	s.idMissCalls = append(s.idMissCalls, indexerID)
	return nil, false, nil
}

func (s *stubMultiLanguageLookup) FindByName(_ context.Context, indexerName string) ([]language.Language, bool, error) {
	s.nameCalls = append(s.nameCalls, indexerName)
	if s.nameErr != nil {
		return nil, false, s.nameErr
	}
	if v, ok := s.byName[indexerName]; ok {
		return v, true, nil
	}
	return nil, false, nil
}

// mkSubject returns a *Subject with the canonical AggregateLanguagesFixture.Setup
// shape: one episode, English-OriginalLanguage series, empty ReleaseInfo.
func mkSubject(parsedLanguages []language.Language, releaseTitle, releaseTokens string) *decisionengine.Subject {
	sub := decisionengine.NewSubject()
	sub.ParsedEpisodeInfo = &parser.ParsedEpisodeInfo{
		Languages:     parsedLanguages,
		ReleaseTitle:  releaseTitle,
		ReleaseTokens: releaseTokens,
	}
	sub.Episodes = []episodes.Episode{{ID: 1, Title: "Generic"}}
	sub.Series = &series.Series{ID: 7, OriginalLanguage: language.English.ID}
	sub.Release = &parser.ReleaseInfo{}
	return sub
}

func mkAggregator(lookup aggregation.IndexerMultiLanguagesLookup) *aggregation.AggregateLanguages {
	return aggregation.NewAggregateLanguages(lookup, slog.Default())
}

const simpleReleaseTitle = "Series.Title.S01E01.xyz-RlsGroup"

// AggregateLanguagesFixture.cs:52-58 — should_return_existing_language_if_episode_title_does_not_have_language.
// Language.Original parsed → falls through to OriginalLanguage substitution.
func TestAggregateLanguagesOriginalSubstitutesSeriesOriginalLanguage(t *testing.T) {
	t.Parallel()
	sub := mkSubject([]language.Language{language.Original}, simpleReleaseTitle, "")
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if !containsLang(sub.Languages, language.English) {
		t.Errorf("Languages = %v, want to contain English (series.OriginalLanguage)", sub.Languages)
	}
	if containsLang(sub.Languages, language.Original) {
		t.Errorf("Languages = %v, should not contain Original after substitution", sub.Languages)
	}
}

// AggregateLanguagesFixture.cs:60-66 — should_return_parsed_language.
// French parsed, no episode-title interference → French preserved.
func TestAggregateLanguagesParsedLanguagePreserved(t *testing.T) {
	t.Parallel()
	sub := mkSubject([]language.Language{language.French}, simpleReleaseTitle, "")
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.French {
		t.Errorf("Languages = %v, want [French]", sub.Languages)
	}
}

// AggregateLanguagesFixture.cs:255-262 — should_return_original_when_no_indexer_value.
// No indexer found, no languages parsed → series.OriginalLanguage fallback.
func TestAggregateLanguagesNoIndexerFallsBackToOriginalLanguage(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.MULTi.1080p.WEB.H265-RlsGroup"
	sub := mkSubject([]language.Language{}, releaseTitle, "")
	sub.Release.(*parser.ReleaseInfo).Title = releaseTitle
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.English {
		t.Errorf("Languages = %v, want [English] (series.OriginalLanguage)", sub.Languages)
	}
}

// AggregateLanguagesFixture.cs:72-90 — should_return_multi_languages_when_indexer_id_has_multi_languages_configuration.
// Indexer Id=1 → MultiLanguages = [Original, French]. Release has MULTi marker.
// Expected: [series.OriginalLanguage, French] (Original substituted).
func TestAggregateLanguagesIndexerByIDAppliesMultiLanguages(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.MULTi.1080p.WEB.H265-RlsGroup"
	sub := mkSubject([]language.Language{}, releaseTitle, "")
	sub.Release.(*parser.ReleaseInfo).IndexerID = 1
	sub.Release.(*parser.ReleaseInfo).Title = releaseTitle

	lookup := &stubMultiLanguageLookup{
		byID: map[int][]language.Language{
			1: {language.Original, language.French},
		},
	}
	agg := mkAggregator(lookup)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	want := []language.Language{language.English, language.French}
	if !sameLanguageSet(sub.Languages, want) {
		t.Errorf("Languages = %v, want %v", sub.Languages, want)
	}
	if len(lookup.idCalls) != 1 || lookup.idCalls[0] != 1 {
		t.Errorf("FindByID calls = %v, want [1]", lookup.idCalls)
	}
	if len(lookup.nameCalls) != 0 {
		t.Errorf("FindByName called %v; expected no name lookup when ID hit", lookup.nameCalls)
	}
}

// AggregateLanguagesFixture.cs:96-127 — should_return_multi_languages_from_indexer_with_id_when_indexer_id_and_name_are_set.
// IndexerId hit takes priority over Indexer-name lookup.
func TestAggregateLanguagesIDLookupTakesPriorityOverName(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.MULTi.1080p.WEB.H265-RlsGroup"
	sub := mkSubject([]language.Language{}, releaseTitle, "")
	sub.Release.(*parser.ReleaseInfo).IndexerID = 1
	sub.Release.(*parser.ReleaseInfo).Indexer = "MyIndexer2"
	sub.Release.(*parser.ReleaseInfo).Title = releaseTitle

	lookup := &stubMultiLanguageLookup{
		byID: map[int][]language.Language{
			1: {language.Original, language.French},
		},
		byName: map[string][]language.Language{
			"MyIndexer2": {language.Original, language.German},
		},
	}
	agg := mkAggregator(lookup)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := []language.Language{language.English, language.French}
	if !sameLanguageSet(sub.Languages, want) {
		t.Errorf("Languages = %v, want %v", sub.Languages, want)
	}
	if len(lookup.nameCalls) != 0 {
		t.Errorf("FindByName called %v; ID match should suppress name lookup", lookup.nameCalls)
	}
}

// AggregateLanguagesFixture.cs:133-153 — should_return_multi_languages_when_indexer_name_has_multi_languages_configuration.
// No IndexerId, name-only lookup hits.
func TestAggregateLanguagesNameLookupWhenIDAbsent(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.MULTi.1080p.WEB.H265-RlsGroup"
	sub := mkSubject([]language.Language{}, releaseTitle, "")
	sub.Release.(*parser.ReleaseInfo).Indexer = "MyIndexer (Prowlarr)"
	sub.Release.(*parser.ReleaseInfo).Title = releaseTitle

	lookup := &stubMultiLanguageLookup{
		byName: map[string][]language.Language{
			"MyIndexer (Prowlarr)": {language.Original, language.French},
		},
	}
	agg := mkAggregator(lookup)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := []language.Language{language.English, language.French}
	if !sameLanguageSet(sub.Languages, want) {
		t.Errorf("Languages = %v, want %v", sub.Languages, want)
	}
	if len(lookup.nameCalls) != 1 || lookup.nameCalls[0] != "MyIndexer (Prowlarr)" {
		t.Errorf("FindByName calls = %v, want [MyIndexer (Prowlarr)]", lookup.nameCalls)
	}
}

// AggregateLanguagesFixture.cs:313-350 — should_return_multi_languages_from_indexer_with_name_when_indexer_id_does_not_exist.
// IndexerId=10 misses → name fallback fires.
func TestAggregateLanguagesIDMissFallsBackToName(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.MULTi.1080p.WEB.H265-RlsGroup"
	sub := mkSubject([]language.Language{}, releaseTitle, "")
	sub.Release.(*parser.ReleaseInfo).IndexerID = 10
	sub.Release.(*parser.ReleaseInfo).Indexer = "MyIndexer1"
	sub.Release.(*parser.ReleaseInfo).Title = releaseTitle

	lookup := &stubMultiLanguageLookup{
		byName: map[string][]language.Language{
			"MyIndexer1": {language.Original, language.French},
		},
	}
	agg := mkAggregator(lookup)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := []language.Language{language.English, language.French}
	if !sameLanguageSet(sub.Languages, want) {
		t.Errorf("Languages = %v, want %v", sub.Languages, want)
	}
	if len(lookup.idMissCalls) != 1 || lookup.idMissCalls[0] != 10 {
		t.Errorf("ID-miss tracking = %v, want [10]", lookup.idMissCalls)
	}
	if len(lookup.nameCalls) != 1 || lookup.nameCalls[0] != "MyIndexer1" {
		t.Errorf("FindByName calls = %v, want [MyIndexer1]", lookup.nameCalls)
	}
}

// AggregateLanguagesFixture.cs:159-177 — should_return_multi_languages_when_release_as_unknown_as_default_language_and_indexer_has_multi_languages_configuration.
// Parsed=[Unknown] is replaced by MultiLanguages, not unioned.
func TestAggregateLanguagesUnknownReplacedByMultiLanguages(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.MULTi.1080p.WEB.H265-RlsGroup"
	sub := mkSubject([]language.Language{language.Unknown}, releaseTitle, "")
	sub.Release.(*parser.ReleaseInfo).IndexerID = 1
	sub.Release.(*parser.ReleaseInfo).Title = releaseTitle

	lookup := &stubMultiLanguageLookup{
		byID: map[int][]language.Language{
			1: {language.Original, language.French},
		},
	}
	agg := mkAggregator(lookup)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := []language.Language{language.English, language.French}
	if !sameLanguageSet(sub.Languages, want) {
		t.Errorf("Languages = %v, want %v", sub.Languages, want)
	}
}

// AggregateLanguagesFixture.cs:207-225 — should_return_multi_languages_when_release_as_other_language_and_indexer_has_multi_languages_configuration.
// Parsed=[German] is UNIONed with MultiLanguages=[Original, French] →
// expected {OriginalLanguage, French, German} (Original substituted).
func TestAggregateLanguagesUnionsParsedWithMultiLanguages(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.MULTi.1080p.WEB.H265-RlsGroup"
	sub := mkSubject([]language.Language{language.German}, releaseTitle, "")
	sub.Release.(*parser.ReleaseInfo).IndexerID = 1
	sub.Release.(*parser.ReleaseInfo).Title = releaseTitle

	lookup := &stubMultiLanguageLookup{
		byID: map[int][]language.Language{
			1: {language.Original, language.French},
		},
	}
	agg := mkAggregator(lookup)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := []language.Language{language.English, language.French, language.German}
	if !sameLanguageSet(sub.Languages, want) {
		t.Errorf("Languages = %v, want %v", sub.Languages, want)
	}
}

// AggregateLanguagesFixture.cs:231-249 — should_return_original_when_indexer_has_no_multi_languages_configuration.
// Indexer found but MultiLanguages empty → OriginalLanguage fallback fires.
func TestAggregateLanguagesIndexerNoMultiLanguagesFallsBackToOriginal(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.MULTi.1080p.WEB.H265-RlsGroup"
	sub := mkSubject([]language.Language{}, releaseTitle, "")
	sub.Release.(*parser.ReleaseInfo).IndexerID = 1
	sub.Release.(*parser.ReleaseInfo).Title = releaseTitle

	lookup := &stubMultiLanguageLookup{
		byID: map[int][]language.Language{
			1: {}, // indexer exists, empty MultiLanguages
		},
	}
	agg := mkAggregator(lookup)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := []language.Language{language.English}
	if !sameLanguageSet(sub.Languages, want) {
		t.Errorf("Languages = %v, want %v", sub.Languages, want)
	}
}

// AggregateLanguagesFixture.cs:264-273 — should_exclude_language_that_is_part_of_episode_title_when_release_tokens_contains_episode_title.
// Episode title "Jimmy The Greek" — Greek language detected from the
// episode title appears in release tokens → exclude Greek from the
// languages list, then OriginalLanguage fallback fires.
func TestAggregateLanguagesExcludesEpisodeTitleLanguages(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.Jimmy.The.Greek.xyz-RlsGroup"
	releaseTokens := ".Jimmy.The.Greek.xyz-RlsGroup"
	sub := mkSubject([]language.Language{language.Greek}, releaseTitle, releaseTokens)
	sub.Episodes[0].Title = "Jimmy The Greek"
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.English {
		t.Errorf("Languages = %v, want [English] (Greek excluded)", sub.Languages)
	}
}

// AggregateLanguagesFixture.cs:276-286 — should_remove_parsed_language_that_is_part_of_episode_title_when_release_tokens_contains_episode_title.
// Greek removed (episode-title), French preserved.
func TestAggregateLanguagesPartialEpisodeTitleExclusion(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.Jimmy.The.Greek.French.xyz-RlsGroup"
	releaseTokens := ".Jimmy.The.Greek.French.xyz-RlsGroup"
	sub := mkSubject([]language.Language{language.Greek, language.French}, releaseTitle, releaseTokens)
	sub.Episodes[0].Title = "Jimmy The Greek"
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.French {
		t.Errorf("Languages = %v, want [French]", sub.Languages)
	}
}

// AggregateLanguagesFixture.cs:288-298 — should_not_exclude_language_that_is_part_of_episode_title_when_release_tokens_does_not_contain_episode_title.
// Episode title not in release tokens → no exclusion, Greek preserved.
func TestAggregateLanguagesEpisodeTitleNotInTokensPreservesLanguage(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.xyz-RlsGroup"
	releaseTokens := ".xyz-RlsGroup"
	sub := mkSubject([]language.Language{language.Greek}, releaseTitle, releaseTokens)
	sub.Episodes[0].Title = "Jimmy The Greek"
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.Greek {
		t.Errorf("Languages = %v, want [Greek] (preserved)", sub.Languages)
	}
}

// AggregateLanguagesFixture.cs:300-310 — should_use_reparse_language_after_determining_languages_that_are_in_episode_titles.
// Greek appears twice in tokens — episode title strips one occurrence,
// but the trimmed tokens still detect Greek → Greek preserved overall.
func TestAggregateLanguagesReparsesAfterEpisodeTitleStrip(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.Jimmy.The.Greek.Greek.xyz-RlsGroup"
	releaseTokens := ".Jimmy.The.Greek.Greek.xyz-RlsGroup"
	sub := mkSubject([]language.Language{language.Greek}, releaseTitle, releaseTokens)
	sub.Episodes[0].Title = "Jimmy The Greek"
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.Greek {
		t.Errorf("Languages = %v, want [Greek]", sub.Languages)
	}
}

// Series-nil short-circuit: when parsingservice couldn't resolve a
// Series, fall back to parsed.Languages (since releaseInfo.Languages
// is empty in this setup).
func TestAggregateLanguagesNilSeriesFallsBackToParsed(t *testing.T) {
	t.Parallel()
	sub := mkSubject([]language.Language{language.French}, simpleReleaseTitle, "")
	sub.Series = nil
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.French {
		t.Errorf("Languages = %v, want [French]", sub.Languages)
	}
}

// Series-nil AND release-info-supplied languages → release info wins.
func TestAggregateLanguagesNilSeriesUsesReleaseLanguages(t *testing.T) {
	t.Parallel()
	sub := mkSubject([]language.Language{language.French}, simpleReleaseTitle, "")
	sub.Series = nil
	sub.Release.(*parser.ReleaseInfo).Languages = []language.Language{language.German}
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.German {
		t.Errorf("Languages = %v, want [German] (release-supplied)", sub.Languages)
	}
}

// Nil ParsedEpisodeInfo → no-op (defensive guard).
func TestAggregateLanguagesNilParsedInfoIsNoop(t *testing.T) {
	t.Parallel()
	sub := decisionengine.NewSubject() // ParsedEpisodeInfo nil
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 0 {
		t.Errorf("Languages = %v, want empty (no-op)", sub.Languages)
	}
}

// Nil Subject → ErrNilSubject.
func TestAggregateLanguagesNilSubjectErrors(t *testing.T) {
	t.Parallel()
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), nil); !errors.Is(err, aggregation.ErrNilSubject) {
		t.Errorf("err = %v, want ErrNilSubject", err)
	}
}

// Release-supplied non-empty languages take priority — skip the
// episode-title scrubbing entirely.
func TestAggregateLanguagesReleaseLanguagesWin(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.Jimmy.The.Greek.xyz-RlsGroup"
	sub := mkSubject([]language.Language{language.Greek}, releaseTitle, "")
	sub.Episodes[0].Title = "Jimmy The Greek"
	sub.Release.(*parser.ReleaseInfo).Languages = []language.Language{language.Portuguese}
	agg := mkAggregator(nil)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(sub.Languages) != 1 || sub.Languages[0] != language.Portuguese {
		t.Errorf("Languages = %v, want [Portuguese] (release-supplied wins)", sub.Languages)
	}
}

// TorrentInfo Subject.Release shape is recognised — language path
// applies identically.
func TestAggregateLanguagesTorrentInfoReleaseRecognised(t *testing.T) {
	t.Parallel()
	releaseTitle := "Series.Title.S01E01.MULTi.1080p.WEB.H265-RlsGroup"
	sub := mkSubject([]language.Language{}, releaseTitle, "")
	torrent := &parser.TorrentInfo{
		ReleaseInfo: parser.ReleaseInfo{
			IndexerID: 1,
			Title:     releaseTitle,
		},
	}
	sub.Release = torrent

	lookup := &stubMultiLanguageLookup{
		byID: map[int][]language.Language{
			1: {language.Original, language.French},
		},
	}
	agg := mkAggregator(lookup)
	if err := agg.Aggregate(context.Background(), sub); err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	want := []language.Language{language.English, language.French}
	if !sameLanguageSet(sub.Languages, want) {
		t.Errorf("Languages = %v, want %v", sub.Languages, want)
	}
}

// containsLang / sameLanguageSet are tiny helpers used across the
// table-driven tests above.
func containsLang(xs []language.Language, l language.Language) bool {
	for _, x := range xs {
		if x == l {
			return true
		}
	}
	return false
}

func sameLanguageSet(a, b []language.Language) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[language.Language]int)
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
		if seen[x] < 0 {
			return false
		}
	}
	return true
}
