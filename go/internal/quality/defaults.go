package quality

// DefaultDefinition mirrors the runtime decoration the C# tree applies
// to a persisted `QualityDefinition` row from
// `Quality.DefaultQualityDefinitions` at `Quality.cs:158-179`.
//
// Persistence shape: Id, Quality (int), Title — only Title is
// configurable per user. Weight, GroupName, MinSize, MaxSize,
// PreferredSize are NOT persisted (`TableMapping.cs:135-140` `.Ignore()`s
// them) — they're decoration-only data that the service layer attaches
// at read time. Migration 207 (2024-09) moved the per-quality size
// limits off QualityDefinition's persisted columns; they now live on
// QualityProfile.Items per quality. The runtime defaults table here
// still carries them because the QualityProfileService.SeedDefaults
// pass copies them onto every fresh QualityProfile.Items entry —
// matching C# `QualityProfileService.GetDefaultProfile` at
// `QualityProfileService.cs:206-213` which reads
// `group.First().MinSize / MaxSize / PreferredSize` from
// DefaultQualityDefinitions.
//
// The Title field on this struct is the canonical default — it's
// `Quality.Name` per `QualityDefinition.cs:25` (`Title = quality.Name`).
// Persisted Title rows can override it; the seed pass uses this
// default for first-time inserts.
//
// MinSize/MaxSize/PreferredSize use *float64 because the C# fields
// are `double?` (nullable). RawHD + every 1080p/2160p Bluray-Remux +
// every 2160p have MaxSize=null in the C# default table.
type DefaultDefinition struct {
	Quality       Quality
	Title         string
	Weight        int
	GroupName     string
	MinSize       *float64
	MaxSize       *float64
	PreferredSize *float64
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
// (`Title = quality.Name`). MinSize/MaxSize/PreferredSize carry the
// per-quality size defaults the QualityProfileService.SeedDefaults
// pass copies onto every fresh profile's Items entry (post-migration
// 207 these size limits live on QualityProfile.Items rather than the
// persisted QualityDefinition row, but the runtime defaults still
// originate here).
var defaultDefinitions = []DefaultDefinition{
	{Quality: Unknown, Title: Unknown.Name, Weight: 1, MinSize: f64Ptr(1), MaxSize: f64Ptr(199.9), PreferredSize: f64Ptr(95)},
	{Quality: SDTV, Title: SDTV.Name, Weight: 2, MinSize: f64Ptr(2), MaxSize: f64Ptr(100), PreferredSize: f64Ptr(95)},
	{Quality: WEBRip480p, Title: WEBRip480p.Name, Weight: 3, GroupName: "WEB 480p", MinSize: f64Ptr(2), MaxSize: f64Ptr(100), PreferredSize: f64Ptr(95)},
	{Quality: WEBDL480p, Title: WEBDL480p.Name, Weight: 3, GroupName: "WEB 480p", MinSize: f64Ptr(2), MaxSize: f64Ptr(100), PreferredSize: f64Ptr(95)},
	{Quality: DVD, Title: DVD.Name, Weight: 4, MinSize: f64Ptr(2), MaxSize: f64Ptr(100), PreferredSize: f64Ptr(95)},
	{Quality: Bluray480p, Title: Bluray480p.Name, Weight: 5, MinSize: f64Ptr(2), MaxSize: f64Ptr(100), PreferredSize: f64Ptr(95)},
	{Quality: Bluray576p, Title: Bluray576p.Name, Weight: 6, MinSize: f64Ptr(2), MaxSize: f64Ptr(100), PreferredSize: f64Ptr(95)},
	{Quality: HDTV720p, Title: HDTV720p.Name, Weight: 7, MinSize: f64Ptr(3), MaxSize: f64Ptr(125), PreferredSize: f64Ptr(95)},
	{Quality: HDTV1080p, Title: HDTV1080p.Name, Weight: 8, MinSize: f64Ptr(4), MaxSize: f64Ptr(125), PreferredSize: f64Ptr(95)},
	{Quality: RawHD, Title: RawHD.Name, Weight: 9, MinSize: f64Ptr(4), MaxSize: nil, PreferredSize: f64Ptr(95)},
	{Quality: WEBRip720p, Title: WEBRip720p.Name, Weight: 10, GroupName: "WEB 720p", MinSize: f64Ptr(3), MaxSize: f64Ptr(130), PreferredSize: f64Ptr(95)},
	{Quality: WEBDL720p, Title: WEBDL720p.Name, Weight: 10, GroupName: "WEB 720p", MinSize: f64Ptr(3), MaxSize: f64Ptr(130), PreferredSize: f64Ptr(95)},
	{Quality: Bluray720p, Title: Bluray720p.Name, Weight: 11, MinSize: f64Ptr(4), MaxSize: f64Ptr(130), PreferredSize: f64Ptr(95)},
	{Quality: WEBRip1080p, Title: WEBRip1080p.Name, Weight: 12, GroupName: "WEB 1080p", MinSize: f64Ptr(4), MaxSize: f64Ptr(130), PreferredSize: f64Ptr(95)},
	{Quality: WEBDL1080p, Title: WEBDL1080p.Name, Weight: 12, GroupName: "WEB 1080p", MinSize: f64Ptr(4), MaxSize: f64Ptr(130), PreferredSize: f64Ptr(95)},
	{Quality: Bluray1080p, Title: Bluray1080p.Name, Weight: 13, MinSize: f64Ptr(4), MaxSize: f64Ptr(155), PreferredSize: f64Ptr(95)},
	{Quality: Bluray1080pRemux, Title: Bluray1080pRemux.Name, Weight: 14, MinSize: f64Ptr(35), MaxSize: nil, PreferredSize: f64Ptr(95)},
	{Quality: HDTV2160p, Title: HDTV2160p.Name, Weight: 15, MinSize: f64Ptr(35), MaxSize: f64Ptr(199.9), PreferredSize: f64Ptr(95)},
	{Quality: WEBRip2160p, Title: WEBRip2160p.Name, Weight: 16, GroupName: "WEB 2160p", MinSize: f64Ptr(35), MaxSize: nil, PreferredSize: f64Ptr(95)},
	{Quality: WEBDL2160p, Title: WEBDL2160p.Name, Weight: 16, GroupName: "WEB 2160p", MinSize: f64Ptr(35), MaxSize: nil, PreferredSize: f64Ptr(95)},
	{Quality: Bluray2160p, Title: Bluray2160p.Name, Weight: 17, MinSize: f64Ptr(35), MaxSize: nil, PreferredSize: f64Ptr(95)},
	{Quality: Bluray2160pRemux, Title: Bluray2160pRemux.Name, Weight: 18, MinSize: f64Ptr(35), MaxSize: nil, PreferredSize: f64Ptr(95)},
}

// f64Ptr is a tiny helper to keep the defaultDefinitions table
// readable — `MinSize: f64Ptr(2)` reads cleaner than `MinSize: func()
// *float64 { v := 2.0; return &v }()` for the 60-odd literal pointers
// the table needs.
func f64Ptr(v float64) *float64 { return &v }
