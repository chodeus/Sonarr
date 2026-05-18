package qbittorrent_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/qbittorrent"
	"sonarr.tv/sonarr/internal/indexers"
)

// ============================================================================
// Settings.Validate — ports rules from
// `Download/Clients/QBittorrent/QBittorrentSettings.cs:9-29`.
// ============================================================================

func TestSettings_DefaultsValid(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	got := s.Validate()
	if len(got) != 0 {
		t.Errorf("Validate() = %+v, want no failures", got)
	}
}

func TestSettings_HostRequired(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	s.Host = "  "
	got := s.Validate()
	if !hasFailure(got, "Host") {
		t.Errorf("Validate() = %+v, want Host failure", got)
	}
}

func TestSettings_PortOutOfRange(t *testing.T) {
	t.Parallel()
	for _, port := range []int{0, -1, 65536, 99999} {
		s := qbittorrent.NewSettings()
		s.Port = port
		got := s.Validate()
		if !hasFailure(got, "Port") {
			t.Errorf("Validate(Port=%d) = %+v, want Port failure", port, got)
		}
	}
}

func TestSettings_UrlBaseMustStartWithSlash(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	s.UrlBase = "qbittorrent"
	got := s.Validate()
	if !hasFailure(got, "UrlBase") {
		t.Errorf("Validate() = %+v, want UrlBase failure", got)
	}
}

func TestSettings_UrlBaseEmptyOk(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	s.UrlBase = ""
	got := s.Validate()
	if hasFailure(got, "UrlBase") {
		t.Errorf("Validate() reported UrlBase failure for empty UrlBase: %+v", got)
	}
}

func TestSettings_UrlBaseSlashPrefixOk(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	s.UrlBase = "/qbittorrent"
	got := s.Validate()
	if hasFailure(got, "UrlBase") {
		t.Errorf("Validate() reported UrlBase failure for slash-prefixed UrlBase: %+v", got)
	}
}

func TestSettings_ApiKeyExclusiveWithUsername(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	s.ApiKey = "abc"
	s.Username = "admin"
	got := s.Validate()
	if !hasFailure(got, "Username") {
		t.Errorf("Validate() = %+v, want Username failure", got)
	}
}

func TestSettings_ApiKeyExclusiveWithPassword(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	s.ApiKey = "abc"
	s.Password = "secret"
	got := s.Validate()
	if !hasFailure(got, "Password") {
		t.Errorf("Validate() = %+v, want Password failure", got)
	}
}

func TestSettings_ApiKeyAloneOk(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	s.ApiKey = "abc"
	got := s.Validate()
	for _, f := range got {
		if f.PropertyName == "Username" || f.PropertyName == "Password" {
			t.Errorf("Validate() flagged %s=%q with ApiKey-only auth: %+v", f.PropertyName, f, got)
		}
	}
}

func TestSettings_PasswordWithoutApiKeyOk(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	s.Username = "admin"
	s.Password = "secret"
	got := s.Validate()
	if hasFailure(got, "Username") || hasFailure(got, "Password") {
		t.Errorf("Validate() flagged Username/Password without ApiKey: %+v", got)
	}
}

func TestSettings_CategoryAccepted(t *testing.T) {
	t.Parallel()
	for _, c := range []string{
		"",
		"tv-sonarr",
		"tv_sonarr",
		"tv sonarr",
		"TV-SONARR",
		"sonarr.tv",
		"sonarr/sub",
		"a/b/c",
		"a",
	} {
		s := qbittorrent.NewSettings()
		s.TvCategory = c
		got := s.Validate()
		if hasFailure(got, "TvCategory") {
			t.Errorf("Validate(TvCategory=%q) reported failure: %+v (allowed)", c, got)
		}
	}
}

func TestSettings_CategoryRejected(t *testing.T) {
	t.Parallel()
	for _, c := range []string{
		`tv\sonarr`,
		"/tv-sonarr",
		"tv-sonarr/",
		"a//b",
		`a\b`,
	} {
		s := qbittorrent.NewSettings()
		s.TvCategory = c
		got := s.Validate()
		if !hasFailure(got, "TvCategory") {
			t.Errorf("Validate(TvCategory=%q) = %+v, want TvCategory failure", c, got)
		}
	}
}

