package notifications

// Settings is the marker interface every notification-settings type
// implements. Mirrors C# `IProviderConfig` at
// `src/NzbDrone.Core/ThingiProvider/IProviderConfig.cs` filtered
// through the C# `NotificationSettingsBase<TSettings>` shape at
// `src/NzbDrone.Core/Notifications/NotificationSettingsBase.cs:8-29`.
//
// The C# base parameterises on TSettings purely to derive a
// MemberwiseEqualityComparer for hasDefinitionChanged round-trip
// detection — the Go port leaves equality to the per-package
// Settings struct (Go has reflect.DeepEqual; the equ.MemberwiseEquality
// nicety isn't worth porting).
//
// Concrete settings types (WebhookSettings, DiscordSettings, etc. —
// queue [N3] and follow-ups) implement Validate() to surface
// FluentValidation-style field-level failures. The factory-level
// Test entry point at C# `NotificationFactory.cs:210-229` invokes
// Validate before invoking the per-provider Test() hook.
//
// Slice 4.AC (sub-arc N2a of queue [N2]).
type Settings interface {
	// Validate runs FluentValidation-style field-level checks
	// against the settings instance. Returns the failures (empty
	// slice = valid). Mirrors C#
	// `NotificationSettingsBase<T>.Validate()` at
	// `NotificationSettingsBase.cs:13`.
	Validate() []TestFailure
}

// TestFailure is one FluentValidation-style field-level failure
// returned by [Settings.Validate] or by a notification provider's
// Test() hook. Mirrors C# `ValidationFailure` at the Provider test
// boundary.
//
// Kept as a value type (not an interface) to match the
// downloadclients.TestFailure precedent at
// `internal/downloadclients/provider.go:344-361`. Field names are
// identical so a future shared package can collapse the two if a
// cross-family need emerges; until then duplication is cheaper than
// premature coupling.
type TestFailure struct {
	// PropertyName is the dotted-path field this failure applies
	// to. Empty string for whole-form failures (e.g. "Test failed:
	// <message>" — see C# `NotificationFactory.cs:212` / per-provider
	// Test() bodies).
	PropertyName string

	// ErrorMessage is the human-readable failure description. The
	// v3 React UI renders this verbatim next to the field input.
	ErrorMessage string

	// DetailedDescription is the optional long-form explanation
	// some validators attach. Empty when no long-form is available;
	// the UI shows it under "More info" if non-empty.
	DetailedDescription string
}
