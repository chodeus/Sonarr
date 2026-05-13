package localization_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/localization"
)

func TestService_GetLocalizationDictionary_Count(t *testing.T) {
	t.Parallel()
	// The embedded en.json carries 2269 entries per the C# recording.
	// A drift here means the source file under
	// `src/NzbDrone.Core/Localization/Core/en.json` changed and the
	// Go-side copy needs a refresh.
	const wantCount = 2269
	s := localization.NewService()
	if got := len(s.GetLocalizationDictionary()); got != wantCount {
		t.Errorf("dict size: got %d want %d", got, wantCount)
	}
	if got := localization.Count(); got != wantCount {
		t.Errorf("package-level Count(): got %d want %d", got, wantCount)
	}
}

func TestService_GetLocalizationDictionary_FreshCopy(t *testing.T) {
	t.Parallel()
	// Mutation guard — the dict returned to callers must be a copy,
	// not a shared reference to the package-level map.
	s := localization.NewService()
	d := s.GetLocalizationDictionary()
	d["test-key"] = "test-value"
	again := s.GetLocalizationDictionary()
	if _, ok := again["test-key"]; ok {
		t.Errorf("dict mutation leaked into shared state")
	}
}

func TestService_GetLocalizedString(t *testing.T) {
	t.Parallel()
	s := localization.NewService()
	if got := s.GetLocalizedString("About"); got != "About" {
		t.Errorf("known key: got %q want About", got)
	}
	if got := s.GetLocalizedString("AbsoluteEpisodeNumber"); got != "Absolute Episode Number" {
		t.Errorf("multi-word key: got %q", got)
	}
	if got := s.GetLocalizedString("DefinitelyNotAKey"); got != "DefinitelyNotAKey" {
		t.Errorf("unknown key should pass through: got %q", got)
	}
}
