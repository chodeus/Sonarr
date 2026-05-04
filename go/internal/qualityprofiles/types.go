package qualityprofiles

import (
	"encoding/json"
	"fmt"

	"sonarr.tv/sonarr/internal/quality"
)

// QualityItem is the typed-view of one entry in
// `Profile.Items`. Mirrors `Profiles/Qualities/QualityProfileQualityItem.cs`.
//
// Two shapes appear in the same list:
//
//   - Quality entry: `Quality` is non-zero; `Items` is empty; `Id`/`Name`
//     are zero/empty. Represents a single quality with its
//     allowed/size flags.
//   - Group entry: `Quality` is zero (the C# `null`); `Id` is the group
//     ID (≥ 1000 by C# convention; user-set in REST patches); `Name` is
//     the group label; `Items` lists the qualities in the group. The
//     `Allowed` flag is shared across all members.
//
// MinSize/MaxSize/PreferredSize are pointers because the C# wire
// shape uses `double?` (nullable). nil/zero are distinguishable —
// migration 207+ writes per-quality size limits but a fresh group
// entry omits them.
//
// JSON encoding mirrors C# verbatim:
//
//   - `Quality` is encoded as just the int Quality.Id (per
//     `QualityIntConverter.cs:15-18`); on decode the id is resolved
//     via `quality.FindByID`.
//   - `Id` is omitted when zero
//     (`[JsonIgnore(Condition = JsonIgnoreCondition.WhenWritingDefault)]`).
//   - `Items` is always emitted; the C# default constructor sets it to
//     `new List<QualityProfileQualityItem>()`. Emit `[]` (not omitted,
//     not null) for parity.
type QualityItem struct {
	ID            int             `json:"id,omitempty"`
	Name          string          `json:"name,omitempty"`
	Quality       quality.Quality `json:"quality"`
	Items         []QualityItem   `json:"items"`
	Allowed       bool            `json:"allowed"`
	MinSize       *float64        `json:"minSize,omitempty"`
	MaxSize       *float64        `json:"maxSize,omitempty"`
	PreferredSize *float64        `json:"preferredSize,omitempty"`
}

// IsGroup reports whether this item is a group (i.e. has nested Items
// and zero Quality). Mirrors the C# `Quality == null` check at
// `QualityProfile.cs:30` / `:62`.
func (i QualityItem) IsGroup() bool {
	return i.Quality.ID == 0 && len(i.Items) > 0
}

// GetQualities mirrors `QualityProfileQualityItem.GetQualities()` at
// `:28-35`. For a quality entry returns a single-element slice; for a
// group entry returns each nested quality's `Quality` field.
func (i QualityItem) GetQualities() []quality.Quality {
	if !i.IsGroup() {
		return []quality.Quality{i.Quality}
	}
	out := make([]quality.Quality, 0, len(i.Items))
	for _, c := range i.Items {
		out = append(out, c.Quality)
	}
	return out
}

// MarshalJSON encodes the typed Quality as just its integer Id, per
// the C# `QualityIntConverter`. The rest of the struct uses standard
// json encoding.
//
// Implementation note: a separate alias type avoids infinite recursion
// when calling `json.Marshal` from within MarshalJSON.
func (i QualityItem) MarshalJSON() ([]byte, error) {
	type alias struct {
		ID            int           `json:"id,omitempty"`
		Name          string        `json:"name,omitempty"`
		Quality       int           `json:"quality"`
		Items         []QualityItem `json:"items"`
		Allowed       bool          `json:"allowed"`
		MinSize       *float64      `json:"minSize,omitempty"`
		MaxSize       *float64      `json:"maxSize,omitempty"`
		PreferredSize *float64      `json:"preferredSize,omitempty"`
	}
	items := i.Items
	if items == nil {
		items = []QualityItem{}
	}
	return json.Marshal(alias{
		ID: i.ID, Name: i.Name, Quality: i.Quality.ID, Items: items,
		Allowed: i.Allowed, MinSize: i.MinSize, MaxSize: i.MaxSize, PreferredSize: i.PreferredSize,
	})
}

// UnmarshalJSON decodes the int-shaped `Quality` field by looking up
// the registry, mirroring `QualityIntConverter.cs:11-13` (which builds
// `new Quality { Id = reader.GetInt32() }`).
//
// An unknown id resolves to `quality.Unknown` per `quality.FindByID`
// semantics — the persisted form may pre-date a quality removal.
// Group entries have Quality == 0 → resolves to Unknown, which is the
// IsGroup signal.
func (i *QualityItem) UnmarshalJSON(data []byte) error {
	type alias struct {
		ID            int             `json:"id"`
		Name          string          `json:"name"`
		Quality       int             `json:"quality"`
		Items         []QualityItem   `json:"items"`
		Allowed       bool            `json:"allowed"`
		MinSize       *float64        `json:"minSize"`
		MaxSize       *float64        `json:"maxSize"`
		PreferredSize *float64        `json:"preferredSize"`
		QualityFull   json.RawMessage `json:"qualityFull,omitempty"`
	}
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return fmt.Errorf("qualityprofiles: unmarshal QualityItem: %w", err)
	}
	q, _ := quality.FindByID(a.Quality)
	*i = QualityItem{
		ID: a.ID, Name: a.Name, Quality: q,
		Items: a.Items, Allowed: a.Allowed,
		MinSize: a.MinSize, MaxSize: a.MaxSize, PreferredSize: a.PreferredSize,
	}
	if i.Items == nil {
		i.Items = []QualityItem{}
	}
	return nil
}

// FormatItem is the typed-view of one entry in `Profile.FormatItems`.
// Mirrors `Profiles/ProfileFormatItem.cs` plus the
// `CustomFormatIntConverter` that flattens the persisted CustomFormat
// reference to just its integer Id.
//
// We keep only `FormatID + Score` here. The full CustomFormat row
// resolution happens at the boundary where scoring runs (decision
// engine in Phase 3) — the typed view holds the wire-shape data. This
// matches the C# `CustomFormatIntConverter.Read` at `:11-13` which
// constructs `new CustomFormat { Id = reader.GetInt32() }` (a stub
// CustomFormat carrying only the Id).
type FormatItem struct {
	FormatID int `json:"format"`
	Score    int `json:"score"`
}

// QualityIndex ports `Profiles/Qualities/QualityIndex.cs`. The
// `Index` is the position in the `Items` slice (top-level); the
// `GroupIndex` is the position within a nested group's `Items`. A
// non-found result is the zero value (`{Index: 0, GroupIndex: 0}`),
// which intentionally collides with "the first quality at the top
// level" — the C# version has the same ambiguity. Callers that need
// to distinguish use `respectGroupOrder = true` plus the position-aware
// CompareTo.
type QualityIndex struct {
	Index      int
	GroupIndex int
}

// CompareTo mirrors `QualityIndex.CompareTo(other, respectGroupOrder)`
// at `QualityIndex.cs:38-53`. Returns -1/0/1.
func (q QualityIndex) CompareTo(other QualityIndex, respectGroupOrder bool) int {
	switch {
	case q.Index < other.Index:
		return -1
	case q.Index > other.Index:
		return 1
	}
	if !respectGroupOrder {
		return 0
	}
	switch {
	case q.GroupIndex < other.GroupIndex:
		return -1
	case q.GroupIndex > other.GroupIndex:
		return 1
	}
	return 0
}
