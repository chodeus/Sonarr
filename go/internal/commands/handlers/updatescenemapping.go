package handlers

import (
	"context"
	"errors"
	"log/slog"
)

// UpdateSceneMappingName is the registry key + Commands.Name +
// ScheduledTasks.TypeName. Mirrors the C# short form:
// `UpdateSceneMappingCommand` → `UpdateSceneMapping`.
const UpdateSceneMappingName = "UpdateSceneMapping"

// UpdateSceneMappingIntervalMinutes is the seed scheduled-task
// cadence. Matches C# `TaskManager.cs:90` — `Interval = 3 * 60`
// (3 hours in minutes).
const UpdateSceneMappingIntervalMinutes = 3 * 60

// SceneMappingUpdater is the narrow interface
// [UpdateSceneMapping] handler reaches for. The production wiring
// binds `*scenemappings.Service` via Go structural typing
// (`UpdateMappings(ctx) error`). Defined here so test code can
// inject a stub without constructing a fully-wired service.
type SceneMappingUpdater interface {
	UpdateMappings(ctx context.Context) error
}

// UpdateSceneMapping is the IExecute<UpdateSceneMappingCommand> port.
// Ports C# `SceneMappingService.Execute(UpdateSceneMappingCommand)`
// at `src/NzbDrone.Core/DataAugmentation/Scene/SceneMappingService.cs:335-338`.
//
// The C# implementation is a thin one-liner: `UpdateMappings();`.
// The Go port preserves that shape — the handler delegates to
// `Service.UpdateMappings(ctx)` and propagates any error.
//
// Slice 4.AZ (sub-arc M6b-ii of queue [M6]) — closes Phase 4
// focused-subset 5 (METADATA-SOURCES family).
type UpdateSceneMapping struct {
	updater SceneMappingUpdater
	log     *slog.Logger
}

// NewUpdateSceneMapping builds the handler. updater MUST be non-nil
// — there's no useful stub for "refresh scene mappings from cloud"
// (a stub would silently no-op the periodic refresh). log defaults
// to slog.Default when nil.
func NewUpdateSceneMapping(updater SceneMappingUpdater, log *slog.Logger) (*UpdateSceneMapping, error) {
	if updater == nil {
		return nil, errors.New("handlers: NewUpdateSceneMapping: nil SceneMappingUpdater")
	}
	if log == nil {
		log = slog.Default()
	}
	return &UpdateSceneMapping{updater: updater, log: log}, nil
}

// Execute delegates to Service.UpdateMappings. The error contract
// matches the C# `Execute(...)` method — service errors propagate
// to the dispatcher which logs + persists the command as failed.
//
// The body argument is unused (C# `UpdateSceneMappingCommand` has
// no payload).
func (h *UpdateSceneMapping) Execute(ctx context.Context, _ string) error {
	h.log.LogAttrs(ctx, slog.LevelInfo,
		"updatescenemapping: refreshing scene mappings",
		slog.String("command", UpdateSceneMappingName))
	if err := h.updater.UpdateMappings(ctx); err != nil {
		return err
	}
	return nil
}
