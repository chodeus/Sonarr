package scenemappings

// events.go ports the C# `SceneMappingsUpdatedEvent` at
// `src/NzbDrone.Core/DataAugmentation/Scene/SceneMappingsUpdatedEvent.cs`.
//
// Published by [Service.UpdateMappings] after the refresh+diff pass
// completes (whether or not any provider succeeded). Downstream
// consumers (parser path, search service) re-read their caches when
// they receive this event so a refresh propagates immediately
// without waiting for the next lazy populate.

// UpdatedEvent fires after a SceneMappings refresh pass. C# parity:
// the event carries no payload — it's a signal to invalidate any
// derived caches. Subscribers re-read mappings from the repo.
type UpdatedEvent struct{}
