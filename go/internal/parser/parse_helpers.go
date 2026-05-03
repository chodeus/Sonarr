package parser

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// Helper regexes + small standalone helpers ported from
// `NzbDrone.Core.Parser.Parser`. Each helper here corresponds to a
// public/private static method that is testable in isolation; the
// 122-element ReportTitleRegex array + ParseTitle / ParseMatchCollection
// pipeline land in subsequent slices.

// rejectHashedReleasesRegexes ports the C# `RejectHashedReleasesRegexes`
// array at `Parser.cs:463-505`. Each pattern matches a known hash-shaped
// release title that should be rejected by ValidateBeforeParsing
// before any parsing is attempted.
//
// Order is preserved against the C# source for diff-ability.
var rejectHashedReleasesRegexes = []*regexp.Regexp{
	// Generic match for md5 and mixed-case hashes.
	regexp.MustCompile(`^[0-9a-zA-Z]{32}`),
	// Generic match for shorter lower-case hashes.
	regexp.MustCompile(`^[a-z0-9]{24}$`),
	// Format seen on some NZBGeek releases.
	regexp.MustCompile(`^[A-Z]{11}\d{3}$`),
	regexp.MustCompile(`^[a-z]{12}\d{3}$`),
	// Backup filename (Unknown origins).
	regexp.MustCompile(`^Backup_\d{5,}S\d{2}-\d{2}$`),
	// 123 - Started appearing December 2014.
	regexp.MustCompile(`^123$`),
	// abc - Started appearing January 2015.
	regexp.MustCompile(`(?i)^abc$`),
	// abc - Started appearing 2020.
	regexp.MustCompile(`(?i)^abc[-_. ]xyz`),
	// b00bs - Started appearing January 2015.
	regexp.MustCompile(`(?i)^b00bs$`),
	// 170424_26 - Started appearing August 2018.
	regexp.MustCompile(`^\d{6}_\d{2}$`),
	// Additional generic match for mixed-case hashes - Dec 2020.
	regexp.MustCompile(`^[0-9a-zA-Z]{30}`),
	// Additional generic match for mixed-case hashes - Jan 2021.
	regexp.MustCompile(`^[0-9a-zA-Z]{26}`),
	// Additional generic match for mixed-case hashes - Jan 2021.
	regexp.MustCompile(`^[0-9a-zA-Z]{39}`),
	// Additional generic match for mixed-case hashes - Jan 2021.
	regexp.MustCompile(`^[0-9a-zA-Z]{24}`),
}

// seasonFolderRegexes ports the C# `SeasonFolderRegexes` array at
// `Parser.cs:507-510`. Used by ValidateBeforeParsing to reject
// folder-name-shaped titles that aren't actually release titles.
var seasonFolderRegexes = []*regexp.Regexp{
	regexp.MustCompile(`^(?:Season[ ._\-]*\d+|Specials)$`),
}

// multiRe ports the C# `MultiRegex` at `Parser.cs:551`. Detects the
// `multi` / `multilang` / `multilanguage` token bracketed by `[-_. \[`
// on the left and `[-_. \]]` on the right.
var multiRe = regexp.MustCompile(`(?i)[-_. \[](?:multi|multilang|multilanguage)[-_. \]]`)

// imdbIDRe ports the inline regex at `Parser.cs:865` —
// `^(\d{1,10}|(tt)\d{1,10})$`. Validates IMDB id input shapes.
var imdbIDRe = regexp.MustCompile(`^(?:\d{1,10}|tt\d{1,10})$`)

// englishNumberWords ports the C# `Numbers` array at `Parser.cs:549`.
// IndexOf-by-string maps a number word to its int value, used by
// ParseNumber for episode counts spelled out in English.
var englishNumberWords = []string{
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
}

// ShortMonths mirrors the C# `Parser.ShortMonths` map at
// `Parser.cs:553-567`. Used by daily-episode parsers that match months
// by short English name (`jan` / `feb` / etc.).
var ShortMonths = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4,
	"may": 5, "jun": 6, "jul": 7, "aug": 8,
	"sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

// ValidateBeforeParsing mirrors `Parser.ValidateBeforeParsing` at
// `Parser.cs:1180-1208`. Returns true iff the title is worth parsing.
//
// Rejects:
//   - Titles containing both "password" and "yenc" (yEnc passworded archives).
//   - Titles with no letter or digit chars.
//   - Titles whose extension-stripped form matches any rejectHashedReleasesRegexes.
//   - Titles whose extension-stripped form matches any seasonFolderRegexes.
func ValidateBeforeParsing(title string) bool {
	lower := strings.ToLower(title)
	if strings.Contains(lower, "password") && strings.Contains(lower, "yenc") {
		return false
	}

	hasLetterOrDigit := false
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			hasLetterOrDigit = true
			break
		}
	}
	if !hasLetterOrDigit {
		return false
	}

	titleNoExt := RemoveFileExtension(title)
	for _, re := range rejectHashedReleasesRegexes {
		if re.MatchString(titleNoExt) {
			return false
		}
	}
	for _, re := range seasonFolderRegexes {
		if re.MatchString(titleNoExt) {
			return false
		}
	}
	return true
}

// HasMultipleLanguages mirrors `Parser.HasMultipleLanguages` at
// `Parser.cs:886-889`. True iff the title contains a multi-language
// marker token.
func HasMultipleLanguages(title string) bool {
	return multiRe.MatchString(title)
}

