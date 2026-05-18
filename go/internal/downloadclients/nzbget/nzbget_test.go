package nzbget_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/nzbget"
	"sonarr.tv/sonarr/internal/indexers"
)

// ============================================================================
// Settings.Validate — ports rules from
// `Download/Clients/Nzbget/NzbgetSettings.cs:8-19`.
// ============================================================================

func TestSettings_DefaultsTriggerCategoryWarning(t *testing.T) {
	t.Parallel()
	// NewSettings sets TvCategory="tv" so the category warning
	// doesn't fire — the defaults are fully valid.
	s := nzbget.NewSettings()
	got := s.Validate()
	if len(got) != 0 {
		t.Errorf("Validate() = %+v, want no failures with defaults", got)
	}
}

func TestSettings_DefaultsMatchCSharpConstructor(t *testing.T) {
	t.Parallel()
	s := nzbget.NewSettings()
	if s.Host != "localhost" {
		t.Errorf("Host = %q", s.Host)
	}
	if s.Port != 6789 {
		t.Errorf("Port = %d, want 6789", s.Port)
	}
	if s.TvCategory != "tv" {
		t.Errorf("TvCategory = %q, want tv", s.TvCategory)
	}
	if s.RecentTvPriority != nzbget.PriorityNormal {
		t.Errorf("RecentTvPriority = %d, want Normal=0", s.RecentTvPriority)
	}
}

func TestSettings_HostRequired(t *testing.T) {
	t.Parallel()
	s := nzbget.NewSettings()
	s.Host = "  "
	got := s.Validate()
	if !hasFailure(got, "Host") {
		t.Errorf("Validate() = %+v, want Host failure", got)
	}
}

func TestSettings_PortOutOfRange(t *testing.T) {
	t.Parallel()
	for _, port := range []int{0, -1, 65536, 99999} {
		s := nzbget.NewSettings()
		s.Port = port
		got := s.Validate()
		if !hasFailure(got, "Port") {
			t.Errorf("Validate(Port=%d) = %+v, want Port failure", port, got)
		}
	}
}

func TestSettings_UrlBaseMustStartWithSlash(t *testing.T) {
	t.Parallel()
	s := nzbget.NewSettings()
	s.UrlBase = "nzbget"
	got := s.Validate()
	if !hasFailure(got, "UrlBase") {
		t.Errorf("Validate() = %+v, want UrlBase failure", got)
	}
}

func TestSettings_UrlBaseEmptyOk(t *testing.T) {
	t.Parallel()
	s := nzbget.NewSettings()
	s.UrlBase = ""
	got := s.Validate()
	if hasFailure(got, "UrlBase") {
		t.Errorf("Validate() reported UrlBase failure for empty value: %+v", got)
	}
}

func TestSettings_UsernamePasswordCoSet(t *testing.T) {
	t.Parallel()
	// Username without Password → Password failure.
	s := nzbget.NewSettings()
	s.Username = "admin"
	s.Password = ""
	got := s.Validate()
	if !hasFailure(got, "Password") {
		t.Errorf("Validate(Username only) = %+v, want Password failure", got)
	}

	// Password without Username → Username failure.
	s = nzbget.NewSettings()
	s.Username = ""
	s.Password = "secret"
	got = s.Validate()
	if !hasFailure(got, "Username") {
		t.Errorf("Validate(Password only) = %+v, want Username failure", got)
	}

	// Both empty → no auth-related failures.
	s = nzbget.NewSettings()
	got = s.Validate()
	for _, f := range got {
		if f.PropertyName == "Username" || f.PropertyName == "Password" {
			t.Errorf("Validate(both empty) flagged %s: %+v", f.PropertyName, f)
		}
	}

	// Both non-empty → no auth-related failures.
	s = nzbget.NewSettings()
	s.Username = "admin"
	s.Password = "secret"
	got = s.Validate()
	for _, f := range got {
		if f.PropertyName == "Username" || f.PropertyName == "Password" {
			t.Errorf("Validate(both set) flagged %s: %+v", f.PropertyName, f)
		}
	}
}

