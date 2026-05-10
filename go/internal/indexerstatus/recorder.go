package indexerstatus

import (
	"context"

	"sonarr.tv/sonarr/internal/providerstatus"
)

// GetBlocked is the BlockedView projection of GetBlockedProviders that
// strips the family-specific extras (LastRssSyncReleaseInfo) so callers
// targeting providerstatus.BlockedView can iterate disabled providers
// without depending on indexerstatus.Status. C# cite: see the package-
// level note in providerstatus/recorder.go.
func (s *Service) GetBlocked(ctx context.Context) ([]providerstatus.BlockedProvider, error) {
	rows, err := s.GetBlockedProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]providerstatus.BlockedProvider, 0, len(rows))
	for _, r := range rows {
		out = append(out, providerstatus.BlockedProvider{
			ProviderID:      r.ProviderID,
			DisabledTill:    r.DisabledTill,
			EscalationLevel: r.EscalationLevel,
		})
	}
	return out, nil
}

// Compile-time interface assertions: the Service must satisfy both the
// shared Recorder write-surface and the BlockedView read-surface.
// Closes queue [3] of Phase 4.
var (
	_ providerstatus.Recorder    = (*Service)(nil)
	_ providerstatus.BlockedView = (*Service)(nil)
)