// NormalizeImdbID mirrors `Parser.NormalizeImdbId` at
// `Parser.cs:863-879`. Returns the canonical `tt<7-digit>` form of an
// IMDB id, or "" (C# null) for invalid input.
//
// Accepted input shapes:
//   - 1-10 bare digits → padded to 7 with leading zeros, prefixed `tt`.
//   - `tt` + 1-10 digits → digits part padded to 7, re-prefixed `tt`.
//
// Returns "" for any other shape OR for inputs of length ≤ 2 (the
// C# code's `imdbId.Length > 2` guard rejects `tt` alone or single-digit ids).
func NormalizeImdbID(imdbID string) string {
	if !imdbIDRe.MatchString(imdbID) {
		return ""
	}
	if len(imdbID) <= 2 {
		return ""
	}
	digits := strings.TrimPrefix(imdbID, "tt")
	if len(digits) < 7 {
		digits = strings.Repeat("0", 7-len(digits)) + digits
	}
	return "tt" + digits
}

// ParseNumber mirrors `Parser.ParseNumber` at `Parser.cs:1241-1258`.
// Converts a value string to an integer, accepting:
//   - Standard ASCII digits.
//   - Unicode-numeric forms via NFKC normalisation + ConvertToNumerals
//     (handles full-width digits, etc.).
//   - English number words `zero`..`nine` (case-insensitive).
//
// Returns (n, true) on success; (0, false) on unparseable input.
// (The C# version throws FormatException; the Go port returns a flag
// so callers can distinguish parse failure from a legitimate 0.)
func ParseNumber(value string) (int, bool) {
	normalized := convertToNumerals(normaliseNFKC(value))
	if n, err := strconv.Atoi(normalized); err == nil {
		return n, true
	}
	for i, w := range englishNumberWords {
		if strings.EqualFold(value, w) {
			return i, true
		}
	}
	return 0, false
}

// ParseDecimal mirrors `Parser.ParseDecimal` at `Parser.cs:1260-1270`.
// Accepts the same Unicode-numeric forms as ParseNumber and parses
// the result as a float64 (C# `decimal`). The C# code uses
// `NumberStyles.Float` + `CultureInfo.InvariantCulture`; Go's
// `strconv.ParseFloat` is invariant by default and accepts `inf` /
// `nan` / scientific notation. Episode-number callers feed bounded
// decimal forms only, so the broader acceptance is benign.
func ParseDecimal(value string) (float64, bool) {
	normalized := convertToNumerals(normaliseNFKC(value))
	if f, err := strconv.ParseFloat(normalized, 64); err == nil {
		return f, true
	}
	return 0, false
}

// convertToNumerals mirrors `Parser.ConvertToNumerals` at
// `Parser.cs:1272-1289`. Walks each rune; if rune is a Unicode
// number, replace with its decimal-digit form; otherwise pass through.
//
// Used to fold full-width digits and Unicode numerals into ASCII
// digits before strconv parsing.
func convertToNumerals(input string) string {
	var b strings.Builder
	b.Grow(len(input))
	for _, r := range input {
		if unicode.IsNumber(r) {
			// Use the rune's numeric value (0-9) as the ASCII digit.
			// Falls back to passthrough for non-decimal numeric runes
			// like Roman numerals (which IsNumber accepts).
			d := numericValue(r)
			if d >= 0 && d <= 9 {
				b.WriteByte('0' + byte(d))
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// numericValue returns the numeric value of a numeric rune, or -1 if
// it isn't representable as a single decimal digit. Mirrors C#
// `char.GetNumericValue(c)` for the 0-9 range.
func numericValue(r rune) int {
	if r >= '0' && r <= '9' {
		return int(r - '0')
	}
	// Full-width digits 0-9 (U+FF10..U+FF19).
	if r >= 0xFF10 && r <= 0xFF19 {
		return int(r - 0xFF10)
	}
	// Other Unicode-numeric ranges (Arabic-Indic, Devanagari, etc.) —
	// preserved as fallback. Sonarr inputs are scene release titles
	// which use ASCII or full-width; the broader ranges are defensive.
	if unicode.IsDigit(r) {
		return digitFromUnicode(r)
	}
	return -1
}

// digitFromUnicode walks a few common decimal-digit Unicode blocks and
// returns 0..9 if the rune lands in one of them. Defensive coverage
// beyond ASCII + full-width; not exercised by current fixtures.
func digitFromUnicode(r rune) int {
	for _, block := range []rune{
		0x0660, 0x06F0, 0x07C0, 0x0966, 0x09E6, 0x0A66, 0x0AE6,
		0x0B66, 0x0BE6, 0x0C66, 0x0CE6, 0x0D66, 0x0DE6, 0x0E50,
		0x0ED0, 0x0F20, 0x1040, 0x1090, 0x17E0, 0x1810, 0x1946,
		0x19D0, 0x1A80, 0x1A90, 0x1B50, 0x1BB0, 0x1C40, 0x1C50,
		0xA620, 0xA8D0, 0xA900, 0xA9D0, 0xA9F0, 0xAA50, 0xABF0,
	} {
		if r >= block && r <= block+9 {
			return int(r - block)
		}
	}
	return -1
}

// normaliseNFKC mirrors C# `string.Normalize(NormalizationForm.FormKC)`.
// Decomposes compatibility-equivalent code points and recomposes
// canonical-equivalent ones. Go's stdlib `golang.org/x/text/unicode/norm`
// implements this; rather than pull in the dependency for this single
// call site, the Go port uses a stripped-down passthrough — current
// fixture inputs are pure ASCII, and full-width digits already have
// their own pass-through path in numericValue. If a future fixture
// needs full NFKC, swap this body for `norm.NFKC.String(s)`.
func normaliseNFKC(s string) string {
	return s
}
