package downloadclients

import "time"

// TorrentSeedConfiguration mirrors the C# class at
// `src/NzbDrone.Core/Download/Clients/TorrentSeedConfiguration.cs:1-12`.
// Carries the per-grab seeding-policy override Sonarr stamps onto a
// torrent at the moment it's handed to the download client.
//
// Both fields are pointers because the C# uses `double?` and
// `TimeSpan?` — nil means "fall back to the client's default". A
// torrent client receiving nil values writes the global default
// (TransmissionRC, qBittorrent global ratio limit, etc.).
//
// SeedTime is a *time.Duration so callers don't have to translate
// the C# TimeSpan minutes-or-hours convention at the boundary; the
// Go port carries arbitrary durations through.
type TorrentSeedConfiguration struct {
	Ratio    *float64
	SeedTime *time.Duration
}

// DefaultTorrentSeedConfiguration is the C# `DefaultConfiguration`
// singleton at `:7`. Both fields nil → "use the client's default."
// Mirrors the static reference so call sites that want "no override"
// can pass this rather than constructing a fresh empty struct each
// time.
var DefaultTorrentSeedConfiguration = TorrentSeedConfiguration{}
