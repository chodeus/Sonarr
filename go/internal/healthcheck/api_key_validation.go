package healthcheck

import (
	"context"
	"fmt"
	"reflect"

	"sonarr.tv/sonarr/internal/configfile"
)

// ApiKeyValidationCheckName is the canonical Source.
const ApiKeyValidationCheckName = "ApiKeyValidationCheck"

// ApiKeyMinimumLength matches the C# `:14` `MinimumLength` constant.
// A shorter API key fails the check with reason MinimumApiKeyLength.
const ApiKeyMinimumLength = 20

// ApiKeyValidationCheck warns when the configured API key is shorter
// than ApiKeyMinimumLength. Mirrors
// `src/NzbDrone.Core/HealthCheck/Checks/ApiKeyValidationCheck.cs:1-43`.
//
// Implements EventDrivenCheck — the binding mirrors the C#
// `[CheckOn(typeof(ConfigSavedEvent))]` attribute at `:11`. The C#
// version also carries `[CheckOn(typeof(ApplicationStartedEvent))]`
// which is redundant in both the C# tree and the Go port (the global
// IEvent handler explicitly skips ApplicationStartedEvent — its
// dedicated handler runs the startup-checks batch via
// CheckOnStartup() instead). The Go port omits the redundant binding.
type ApiKeyValidationCheck struct {
	cfg *configfile.Provider
}

// NewApiKeyValidationCheck binds a configfile.Provider. cfg must not
// be nil — the check has nothing to do without one.
func NewApiKeyValidationCheck(cfg *configfile.Provider) (*ApiKeyValidationCheck, error) {
	if cfg == nil {
		return nil, fmt.Errorf("healthcheck: NewApiKeyValidationCheck: nil configfile provider")
	}
	return &ApiKeyValidationCheck{cfg: cfg}, nil
}

// Name returns the canonical check identifier.
func (*ApiKeyValidationCheck) Name() string { return ApiKeyValidationCheckName }

// CheckOnStartup defaults true (HealthCheckBase default).
func (*ApiKeyValidationCheck) CheckOnStartup() bool { return true }

// CheckOnSchedule defaults true (HealthCheckBase default).
func (*ApiKeyValidationCheck) CheckOnSchedule() bool { return true }

// EventTriggers returns the [CheckOn] equivalents. Mirrors the C#
// attribute at `:11`. The Go port uses ConfigFileSavedEvent — the
// configfile package's signal-of-record for any persisted change to
// the XML config (CFG-Q5 in analysis/questions.md will eventually
// consolidate ConfigSavedEvent + ConfigFileSavedEvent into one
// ConfigChangedEvent; this binding will follow that consolidation).
func (*ApiKeyValidationCheck) EventTriggers() []EventTrigger {
	return []EventTrigger{
		{EventType: reflect.TypeOf(configfile.ConfigFileSavedEvent{})},
	}
}

// Run is the C# Check() port. Returns Warning when the API key is
// shorter than the minimum length. Mirrors `:26-41`.
//
// On a configfile error (extremely unlikely — Provider.APIKey
// auto-generates when missing) the check returns ResultOK rather than
// surfacing the error as a failure: the failure mode "we couldn't
// read the API key" is not the failure mode this check is designed
// to detect, and surfacing it as MinimumApiKeyLength would mislead
// operators.
func (c *ApiKeyValidationCheck) Run(ctx context.Context) HealthCheck {
	apiKey, err := c.cfg.APIKey(ctx)
	if err != nil {
		return OK(ApiKeyValidationCheckName)
	}
	if len(apiKey) < ApiKeyMinimumLength {
		return New(
			ApiKeyValidationCheckName,
			ResultWarning,
			ReasonMinimumApiKeyLength,
			fmt.Sprintf(apiKeyTooShortFormat, ApiKeyMinimumLength),
			"#invalid-api-key",
		)
	}
	return OK(ApiKeyValidationCheckName)
}

// apiKeyTooShortFormat is the localised string at C#
// `Localization/Core/en.json` key `ApiKeyValidationHealthCheckMessage`.
// The C# message takes a `{length}` placeholder; the Go port uses
// fmt.Sprintf with the same minimum length.
const apiKeyTooShortFormat = "Please update your API key to be at least %d characters long"