func TestSettings_EmptyTvCategoryWarns(t *testing.T) {
	t.Parallel()
	s := nzbget.NewSettings()
	s.TvCategory = ""
	got := s.Validate()
	if !hasFailure(got, "TvCategory") {
		t.Errorf("Validate(empty category) = %+v, want TvCategory warning", got)
	}
}

// ============================================================================
// Registry registration — slice 5.G3a.
// ============================================================================

func TestRegistry_NzbgetRegistered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(nzbget.Implementation, nzbget.ConfigContract) {
		t.Fatalf("Default registry has no entry for (%s,%s)",
			nzbget.Implementation, nzbget.ConfigContract)
	}
}

func TestRegistry_ConstructProvider(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		ID:             4,
		Name:           "nzbget-local",
		Implementation: nzbget.Implementation,
		ConfigContract: nzbget.ConfigContract,
		Settings:       `{"host":"nzbget.example","port":6790,"useSsl":true,"username":"admin","password":"secret","tvCategory":"tv"}`,
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct() err = %v", err)
	}
	if got := p.Name(); got != "NZBGet" {
		t.Errorf("Name() = %q, want NZBGet", got)
	}
	if got := p.Protocol(); got != indexers.DownloadProtocolUsenet {
		t.Errorf("Protocol() = %v, want Usenet", got)
	}
	if p.Definition() != def {
		t.Errorf("Definition() did not echo input row")
	}

	n, ok := p.(*nzbget.Nzbget)
	if !ok {
		t.Fatalf("Construct() returned %T, want *nzbget.Nzbget", p)
	}
	got := n.Settings()
	if got.Host != "nzbget.example" || got.Port != 6790 || !got.UseSsl || got.Username != "admin" || got.Password != "secret" {
		t.Errorf("Settings() = %+v, want JSON round-trip", got)
	}
}

func TestRegistry_ConstructBadJSON(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: nzbget.Implementation,
		ConfigContract: nzbget.ConfigContract,
		Settings:       `{bad`,
	}
	if _, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{}); err == nil {
		t.Fatalf("Construct(bad JSON) err = nil, want error")
	}
}

func TestRegistry_ConstructNilRow(t *testing.T) {
	t.Parallel()
	if _, err := downloadclients.Default().Construct(nil, downloadclients.Dependencies{}); err == nil {
		t.Fatalf("Construct(nil) err = nil, want error")
	}
}

func TestRegistry_DefaultsUsedWhenSettingsEmpty(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: nzbget.Implementation,
		ConfigContract: nzbget.ConfigContract,
		Settings:       "",
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct() err = %v", err)
	}
	got := p.(*nzbget.Nzbget).Settings()
	if got.Host != "localhost" || got.Port != 6789 || got.TvCategory != "tv" {
		t.Errorf("default Settings = %+v, want NewSettings() defaults", got)
	}
}

// ============================================================================
// Provider stubs — slice 5.G3a leaves the heavy methods returning
// ErrNotImplementedYet.
// ============================================================================

func TestStub_GetStatusReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	_, err := n.GetStatus(context.Background())
	if !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetStatus() err = %v, want ErrNotImplementedYet", err)
	}
}

func TestStub_MarkItemAsImportedReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	err := n.MarkItemAsImported(context.Background(), &downloadclients.Item{})
	if !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("MarkItemAsImported() err = %v, want ErrNotImplementedYet", err)
	}
}

func TestStub_GetImportItemPassesThrough(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	in := &downloadclients.Item{DownloadID: "abc"}
	got, err := n.GetImportItem(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("GetImportItem() err = %v", err)
	}
	if got != in {
		t.Errorf("GetImportItem returned %p, want passthrough %p", got, in)
	}
}

func TestStub_RequestActionReturnsNilNil(t *testing.T) {
	t.Parallel()
	n := nzbget.New(nil, nzbget.NewSettings(), nil, nil)
	got, err := n.RequestAction(context.Background(), "anyAction", nil)
	if err != nil {
		t.Errorf("RequestAction() err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("RequestAction() = %v, want nil", got)
	}
}

func hasFailure(failures []downloadclients.TestFailure, property string) bool {
	for _, f := range failures {
		if f.PropertyName == property {
			return true
		}
	}
	return false
}
