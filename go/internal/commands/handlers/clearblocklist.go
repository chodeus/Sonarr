// ClearBlocklist handler. Ports
// `BlocklistService.Execute(ClearBlocklistCommand)`
// (`src/NzbDrone.Core/Blocklisting/BlocklistService.cs:121-124`) →
// `_blocklistRepository.Purge()`. Removes every blocklist row.

package handlers

import (
	"context"
	"errors"
	"log/slog"
)

// ClearBlocklistName is the registry key + Commands.Name (C# short form
// `ClearBlocklistCommand` → `ClearBlocklist`).
const ClearBlocklistName = "ClearBlocklist"

// BlocklistClearer purges every blocklist row. *blocklist.Service
// satisfies it via Clear.
type BlocklistClearer interface {
	Clear(ctx context.Context) (int64, error)
}

// ClearBlocklist is the IExecute<ClearBlocklistCommand> port.
type ClearBlocklist struct {
	clearer BlocklistClearer
	log     *slog.Logger
}

// NewClearBlocklist builds the handler. clearer MUST be non-nil.
func NewClearBlocklist(clearer BlocklistClearer, log *slog.Logger) (*ClearBlocklist, error) {
	if clearer == nil {
		return nil, errors.New("handlers: NewClearBlocklist: nil BlocklistClearer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ClearBlocklist{clearer: clearer, log: log}, nil
}

// Execute purges the blocklist. The command body is ignored (the C#
// command carries no parameters).
func (h *ClearBlocklist) Execute(ctx context.Context, _ string) error {
	n, err := h.clearer.Clear(ctx)
	if err != nil {
		return err
	}
	h.log.LogAttrs(ctx, slog.LevelInfo, "clearblocklist: cleared blocklist", slog.Int64("removed", n))
	return nil
}
