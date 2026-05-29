// ClearLog handler. Ports `LogService.Execute(ClearLogCommand)`
// (`src/NzbDrone.Core/Instrumentation/LogService.cs:26-29`) →
// `_logRepository.Purge(vacuum: true)`. Removes every row from the log
// database.

package handlers

import (
	"context"
	"errors"
	"log/slog"
)

// ClearLogName is the registry key + Commands.Name (C# short form
// `ClearLogCommand` → `ClearLog`).
const ClearLogName = "ClearLog"

// LogClearer purges every log row. *logs.Repo satisfies it via Purge.
type LogClearer interface {
	Purge(ctx context.Context) (int64, error)
}

// ClearLog is the IExecute<ClearLogCommand> port.
type ClearLog struct {
	clearer LogClearer
	log     *slog.Logger
}

// NewClearLog builds the handler. clearer MUST be non-nil.
func NewClearLog(clearer LogClearer, log *slog.Logger) (*ClearLog, error) {
	if clearer == nil {
		return nil, errors.New("handlers: NewClearLog: nil LogClearer")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ClearLog{clearer: clearer, log: log}, nil
}

// Execute purges the log database. The command body is ignored (the C#
// command carries no parameters).
func (h *ClearLog) Execute(ctx context.Context, _ string) error {
	n, err := h.clearer.Purge(ctx)
	if err != nil {
		return err
	}
	h.log.LogAttrs(ctx, slog.LevelInfo, "clearlog: cleared log database", slog.Int64("removed", n))
	return nil
}
