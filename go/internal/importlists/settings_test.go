package importlists_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/importlists"
)

// TestFailure is a plain value struct — pin field layout so future
// refactors that rename a field surface as a test breakage. Mirrors
// the equivalent test on notifications.TestFailure.
func TestTestFailure_Zero(t *testing.T) {
	t.Parallel()
	var f importlists.TestFailure
	if f.PropertyName != "" || f.ErrorMessage != "" || f.DetailedDescription != "" {
		t.Errorf("zero TestFailure: got %+v want all-empty", f)
	}
}

// Settings is a marker interface — confirm a minimal Settings impl
// type-asserts against it. Pattern-mirror of the corresponding
// notifications.Settings smoke test.
type fakeSettings struct{ failures []importlists.TestFailure }

func (f fakeSettings) Validate() []importlists.TestFailure { return f.failures }

func TestSettings_MarkerInterface(t *testing.T) {
	t.Parallel()
	var s importlists.Settings = fakeSettings{
		failures: []importlists.TestFailure{
			{PropertyName: "BaseUrl", ErrorMessage: "required"},
		},
	}
	got := s.Validate()
	if len(got) != 1 || got[0].PropertyName != "BaseUrl" {
		t.Errorf("Validate: %+v want one BaseUrl failure", got)
	}
}
