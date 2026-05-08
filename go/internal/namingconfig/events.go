package namingconfig

// NamingConfigSavedEvent is published by [Service.Save] when the
// singleton NamingConfig row has been persisted (Insert or Update).
// Mirrors the empty-marker shape of [config.ConfigSavedEvent] +
// [configfile.ConfigFileSavedEvent] — subscribers re-read state
// from the underlying source rather than carry payload on the wire.
//
// The C# tree doesn't define an explicit "naming-config saved" event;
// the only consumer of the cached row in the C# IoC graph is
// `INamingConfigService.GetConfig` itself, and the C# class doesn't
// cache (every call hits the DB). The Go port adds the cache wrapper
// at slice 3.A2 to amortize the per-call DB hit in the slice-3.94
// fileNamerAdapter + slice-3.96 nameBuilderAdapter — both reach into
// GetConfig once per import-decision spec / writer-path call, which
// adds up across an N-file batch.
//
// The event is a hook for future out-of-band writers (a migration
// script, a REST handler that doesn't go through Service.Save, etc.).
// Today every writer flows through Service.Save, so the self-publish
// → self-subscribe path is just one extra invalidate per Save —
// matches the [config.Provider] posture exactly.
type NamingConfigSavedEvent struct{}
