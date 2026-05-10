package importliststatus

import (
	"context"

	"sonarr.tv/sonarr/internal/providerstatus"
)

// GetBlocked is the BlockedView projection of GetBlockedProviders.
// See providerstatus/recorder.go for the package-level rationale.
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

// Compile-time interface assertions. Closes queue [3] of Phase 4.
var (
	_ providerstatus.Recorder    = (*Service)(nil)
	_ providerstatus.BlockedView = (*Service)(nil)
)
