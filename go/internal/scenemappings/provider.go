package scenemappings

import "context"

// Provider ports the C# `ISceneMappingProvider` interface at
// `src/NzbDrone.Core/DataAugmentation/Scene/ISceneMappingProvider.cs:5-9`.
//
// The C# tree's `SceneMappingService.UpdateMappings` iterates over
// every registered `ISceneMappingProvider`, asks each one for its
// per-provider mapping list, and diff-merges the result against the
// `Type`-namespaced rows in the repo. Today the C# tree ships exactly
// one concrete provider — `SceneMappingProxy` — hitting
// `services.sonarr.tv/v1/scenemapping`. The interface is preserved as
// an extension point in case Sonarr ever adds a second provider; the
// Go port mirrors the same shape.
//
// Slice 4.AX (sub-arc M6a of queue [M6]) — adds the Provider
// interface + the single concrete Proxy impl. Sub-arc M6b wires the
// SceneMappingService.UpdateMappings refresh logic (diff/merge,
// per-type partitioning, event publish) and the
// UpdateSceneMappingCommand handler that triggers it.
//
// ctx divergence: C# `GetSceneMappings()` is sync and ctx-less; the
// Go port adds ctx for cancellation. nil ctx is a programmer error
// — implementations should propagate ctx to their underlying HTTP
// or DB calls.
type Provider interface {
	// GetSceneMappings returns the full mapping list this provider is
	// responsible for. The caller (UpdateMappings, slice M6b) is
	// responsible for filtering invalid rows (empty Title /
	// SearchTerm), normalising the ParseTerm + Type fields, and
	// diff-merging against the persisted set.
	GetSceneMappings(ctx context.Context) ([]Mapping, error)
}
