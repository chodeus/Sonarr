package organizer_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/organizer"
)

// CleanTitle: ports the C# CleanTitleFixture cases that don't depend
// on CleanFileName's BadCharacters/GoodCharacters mapping (e.g. `*`
// stays `*` here; the `-` substitution lands in CleanFileName). Cases
// kept short — the full integration suite ports later when
// BuildFileName arrives.
func TestCleanTitle(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		// & → and
		{"Law & Order", "Law and Order"},
		// / → space
		{"Anne Hathaway/Florence + The Machine", "Anne Hathaway Florence + The Machine"},
		// brackets removed
		{"Castle (2009)", "Castle 2009"},
		{"[a] title", "a title"},
		// `\s X \s` removes the special char, leaves both spaces.
		// The double-space gets collapsed downstream by CleanFolderName/
		// CleanFileName run-collapse (when those run); pure CleanTitle
		// preserves the spacing verbatim.
		{"Free! - Iwatobi Swim Club", "Free!  Iwatobi Swim Club"},
		// apostrophe + contraction (drop apostrophe, keep s/m/...)
		{"Karma's a B*tch!", "Karmas a B*tch!"},
		{"I'm the Boss", "Im the Boss"},
		{"That'll Be The Day", "Thatll Be The Day"},
		{"I'd Rather Be Alone", "Id Rather Be Alone"},
		{"I've Been Caught", "Ive Been Caught"},
		{"I Can't Die", "I Cant Die"},
		{"Won`t Get Fooled Again", "Wont Get Fooled Again"},
		{"Don’t Blink", "Dont Blink"},
		// apostrophe + non-contraction (followed by space) → drop apostrophe
		{"Tamara Ecclestone: Billion $$ Girl", "Tamara Ecclestone Billion $$ Girl"},
		// backtick between spaces → space-flanked rule fires
		// (double space preserved for pure CleanTitle).
		{"The ` Legend of Kings", "The  Legend of Kings"},
		// apostrophe NOT followed by contraction-or-space → keep
		{"The Title's", "The Title's"},
		// diacritics stripped
		{"Beyoncé X10", "Beyonce X10"},
		// passthrough
		{"Florence + the Machine", "Florence + the Machine"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := organizer.CleanTitle(tc.in)
			if got != tc.want {
				t.Errorf("CleanTitle(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTitleThe(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"The Office", "Office, The"},
		{"An American Tail", "American Tail, An"},
		{"A New Hope", "New Hope, A"},
		{"The Office (2005)", "Office, The (2005)"},
		{"Office", "Office"},                           // no leading article → unchanged
		{"There Is No Article", "There Is No Article"}, // "There" doesn't match (must be exact word)
		{"the office", "office, the"},                  // case-insensitive on the prefix
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := organizer.TitleThe(tc.in)
			if got != tc.want {
				t.Errorf("TitleThe(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestTitleYear(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title string
		year  int
		want  string
	}{
		{"Office", 2005, "Office (2005)"},
		{"Office", 0, "Office"},                  // zero year skipped
		{"Castle (2009)", 2009, "Castle (2009)"}, // already has year → unchanged
		{"Castle (2009)", 2010, "Castle (2009)"}, // existing year wins
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.title, func(t *testing.T) {
			t.Parallel()
			got := organizer.TitleYear(tc.title, tc.year)
			if got != tc.want {
				t.Errorf("TitleYear(%q,%d) = %q, want %q", tc.title, tc.year, got, tc.want)
			}
		})
	}
}

func TestTitleWithoutYear(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"Castle (2009)", "Castle "},
		{"Office", "Office"},
		{"Show (1999) (Other)", "Show (1999) (Other)"}, // YearRegex anchored at end; only the trailing year is stripped
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := organizer.TitleWithoutYear(tc.in)
			if got != tc.want {
				t.Errorf("TitleWithoutYear(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanTitleThe(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"The Office", "Office, The"},
		{"An American Tail", "American Tail, An"},
		{"The Office (2005)", "Office, The 2005"}, // CleanTitle strips brackets in the suffix
		{"Office", "Office"},                      // no prefix → CleanTitle direct
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := organizer.CleanTitleThe(tc.in)
			if got != tc.want {
				t.Errorf("CleanTitleThe(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanTitleTheYear(t *testing.T) {
	t.Parallel()
	cases := []struct {
		title string
		year  int
		want  string
	}{
		{"The Office", 2005, "Office, The 2005"},
		{"The Office (2005)", 2005, "Office, The 2005"}, // existing trailing year wins
		{"The Office (2005)", 2010, "Office, The 2005"}, // trailing year still wins
		{"Office", 0, "Office"},                         // no year, no prefix
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.title, func(t *testing.T) {
			t.Parallel()
			got := organizer.CleanTitleTheYear(tc.title, tc.year)
			if got != tc.want {
				t.Errorf("CleanTitleTheYear(%q,%d) = %q, want %q", tc.title, tc.year, got, tc.want)
			}
		})
	}
}

func TestTitleFirstCharacter(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"Office", "O"},
		{"office", "O"}, // uppercased
		{"123 Test", "1"},
		{"!Bang", "B"},   // first non-alphanumeric → fall through to second
		{"!!", "_"},      // both non-alphanumeric → underscore
		{"é Title", "E"}, // diacritic stripped
		{"", "_"},        // empty input
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := organizer.TitleFirstCharacter(tc.in)
			if got != tc.want {
				t.Errorf("TitleFirstCharacter(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanTitleYear_NoArticleMove(t *testing.T) {
	t.Parallel()
	got := organizer.CleanTitleYear("The Office", 2005)
	if got != "The Office 2005" {
		t.Errorf("CleanTitleYear: got %q, want %q", got, "The Office 2005")
	}
}

func TestCleanTitleWithoutYear(t *testing.T) {
	t.Parallel()
	got := organizer.CleanTitleWithoutYear("The Office (2005)")
	if got != "The Office" {
		t.Errorf("got %q, want %q", got, "The Office")
	}
}

func TestCleanTitleTheWithoutYear(t *testing.T) {
	t.Parallel()
	got := organizer.CleanTitleTheWithoutYear("The Office (2005)")
	if got != "Office, The" {
		t.Errorf("got %q, want %q", got, "Office, The")
	}
}

func TestTitleTheWithoutYear(t *testing.T) {
	t.Parallel()
	got := organizer.TitleTheWithoutYear("The Office (2005)")
	if got != "Office, The" {
		t.Errorf("got %q, want %q", got, "Office, The")
	}
}

func TestTitleTheYear(t *testing.T) {
	t.Parallel()
	got := organizer.TitleTheYear("The Office", 2005)
	if got != "Office, The (2005)" {
		t.Errorf("got %q, want %q", got, "Office, The (2005)")
	}
}