func TestSettings_ImportedCategoryRegex(t *testing.T) {
	t.Parallel()
	s := qbittorrent.NewSettings()
	s.TvImportedCategory = "/bad"
	got := s.Validate()
	if !hasFailure(got, "TvImportedCategory") {
		t.Errorf("Validate(TvImportedCategory=%q) = %+v, want TvImportedCategory failure", s.TvImportedCategory, got)
	}
}

// ============================================================================
// Registry registration — slice 5.G1a.
// ============================================================================

func TestRegistry_QBittorrentRegistered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(qbittorrent.Implementation, qbittorrent.ConfigContract) {
		t.Fatalf("Default registry has no entry for (%s,%s)",
			qbittorrent.Implementation, qbittorrent.ConfigContract)
	}
}

func TestRegistry_ConstructProvider(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		ID:             7,
		Name:           "qbit-local",
		Implementation: qbittorrent.Implementation,
		ConfigContract: qbittorrent.ConfigContract,
		Settings:       `{"host":"qbit.example","port":9090,"useSsl":true,"tvCategory":"tv-sonarr"}`,
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct() err = %v", err)
	}
	if got := p.Name(); got != "qBittorrent" {
		t.Errorf("Name() = %q, want %q", got, "qBittorrent")
	}
	if got := p.Protocol(); got != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol() = %v, want torrent", got)
	}
	if p.Definition() != def {
		t.Errorf("Definition() did not echo input row")
	}

	qbit, ok := p.(*qbittorrent.QBittorrent)
	if !ok {
		t.Fatalf("Construct() returned %T, want *qbittorrent.QBittorrent", p)
	}
	got := qbit.Settings()
	if got.Host != "qbit.example" || got.Port != 9090 || !got.UseSsl || got.TvCategory != "tv-sonarr" {
		t.Errorf("Settings() = %+v, want JSON round-trip", got)
	}
}

func TestRegistry_ConstructBadJSON(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: qbittorrent.Implementation,
		ConfigContract: qbittorrent.ConfigContract,
		Settings:       `{bad json`,
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
		Implementation: qbittorrent.Implementation,
		ConfigContract: qbittorrent.ConfigContract,
		Settings:       "",
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct() err = %v", err)
	}
	got := p.(*qbittorrent.QBittorrent).Settings()
	if got.Host != "localhost" || got.Port != 8080 || got.TvCategory != "tv-sonarr" {
		t.Errorf("default Settings = %+v, want NewSettings() defaults", got)
	}
}

// ============================================================================
// Provider stubs — slice 5.G1a leaves the heavy methods returning
// ErrNotImplementedYet. Lock that down so a regression turns into a
// loud failure when the body slice lands.
// ============================================================================

func TestStub_DownloadReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	_, err := q.Download(context.Background(), &downloadclients.GrabRequest{})
	if !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("Download() err = %v, want ErrNotImplementedYet", err)
	}
}

func TestStub_RemoveItemReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	err := q.RemoveItem(context.Background(), &downloadclients.Item{}, false)
	if !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("RemoveItem() err = %v, want ErrNotImplementedYet", err)
	}
}

func TestStub_GetStatusReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	_, err := q.GetStatus(context.Background())
	if !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetStatus() err = %v, want ErrNotImplementedYet", err)
	}
}

func TestStub_MarkItemAsImportedReturnsNotImplemented(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	err := q.MarkItemAsImported(context.Background(), &downloadclients.Item{})
	if !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("MarkItemAsImported() err = %v, want ErrNotImplementedYet", err)
	}
}

func TestStub_GetImportItemPassesThrough(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	in := &downloadclients.Item{DownloadID: "abc"}
	got, err := q.GetImportItem(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("GetImportItem() err = %v", err)
	}
	if got != in {
		t.Errorf("GetImportItem returned %p, want passthrough %p", got, in)
	}
}

func TestStub_RequestActionReturnsNilNil(t *testing.T) {
	t.Parallel()
	q := qbittorrent.New(nil, qbittorrent.NewSettings(), nil, nil)
	got, err := q.RequestAction(context.Background(), "getCategories", nil)
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
