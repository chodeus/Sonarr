package healthcheck_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/healthcheck"
)

// newProvider builds a fresh configfile.Provider against a temp
// config.xml whose APIKey is set to fixedKey via the generator.
// The first APIKey() call writes the value; subsequent calls return it.
func newProvider(t *testing.T, fixedKey string) *configfile.Provider {
	t.Helper()
	dir := t.TempDir()
	loader := configfile.NewLoader(filepath.Join(dir, "config.xml"))
	if err := loader.Load(t.Context()); err != nil {
		t.Fatalf("loader.Load: %v", err)
	}
	gen := func() (string, error) { return fixedKey, nil }
	p := configfile.NewProvider(loader, configfile.Overrides{}, nil, gen)
	// Force the value to materialise on disk so subsequent reads
	// don't depend on the generator (which would lose meaning if a
	// test reset the loader).
	if _, err := p.APIKey(t.Context()); err != nil {
		t.Fatalf("APIKey priming: %v", err)
	}
	return p
}

// TestApiKeyValidation_NilProviderRejected — the constructor must
// refuse a nil provider. The check has nothing to do without one;
// a nil panic at first run would surface as a goroutine crash.
func TestApiKeyValidation_NilProviderRejected(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewApiKeyValidationCheck(nil); err == nil {
		t.Errorf("expected error for nil provider, got nil")
	}
}

// TestApiKeyValidation_TooShortFails — key length below 20 fires the
// MinimumApiKeyLength reason. Mirrors the C# `:28-37` branch.
func TestApiKeyValidation_TooShortFails(t *testing.T) {
	t.Parallel()
	p := newProvider(t, "short-key")
	c, err := healthcheck.NewApiKeyValidationCheck(p)
	if err != nil {
		t.Fatalf("NewApiKeyValidationCheck: %v", err)
	}
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("expected Warning, got %+v", got)
	}
	if got.Reason != healthcheck.ReasonMinimumApiKeyLength {
		t.Errorf("Reason = %v, want ReasonMinimumApiKeyLength", got.Reason)
	}
	wantSuffix := "#invalid-api-key"
	if got.WikiURL == "" || got.WikiURL[len(got.WikiURL)-len(wantSuffix):] != wantSuffix {
		t.Errorf("WikiURL = %q, want suffix %q", got.WikiURL, wantSuffix)
	}
}

// TestApiKeyValidation_LongEnoughPasses — key length at or above 20
// passes. Mirrors the implicit happy path at C# `:40`.
func TestApiKeyValidation_LongEnoughPasses(t *testing.T) {
	t.Parallel()
	p := newProvider(t, "this-is-a-twenty-char")
	c, err := healthcheck.NewApiKeyValidationCheck(p)
	if err != nil {
		t.Fatalf("NewApiKeyValidationCheck: %v", err)
	}
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK, got %+v", got)
	}
}

// TestApiKeyValidation_EventTriggers pins the [CheckOn(ConfigSavedEvent)]
// equivalent — the check declares ConfigFileSavedEvent as its trigger.
func TestApiKeyValidation_EventTriggers(t *testing.T) {
	t.Parallel()
	p := newProvider(t, "12345678901234567890")
	c, err := healthcheck.NewApiKeyValidationCheck(p)
	if err != nil {
		t.Fatalf("NewApiKeyValidationCheck: %v", err)
	}
	triggers := c.EventTriggers()
	if len(triggers) != 1 {
		t.Fatalf("triggers = %d, want 1", len(triggers))
	}
	want := reflect.TypeOf(configfile.ConfigFileSavedEvent{})
	if triggers[0].EventType != want {
		t.Errorf("trigger[0].EventType = %v, want %v", triggers[0].EventType, want)
	}
}
