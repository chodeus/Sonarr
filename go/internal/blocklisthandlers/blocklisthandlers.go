// Package blocklisthandlers holds the IHandle<TEvent> port equivalents
// for [blocklist.Service]'s two event subscribers. Lives in its own
// package because the events the handlers consume
// ([trackeddownloads.DownloadFailedEvent] +
// [series.SeriesDeletedEvent]) live in packages that can't be imported
// by [blocklist] without creating cycles — same posture as the
// slice-3.AT historyhandlers sibling.
//
// Each handler builds (or removes) [blocklist.Entry] rows from an
// event payload and routes them through [*blocklist.Service]. The
// cmd/sonarr boot site subscribes one lambda per handler at the
// message bus.
//
// # Where this lives in the C# tree
//
// The C# `BlocklistService` ports both IHandle implementations
// directly on the service class itself
// (`Blocklisting/BlocklistService.cs:126-163`). The Go-port factors
// them into this dedicated package per the cycle note above. The
// handler bodies are 1:1 with their C# counterparts modulo the
// import-graph divergence.
package blocklisthandlers
