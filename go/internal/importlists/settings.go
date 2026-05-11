package importlists

// Settings is the marker interface every import-list-settings type
// implements. Mirrors C# `IImportListSettings` at
// `src/NzbDrone.Core/ImportLists/IImportListSettings.cs:5-8` filtered
// through `ImportListSettingsBase<TSettings>` at
// `src/NzbDrone.Core/ImportLists/ImportListSettingsBase.cs:7-30`.
//
// The C# base parameterises on TSettings purely to derive an
// `Equ.MemberwiseEqualityComparer` for hasDefinitionChanged
// round-trip detection — the Go port leaves equality to the
// per-package Settings struct (Go has reflect.DeepEqual; the
// equ.MemberwiseEquality nicety isn't worth porting).
//
// Concrete settings types (SonarrSettings, PlexSettings, etc. —
// queue [I3] and follow-ups) implement Validate() to surface
// FluentValidation-style field-level failures. The factory-level
// Test entry point at C# `ImportListFactory.cs:74-93` invokes
// Validate before invoking the per-provider Test() hook.
//
// BaseUrl is intentionally NOT exposed on this Go interface — every
// concrete C# import list reads the field directly off its own
// settings struct (the C# interface only declares the property to
// allow `Equ.MemberwiseEquality` to walk through it). Concrete Go
// settings types carry a `BaseURL string` field as a convention.
//
// Slice 4.AK (sub-arc I2a of queue [I2]) — foundation slice.
// Pattern-mirror of notifications.Settings at
// `internal/notifications/settings.go`.
type Settings interface {
	// Validate runs FluentValidation-style field-level checks
	// against the settings instance. Returns the failures (empty
	// slice = valid). Mirrors C#
	// `ImportListSettingsBase<T>.Validate()` at
	// `ImportListSettingsBase.cs:14`.
	Validate() []TestFailure
}

// TestFailure is one FluentValidation-style field-level failure
// returned by [Settings.Validate] or by an import-list provider's
// Test() hook. Mirrors C# `ValidationFailure` at the Provider test
// boundary.
//
// Kept as a value type (not an interface) to match the
// notifications.TestFailure / downloadclients.TestFailure precedent
// at `internal/notifications/settings.go:42-57`. Field names are
// identical so a future shared package can collapse the three if a
// cross-family need emerges; until then duplication is cheaper than
// premature coupling.
type TestFailure struct {
	// PropertyName is the dotted-path field this failure applies
	// to. Empty string for whole-form failures (e.g. "Test failed:
	// <message>" — see C# `ImportListFactory.cs:80-92` /
	// per-provider Test() bodies).
	PropertyName string

	// ErrorMessage is the human-readable failure description. The
	// v3 React UI renders this verbatim next to the field input.
	ErrorMessage string

	// DetailedDescription is the optional long-form explanation
	// some validators attach. Empty when no long-form is available;
	// the UI shows it under "More info" if non-empty.
	DetailedDescription string
}
