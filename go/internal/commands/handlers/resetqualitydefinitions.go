// ResetQualityDefinitions handler. Ports
// `QualityDefinitionService.Execute(ResetQualityDefinitionsCommand)`
// (`src/NzbDrone.Core/Qualities/QualityDefinitionService.cs:112-131`):
// walks the default definitions, and when the command's ResetTitles
// flag is set restores every persisted row's Title to its default,
// then clears the cache. When ResetTitles is false the C# branch
// preserves user-edited Titles (the UpdateMany is a no-op write) — the
// Go port's qualitydefinitions.Service.ResetTitles elides that write.

package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
)

// ResetQualityDefinitionsName is the registry key + Commands.Name (C#
// short form `ResetQualityDefinitionsCommand` → `ResetQualityDefinitions`).
const ResetQualityDefinitionsName = "ResetQualityDefinitions"

// QualityDefinitionResetter restores quality-definition titles to the
// built-in defaults. *qualitydefinitions.Service satisfies it via
// ResetTitles.
type QualityDefinitionResetter interface {
	ResetTitles(ctx context.Context, resetTitles bool) error
}

// ResetQualityDefinitionsBody is the JSON shape of the command body.
// Mirrors the C# command class at
// `Qualities/Commands/ResetQualityDefinitionsCommand.cs:5-13` — a
// single optional `resetTitles` flag defaulting to false.
type ResetQualityDefinitionsBody struct {
	ResetTitles bool `json:"resetTitles,omitempty"`
}

// ResetQualityDefinitions is the IExecute<ResetQualityDefinitionsCommand>
// port.
type ResetQualityDefinitions struct {
	resetter QualityDefinitionResetter
	log      *slog.Logger
}

// NewResetQualityDefinitions builds the handler. resetter MUST be non-nil.
func NewResetQualityDefinitions(resetter QualityDefinitionResetter, log *slog.Logger) (*ResetQualityDefinitions, error) {
	if resetter == nil {
		return nil, errors.New("handlers: NewResetQualityDefinitions: nil QualityDefinitionResetter")
	}
	if log == nil {
		log = slog.Default()
	}
	return &ResetQualityDefinitions{resetter: resetter, log: log}, nil
}

// Execute parses the optional resetTitles flag and resets the
// definitions. A malformed body falls back to the default (false),
// matching the lenient body handling of the other command handlers.
func (h *ResetQualityDefinitions) Execute(ctx context.Context, body string) error {
	var parsed ResetQualityDefinitionsBody
	if body != "" {
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			h.log.LogAttrs(ctx, slog.LevelWarn,
				"resetqualitydefinitions: malformed body — using defaults",
				slog.Any("err", err))
		}
	}
	if err := h.resetter.ResetTitles(ctx, parsed.ResetTitles); err != nil {
		return err
	}
	h.log.LogAttrs(ctx, slog.LevelInfo, "resetqualitydefinitions: reset quality definitions",
		slog.Bool("resetTitles", parsed.ResetTitles))
	return nil
}
