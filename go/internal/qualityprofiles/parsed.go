package qualityprofiles

import (
	"encoding/json"
	"fmt"

	"sonarr.tv/sonarr/internal/quality"
)

// ParseItems decodes `Profile.Items` (the embedded JSON array) into
// the typed [QualityItem] slice. Empty / `[]` / `null` payloads decode
// to a non-nil empty slice — callers can range without nil checks.
func ParseItems(s string) ([]QualityItem, error) {
	if s == "" || s == "null" {
		return []QualityItem{}, nil
	}
	var items []QualityItem
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil, fmt.Errorf("qualityprofiles: parse items: %w", err)
	}
	if items == nil {
		return []QualityItem{}, nil
	}
	return items, nil
}

// MarshalItems encodes the typed [QualityItem] slice back to the
// embedded JSON form for persistence. Always emits `[]` (not null) for
// empty slices, matching the C# wire shape (`new List<...>()`).
func MarshalItems(items []QualityItem) (string, error) {
	if items == nil {
		items = []QualityItem{}
	}
	out, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("qualityprofiles: marshal items: %w", err)
	}
	return string(out), nil
}

// ParseFormatItems decodes `Profile.FormatItems` (the embedded JSON
// array) into the typed [FormatItem] slice. Empty / null payloads
// decode to a non-nil empty slice.
func ParseFormatItems(s string) ([]FormatItem, error) {
	if s == "" || s == "null" {
		return []FormatItem{}, nil
	}
	var items []FormatItem
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil, fmt.Errorf("qualityprofiles: parse format items: %w", err)
	}
	if items == nil {
		return []FormatItem{}, nil
	}
	return items, nil
}

// MarshalFormatItems encodes the typed slice back to JSON.
func MarshalFormatItems(items []FormatItem) (string, error) {
	if items == nil {
		items = []FormatItem{}
	}
	out, err := json.Marshal(items)
	if err != nil {
		return "", fmt.Errorf("qualityprofiles: marshal format items: %w", err)
	}
	return string(out), nil
}

// ParsedItems is a convenience that calls [ParseItems] on
// `Profile.Items` — useful where the Service has already produced a
// `Profile` and the caller wants the typed view.
func (p *Profile) ParsedItems() ([]QualityItem, error) {
	return ParseItems(p.Items)
}

// ParsedFormatItems is the FormatItems equivalent of [Profile.ParsedItems].
func (p *Profile) ParsedFormatItems() ([]FormatItem, error) {
	return ParseFormatItems(p.FormatItems)
}

// SetItems re-encodes the typed slice into `Profile.Items`. Mirrors
// the round-trip semantic mutation handlers want: parse → mutate →
// SetItems.
func (p *Profile) SetItems(items []QualityItem) error {
	s, err := MarshalItems(items)
	if err != nil {
		return err
	}
	p.Items = s
	return nil
}

// SetFormatItems is the FormatItems equivalent of [Profile.SetItems].
func (p *Profile) SetFormatItems(items []FormatItem) error {
	s, err := MarshalFormatItems(items)
	if err != nil {
		return err
	}
	p.FormatItems = s
	return nil
}

// LookupIndex ports `QualityProfile.GetIndex(quality, respectGroupOrder)`
// at `QualityProfile.cs:53-89` — except it returns `(QualityIndex, bool)`
// instead of conflating "first slot" and "not found" into the same
// zero-value. Walks the typed Items slice and returns the position of
// `q` (matched by ID) in the profile.
//
// `respectGroupOrder = true` returns `(top, group)` for a quality
// inside a group; `false` collapses to just `(top, 0)`.
//
// New consumers should prefer this signature. [GetIndex] preserves the
// C# zero-value-on-miss behaviour for parity with existing callers.
func LookupIndex(items []QualityItem, q quality.Quality, respectGroupOrder bool) (QualityIndex, bool) {
	return LookupIndexByID(items, q.ID, respectGroupOrder)
}

// LookupIndexByID is the int-id variant of LookupIndex, mirroring the
// C# overload at `QualityProfile.cs:58-89`.
//
// Lookup priority within one top-level slot:
//
//  1. Direct quality match (`item.Quality.ID == id`)
//  2. Group ID match (`item.ID > 0 && item.ID == id`)
//  3. Group-member quality match (walks `item.Items` for a quality whose
//     `id` matches; honours `respectGroupOrder` for the GroupIndex).
//
// Order matches `QualityProfile.cs:60-86` exactly.
func LookupIndexByID(items []QualityItem, id int, respectGroupOrder bool) (QualityIndex, bool) {
	for i, item := range items {
		if item.Quality.ID > 0 && item.Quality.ID == id {
			return QualityIndex{Index: i}, true
		}
		if item.ID > 0 && item.ID == id {
			return QualityIndex{Index: i}, true
		}
		for g, child := range item.Items {
			if child.Quality.ID == id {
				if respectGroupOrder {
					return QualityIndex{Index: i, GroupIndex: g}, true
				}
				return QualityIndex{Index: i}, true
			}
		}
	}
	return QualityIndex{}, false
}

// GetIndex ports the C# `QualityProfile.GetIndex(int)` overload
// verbatim — including the zero-value-on-miss behaviour. New
// consumers should prefer [LookupIndexByID] which signals miss
// explicitly.
func GetIndex(items []QualityItem, q quality.Quality, respectGroupOrder bool) QualityIndex {
	idx, _ := LookupIndex(items, q, respectGroupOrder)
	return idx
}

// CalculateCustomFormatScore ports
// `QualityProfile.CalculateCustomFormatScore(formats)` at
// `QualityProfile.cs:91-94`. Sums `Score` for every FormatItem whose
// FormatID is present in `presentFormatIDs`.
//
// `presentFormatIDs` is a set rather than a slice — the C#
// `formats.Contains(x.Format)` runs at O(n²) on raw slices; using a
// set in Go costs the same but is faster for typical profile sizes.
func CalculateCustomFormatScore(items []FormatItem, presentFormatIDs map[int]bool) int {
	score := 0
	for _, fi := range items {
		if presentFormatIDs[fi.FormatID] {
			score += fi.Score
		}
	}
	return score
}
