package language_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/language"
)

func TestLanguage_FindByID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		id   int
		want language.Language
		ok   bool
	}{
		{0, language.Unknown, true},
		{1, language.English, true},
		{52, language.Georgian, true},
		{-2, language.Original, true},
		{99999, language.Unknown, false},
	}
	for _, c := range cases {
		got, ok := language.FindByID(c.id)
		if got != c.want || ok != c.ok {
			t.Errorf("FindByID(%d) = (%v, %v), want (%v, %v)", c.id, got, ok, c.want, c.ok)
		}
	}
}

func TestLanguage_FindByName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want language.Language
		ok   bool
	}{
		{"English", language.English, true},
		{"english", language.English, true}, // case-insensitive
		{"ENGLISH", language.English, true},
		{"Portuguese (Brazil)", language.PortugueseBrazil, true},
		{"Spanish (Latino)", language.SpanishLatino, true},
		{"Klingon", language.Unknown, false},
		{"", language.Unknown, false},
	}
	for _, c := range cases {
		got, ok := language.FindByName(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("FindByName(%q) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestLanguage_AllCount(t *testing.T) {
	t.Parallel()
	// 52 named + Unknown + Original = 54.
	if got := len(language.All()); got != 54 {
		t.Errorf("All() count = %d, want 54", got)
	}
}

func TestLanguage_IDsAreFrozen(t *testing.T) {
	t.Parallel()
	// Pin a representative subset of (Id, Name) pairs. Renumbering
	// any of these would corrupt user databases that persist Id ints.
	cases := []struct {
		id   int
		want language.Language
	}{
		{0, language.Unknown},
		{1, language.English},
		{18, language.Portuguese},
		{25, language.Czech},
		{33, language.PortugueseBrazil},
		{52, language.Georgian},
		{-2, language.Original},
	}
	for _, c := range cases {
		got, ok := language.FindByID(c.id)
		if !ok {
			t.Errorf("FindByID(%d) missing", c.id)
			continue
		}
		if got.ID != c.want.ID || got.Name != c.want.Name {
			t.Errorf("Id %d = %+v, want %+v", c.id, got, c.want)
		}
	}
}
