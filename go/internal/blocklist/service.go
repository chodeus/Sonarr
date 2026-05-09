package blocklist

import (
	"context"
	"errors"
	"fmt"
)

// EntryWriter is the slice of [Repo] the [Service] reaches for. Pulled
// out so callers can inject a stub in tests without dragging the full
// CRUD surface; the real repo satisfies the interface via the embedded
// generic [repo.Repository].
//
// Slice 3.BA (sub-arc 10a) ships only the Insert method — the bulk
// of [BlocklistService] (Block / Delete / Paged / Blocklisted) ports
// in follow-up sub-arcs. The "Writer" name is a forward-looking
// commitment to the eventual write-surface; reads can join later as
// the consumers (the IHandle subscribers in 10b, the
// trackeddownloads-driven Block call in the queue/import wire) land.
type EntryWriter interface {
	Insert(ctx context.Context, e *Entry) (*Entry, error)
}

// Service ports a subset of `IBlocklistService` at
// `Blocklisting/BlocklistService.cs:16-24`. This slice (3.BA) lands
// the public Add wrapper that the slice-3.BB / Phase-4 IHandle
// subscribers route through to write a Blocklist row.
//
// Other methods on the C# interface (Blocklisted /
// BlocklistedTorrentHash / Paged / Block / Delete) are deferred —
// each needs additional repo lookups (BlocklistedByTitle,
// BlocklistedByTorrentInfoHash, GetPaged) that haven't ported yet
// and consumers haven't ported either, so adding them now would be
// speculative.
//
// Goroutine-safe: stateless after construction.
type Service struct {
	repo EntryWriter
}

// NewService binds an EntryWriter. nil writer returns nil so a caller
// wiring a service stack at startup can skip optional subscribers
// without an explicit guard. Mirrors the nil-tolerance pattern in
// [history.NewService] (slice 3.62/3.AQ) and
// [downloadhistory.NewService] (slice 3.63).
func NewService(w EntryWriter) *Service {
	if w == nil {
		return nil
	}
	return &Service{repo: w}
}

// Add inserts a new Blocklist row. Mirrors the C# write call sites
// at `BlocklistService.cs:75-99` (Block) and `:126-158`
// (Handle(DownloadFailedEvent)) — both build a Blocklist locally
// and call `_blocklistRepository.Insert(blocklist)` directly. The
// Go port surfaces the write as a public Service.Add so the future
// IHandle subscribers (sub-arc 10b — the Phase-3 DownloadFailed +
// DownloadIgnored consumers) and the production trackeddownloads-
// driven Block path can route through one wrapper rather than each
// importing the Repo.
//
// Validation expectations mirror the C# Insert call sites' implicit
// invariants:
//
//   - SeriesID required (>0). The C# `Block` reads
//     `remoteEpisode.Series.Id` which throws on a nil Series; the
//     Go port surfaces a typed error instead.
//   - SourceTitle / Date required for the same reason — both columns
//     are NOT NULL in the schema (`Blocklisting/Blocklist.cs` +
//     migration 230) and a missing value would raise an SQL error
//     at insert time anyway. Surfacing a typed error here makes the
//     failure path obvious at the Service boundary.
//   - EpisodeIDs may be empty — the C# `Block` writes an empty list
//     for the (rare) "season pack rejected at series level" arm
//     where no episode-id mapping is available; mirrored.
//
// Quality / Languages / TorrentInfoHash / Indexer / Message / Source
// are opaque pass-through values the per-event handler builds before
// calling Add. Add does not validate them — the columns either accept
// empty/null or carry their own format checks at the parser layer.
//
// Returns the inserted Entry (with ID populated by the underlying
// Repo.Insert auto-increment) on success.
func (s *Service) Add(ctx context.Context, e *Entry) (*Entry, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("blocklist: Add on nil receiver")
	}
	if e == nil {
		return nil, errors.New("blocklist: Add: nil entry")
	}
	if e.SeriesID <= 0 {
		return nil, fmt.Errorf("blocklist: Add: SeriesID required, got %d", e.SeriesID)
	}
	if e.SourceTitle == "" {
		return nil, errors.New("blocklist: Add: SourceTitle required")
	}
	if e.Date.IsZero() {
		return nil, errors.New("blocklist: Add: Date required")
	}
	out, err := s.repo.Insert(ctx, e)
	if err != nil {
		return nil, fmt.Errorf("blocklist: Add: %w", err)
	}
	return out, nil
}

// Compile-time interface-satisfaction guard. A refactor that breaks
// the contract (Repo loses Insert via the embedded generic, signature
// changes) fails at build time rather than at the wiring callsite.
var _ EntryWriter = (*Repo)(nil)
