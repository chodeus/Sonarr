// Package localization is the Go port of
// NzbDrone.Core.Localization.LocalizationService — the i18n string
// table the v3 REST API surfaces at GET /api/v3/localization and
// every C# tree access to `_localizationService.GetLocalizedString
// ("Key")` resolves against.
//
// Phase 5 ships the English baseline: en.json is embedded into the
// binary via go:embed and parsed once at package init. Future
// slices will add per-language overlay support mirroring the C#
// `LocalizationService.GetLocalizationDictionary(culture)` merge
// logic at `src/NzbDrone.Core/Localization/LocalizationService.cs`.
//
// Source file `en.json` is copied verbatim from
// `src/NzbDrone.Core/Localization/Core/en.json` at slice 4.EK ship
// time. The duplication is intentional: go:embed can't traverse
// upward into the C# tree, so the file lives alongside this Go
// package. Future C# tree edits won't automatically propagate — a
// follow-up slice will add a code-generator that re-copies on a
// schedule, or the next port phase will pull the C# tree behind a
// well-defined sync point.
package localization

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed en.json
var enJSON []byte

// englishDict is parsed once at init. JSON object key order in Go
// is non-deterministic on map decode, but the wire shape doesn't
// promise an order — the test rig normalizes keys alphabetically
// before byte-eq, so any order is contract-safe.
var englishDict map[string]string

func init() {
	if err := json.Unmarshal(enJSON, &englishDict); err != nil {
		panic(fmt.Sprintf("localization: parse embedded en.json: %v", err))
	}
}

// Service exposes the localization-string read surface. For the
// English-only slice, every accessor returns from the static dict.
// A future per-user-language overlay will gain a Set/Reload pair
// and a culture-keyed map, with English remaining the fallback.
type Service struct{}

// NewService constructs a Service. Future per-language overlays
// will inject a [config.Provider] here; for the English-only slice
// the Service is stateless and the constructor is parameter-free.
func NewService() *Service {
	return &Service{}
}

// GetLocalizationDictionary mirrors
// `LocalizationService.GetLocalizationDictionary()` at
// `src/NzbDrone.Core/Localization/LocalizationService.cs`. Returns a
// FRESH copy so callers may mutate without affecting the cache.
// The wire shape is `{ "strings": {key: value, ...} }`; the resource
// layer adds the outer envelope.
func (s *Service) GetLocalizationDictionary() map[string]string {
	out := make(map[string]string, len(englishDict))
	for k, v := range englishDict {
		out[k] = v
	}
	return out
}

// GetLocalizedString mirrors
// `LocalizationService.GetLocalizedString(key)` at
// `src/NzbDrone.Core/Localization/LocalizationService.cs:62-80` (the
// no-args overload). Unknown keys return the key itself — same
// pass-through behaviour as the C# `out var msg` early-return.
func (s *Service) GetLocalizedString(key string) string {
	if v, ok := englishDict[key]; ok {
		return v
	}
	return key
}

// Count reports the number of localization entries in the current
// dictionary. Useful for tests that pin the embedded-file integrity.
func Count() int {
	return len(englishDict)
}
