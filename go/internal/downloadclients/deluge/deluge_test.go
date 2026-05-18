package deluge_test

import (
	"context"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/deluge"
	"sonarr.tv/sonarr/internal/indexers"
)

// ============================================================================
// Settings.Validate — ports rules from
// `Download/Clients/Deluge/DelugeSettings.cs:7-17`.
// ============================================================================

func TestSettings_DefaultsValid(t *testing.T) {
	t.Parallel()
	s := deluge.NewSettings()
	got := s.Validate()
	if len(got) != 0 {
		t.Errorf("Validate() = %+v, want no failures", got)
	}
}

func TestSettings_DefaultsMatchCSharpConstructor(t *testing.T) {
	t.Parallel()
	s := deluge.NewSettings()
	if s.Host != "localhost" {
		t.Errorf("Host = %q, want localhost", s.Host)
	}
	if s.Port != 8112 {
		t.Errorf("Port = %d, want 8112", s.Port)
	}
	if s.Password != "deluge" {
		t.Errorf("Password = %q, want deluge", s.Password)
	}
	if s.TvCategory != "tv-sonarr" {
		t.Errorf("TvCategory = %q, want tv-sonarr", s.TvCategory)
	}
}

func TestSettings_HostRequired(t *testing.T) {
	t.Parallel()
	s := deluge.NewSettings()
	s.Host = "  "
	got := s.Validate()
	if !hasFailure(got, "Host") {
		t.Errorf("Validate() = %+v, want Host failure", got)
	}
}

func TestSettings_PortOutOfRange(t *testing.T) {
	t.Parallel()
	for _, port := range []int{0, -1, 65536, 99999} {
		s := deluge.NewSettings()
		s.Port = port
		got := s.Validate()
		if !hasFailure(got, "Port") {
			t.Errorf("Validate(Port=%d) = %+v, want Port failure", port, got)
		}
	}
}

func TestSettings_CategoryAccepted(t *testing.T) {
	t.Parallel()
	for _, c := range []string{
		"",
		"tv-sonarr",
		"abc123",
		"123",
		"-",
		"a-b-c-1-2-3",
	} {
		s := deluge.NewSettings()
		s.TvCategory = c
		got := s.Validate()
		if hasFailure(got, "TvCategory") {
			t.Errorf("Validate(TvCategory=%q) reported failure: %+v", c, got)
		}
	}
}

func TestSettings_CategoryRejected(t *testing.T) {
	t.Parallel()
	for _, c := range []string{
		"TV-SONARR", // uppercase
		"tv sonarr", // space
		"tv_sonarr", // underscore
		"tv/sonarr", // slash
		"tv.sonarr", // dot
		"sonarr!",   // bang
	} {
		s := deluge.NewSettings()
		s.TvCategory = c
		got := s.Validate()
		if !hasFailure(got, "TvCategory") {
			t.Errorf("Validate(TvCategory=%q) = %+v, want TvCategory failure", c, got)
		}
	}
}

func TestSettings_ImportedCategoryRegex(t *testing.T) {
	t.Parallel()
	s := deluge.NewSettings()
	s.TvImportedCategory = "BAD CASE"
	got := s.Validate()
	if !hasFailure(got, "TvImportedCategory") {
		t.Errorf("Validate(TvImportedCategory=%q) = %+v, want TvImportedCategory failure", s.TvImportedCategory, got)
	}
}

// ============================================================================
// Registry registration — slice 5.G2a.
// ============================================================================

func TestRegistry_DelugeRegistered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(deluge.Implementation, deluge.ConfigContract) {
		t.Fatalf("Default registry has no entry for (%s,%s)",
			deluge.Implementation, deluge.ConfigContract)
	}
}

func TestRegistry_ConstructProvider(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		ID:             3,
		Name:           "deluge-local",
		Implementation: deluge.Implementation,
		ConfigContract: deluge.ConfigContract,
		Settings:       `{"host":"deluge.example","port":9090,"useSsl":true,"password":"secret","tvCategory":"tv-sonarr"}`,
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct() err = %v", err)
	}
	if got := p.Name(); got != "Deluge" {
		t.Errorf("Name() = %q, want Deluge", got)
	}
	if got := p.Protocol(); got != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol() = %v, want torrent", got)
	}
	if p.Definition() != def {
		t.Errorf("Definition() did not echo input row")
	}

	d, ok := p.(*deluge.Deluge)
	if !ok {
		t.Fatalf("Construct() returned %T, want *deluge.Deluge", p)
	}
	got := d.Settings()
	if got.Host != "deluge.example" || got.Port != 9090 || !got.UseSsl || got.Password != "secret" {
		t.Errorf("Settings() = %+v, want JSON round-trip", got)
	}
}

func TestRegistry_ConstructBadJSON(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: deluge.Implementation,
		ConfigContract: deluge.ConfigContract,
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
		Implementation: deluge.Implementation,
		ConfigContract: deluge.ConfigContract,
		Settings:       "",
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct() err = %v", err)
	}
	got := p.(*deluge.Deluge).Settings()
	if got.Host != "localhost" || got.Port != 8112 || got.Password != "deluge" || got.TvCategory != "tv-sonarr" {
		t.Errorf("default Settings = %+v, want NewSettings() defaults", got)
	}
}

// ============================================================================
// Provider stubs — slice 5.G2a leaves the heavy methods returning
// ErrNotImplementedYet. Lock that down so a regression turns into a
// loud failure when the body slice lands.
// ============================================================================

func TestStub_GetImportItemPassesThrough(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	in := &downloadclients.Item{DownloadID: "abc"}
	got, err := d.GetImportItem(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("GetImportItem() err = %v", err)
	}
	if got != in {
		t.Errorf("GetImportItem returned %p, want passthrough %p", got, in)
	}
}

func TestStub_RequestActionReturnsNilNil(t *testing.T) {
	t.Parallel()
	d := deluge.New(nil, deluge.NewSettings(), nil, nil)
	got, err := d.RequestAction(context.Background(), "anyAction", nil)
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
