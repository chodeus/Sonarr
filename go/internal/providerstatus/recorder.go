package providerstatus

import (
	"context"
	"time"
)

// Recorder is the narrow record-and-query surface that every per-family
// status service exposes (indexerstatus.Service, downloadclientstatus.Service,
// notificationstatus.Service, importliststatus.Service). C# cite:
// src/NzbDrone.Core/ThingiProvider/Status/ProviderStatusServiceBase.cs:11-17.
//
// The C# code parameterises ProviderStatusServiceBase<TProvider, TModel>
// over the per-family Provider + Status row types. Go does not have
// IL-generic services in the same shape, so we landed on a different
// design in Phase 3:
//
//   - The shared logic (Tunables, State, ApplyFailure, ApplySuccess,
//     EscalationBackOff Periods, MaxEscalationLevel) lives in this
//     `providerstatus` package and is consumed by every family service
//     through a State value-type marshal.
//   - Each family has its own typed Service that owns persistence
//     (the typed Repo with a family-specific Status row that may carry
//     extra columns — e.g. indexerstatus.Status carries
//     LastRssSyncReleaseInfo) and event publication (a family-specific
//     StatusChangedEvent type so per-package handlers can subscribe).
//
// This file adds the narrow interfaces that bridge the design — Recorder
// is the surface every consumer (indexer/import-list/notification/
// download-client provider) takes as a parameter when it wants to record
// outcomes, without coupling to a specific family's Service struct.
//
// GetBlockedProviders is NOT in Recorder because it returns the family-
// specific Status type. The BlockedView interface (below) is a
// projection that lets generic readers (e.g. the IndexerStatusCheck
// health check at queue [10]) iterate disabled-provider IDs without
// caring which family they belong to.
//
// Closes queue [3] of Phase 4 — the typed-per-family + narrow shared
// interfaces design intentionally diverges from the C# generic-base-
// class structure to fit Go's idioms.
type Recorder interface {
	// RecordSuccess decrements EscalationLevel and clears DisabledTill
	// when EscalationLevel was non-zero. No-op when already 0. Mirrors
	// C# ProviderStatusServiceBase.RecordSuccess at :60-83.
	RecordSuccess(ctx context.Context, providerID int) error

	// RecordFailure escalates EscalationLevel by one, sets DisabledTill
	// to now+BackOffFor(level), and seeks the level upward if
	// minimumBackOff > 0 until BackOffFor(level) >= minimumBackOff.
	// Mirrors C# RecordFailure at :85-140 with escalate=true.
	RecordFailure(ctx context.Context, providerID int, minimumBackOff time.Duration) error

	// RecordConnectionFailure is RecordFailure with escalate=false —
	// suitable for transport-level errors that shouldn't bump the level
	// beyond what an immediate retry would warrant. Mirrors the
	// `RecordFailure(providerId, escalate: false)` overload at C# :142-149.
	RecordConnectionFailure(ctx context.Context, providerID int) error

	// OnProviderDeleted removes the status row for the given provider.
	// Mirrors C# IHandleAsync<ProviderDeletedEvent<TProvider>> at
	// :151-156.
	OnProviderDeleted(ctx context.Context, providerID int) error
}

// BlockedProvider is the narrow projection the BlockedView interface
// returns. It carries the bare minimum needed for the IndexerStatusCheck
// health-check: which provider is currently disabled, and until when.
//
// Family-specific Status types (which carry extras like
// LastRssSyncReleaseInfo) implement BlockedView via small adapter methods
// in their own packages — those are the per-family Service.GetBlocked
// methods that pre-existed; this file only adds the shared interface.
type BlockedProvider struct {
	ProviderID      int
	DisabledTill    *time.Time
	EscalationLevel int
}

// BlockedView is the read-side counterpart to Recorder. Returns IDs of
// providers currently inside their DisabledTill window. C# cite:
// IProviderStatusServiceBase.GetBlockedProviders at :13.
//
// Each per-family Service implements this via a small projection:
//
//	func (s *Service) GetBlocked(ctx) ([]providerstatus.BlockedProvider, error) {
//	    rows, err := s.GetBlockedProviders(ctx)
//	    ...
//	}
//
// See per-family slices for the actual implementations.
type BlockedView interface {
	GetBlocked(ctx context.Context) ([]BlockedProvider, error)
}
