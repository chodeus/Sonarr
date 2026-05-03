package auth

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// DeriveCookieName mirrors AuthenticationBuilderExtensions.cs:33-43 —
// the C# code derives the auth cookie name from the InstanceName
// config value by stripping diacritics and removing every non-
// alphanumeric character, then suffixing "Auth". Empty / all-stripped
// instance names fall back to "Sonarr" so the cookie name is never
// just "Auth".
//
// Examples (matching the C# behaviour):
//
//	DeriveCookieName("")            == "SonarrAuth"
//	DeriveCookieName("Sonarr")      == "SonarrAuth"
//	DeriveCookieName("Sönarr 4K")   == "Sonarr4KAuth"
//	DeriveCookieName("---")         == "SonarrAuth"
//	DeriveCookieName("Café")        == "CafeAuth"
//
// The implementation uses NFKD normalisation + combining-mark strip
// (the standard "RemoveDiacritics" recipe on Go), followed by an
// alphanumeric-only filter. The C# CookieNameRegex is
// `[^a-z0-9]+` with IgnoreCase, so case is preserved on the way
// through — we don't lowercase.
func DeriveCookieName(instanceName string) string {
	stripped := stripDiacritics(instanceName)
	var b strings.Builder
	b.Grow(len(stripped))
	for _, r := range stripped {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "SonarrAuth"
	}
	return b.String() + "Auth"
}

// stripDiacritics returns s with combining marks removed after NFKD
// normalisation. This is the standard idiom for the
// "RemoveDiacritics" extension that the C# tree gets from the
// Diacritical NuGet package — there is no equivalent helper in Go's
// stdlib, but golang.org/x/text/unicode/norm + the unicode.IsMark
// predicate cover the same ground.
func stripDiacritics(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range norm.NFKD.String(s) {
		if unicode.IsMark(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
