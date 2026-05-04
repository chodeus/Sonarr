package quality

// DefaultDefinition mirrors the runtime decoration the C# tree applies
// to a persisted `QualityDefinition` row from
// `Quality.DefaultQualityDefinitions` at `Quality.cs:158-179`.
//
// Persistence shape: Id, Quality (int), Title — only Title is
// configurable per user. Weight, GroupName, MinSize, MaxSize,
// PreferredSize are NOT persisted (`TableMapping.cs:135-140` `.Ignore()`s
// them) — they're decoration-only data that the service layer attaches
// at read time. Migration 207 (2024-09) moved the size fields off
// QualityDefinition entirely; they now live on QualityProfile.Items
// per quality. So in modern Sonarr the only Definition fields the
// service layer surfaces are Title (persisted), Weight (default),
// GroupName (default).
//
// The Title field on this struct is the canonical default — it's
// `Quality.Name` per `QualityDefinition.cs:25` (`Title = quality.Name`).
// Persisted Title rows can override it; the seed pass uses this
// default for first-time inserts.
type DefaultDefinition struct {
	Quality   Quality
	Title     string
	Weight    int
	GroupName string
}

// DefaultDefinitions returns the canonical 22-entry default-definition
// table. Order matches `Quality.cs:158-179`. Used by:
//
//   - QualityDefinitionService to seed missing rows + decorate
//     persisted rows with Weight/GroupName.
//   - QualityProfileService.GetDefaultProfile to build the per-Quality
//     profile-item set.
//
// Returns a fresh copy so callers cannot mutate the registry.
func DefaultDefinitions() []DefaultDefinition {
	out := make([]DefaultDefinition, len(defaultDefinitions))
	copy(out, defaultDefinitions)
	return out
}

// DefaultDefinitionFor returns the default-definition entry for the
// given Quality, or (zero, false) if the Quality isn't in the default
// table.
func DefaultDefinitionFor(q Quality) (DefaultDefinition, bool) {
	for _, d := range defaultDefinitions {
		if d.Quality.ID == q.ID {
			return d, true
		}
	}
	return DefaultDefinition{}, false
}

// defaultDefinitions is the canonical 22-entry default-definition
// table, mirroring `Quality.DefaultQualityDefinitions` at
// `Quality.cs:158-179`. Title comes from `QualityDefinition.cs:25`
// (`Title = quality.Name`). The size fields (MinSize/MaxSize/
// PreferredSize) are intentionally NOT carried here — see the
// DefaultDefinition doc comment.
var defaultDefinitions = []DefaultDefinition{
	{Quality: Unknown, Title: Unknown.Name, Weight: 1},
	{Quality: SDTV, Title: SDTV.Name, Weight: 2},
	{Quality: WEBRip480p, Title: WEBRip480p.Name, Weight: 3, GroupName: "WEB 480p"},
	{Quality: WEBDL480p, Title: WEBDL480p.Name, Weight: 3, GroupName: "WEB 480p"},
	{Quality: DVD, Title: DVD.Name, Weight: 4},
	{Quality: Bluray480p, Title: Bluray480p.Name, Weight: 5},
	{Quality: Bluray576p, Title: Bluray576p.Name, Weight: 6},
	{Quality: HDTV720p, Title: HDTV720p.Name, Weight: 7},
	{Quality: HDTV1080p, Title: HDTV1080p.Name, Weight: 8},
	{Quality: RawHD, Title: RawHD.Name, Weight: 9},
	{Quality: WEBRip720p, Title: WEBRip720p.Name, Weight: 10, GroupName: "WEB 720p"},
	{Quality: WEBDL720p, Title: WEBDL720p.Name, Weight: 10, GroupName: "WEB 720p"},
	{Quality: Bluray720p, Title: Bluray720p.Name, Weight: 11},
	{Quality: WEBRip1080p, Title: WEBRip1080p.Name, Weight: 12, GroupName: "WEB 1080p"},
	{Quality: WEBDL1080p, Title: WEBDL1080p.Name, Weight: 12, GroupName: "WEB 1080p"},
	{Quality: Bluray1080p, Title: Bluray1080p.Name, Weight: 13},
	{Quality: Bluray1080pRemux, Title: Bluray1080pRemux.Name, Weight: 14},
	{Quality: HDTV2160p, Title: HDTV2160p.Name, Weight: 15},
	{Quality: WEBRip2160p, Title: WEBRip2160p.Name, Weight: 16, GroupName: "WEB 2160p"},
	{Quality: WEBDL2160p, Title: WEBDL2160p.Name, Weight: 16, GroupName: "WEB 2160p"},
	{Quality: Bluray2160p, Title: Bluray2160p.Name, Weight: 17},
	{Quality: Bluray2160pRemux, Title: Bluray2160pRemux.Name, Weight: 18},
}
