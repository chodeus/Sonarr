package utorrent_test

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/downloadclients/utorrent"
	"sonarr.tv/sonarr/internal/indexers"
)

// ============================================================================
// Settings validation — slice 5.J46.
// ============================================================================

func TestSettings_DefaultsValid(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	if failures := s.Validate(); len(failures) != 0 {
		t.Errorf("Validate(default): %+v, want no failures", failures)
	}
	if s.Host != "localhost" || s.Port != 8080 || s.TvCategory != "tv-sonarr" {
		t.Errorf("NewSettings: %+v (want Host=localhost, Port=8080, TvCategory=tv-sonarr)", s)
	}
}

func TestSettings_HostRequired(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.Host = ""
	if !hasFailure(s.Validate(), "Host") {
		t.Errorf("empty host: want Host failure")
	}
}

func TestSettings_PortOutOfRange(t *testing.T) {
	t.Parallel()
	for _, port := range []int{0, -1, 65536} {
		port := port
		t.Run("", func(t *testing.T) {
			t.Parallel()
			s := utorrent.NewSettings()
			s.Port = port
			if !hasFailure(s.Validate(), "Port") {
				t.Errorf("Port=%d: want Port failure", port)
			}
		})
	}
}

func TestSettings_UrlBaseMustStartWithSlash(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.UrlBase = "utorrent" // no leading slash
	if !hasFailure(s.Validate(), "UrlBase") {
		t.Errorf("UrlBase without leading /: want UrlBase failure")
	}
}

func TestSettings_UrlBaseSlashPrefixOk(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.UrlBase = "/utorrent"
	if hasFailure(s.Validate(), "UrlBase") {
		t.Errorf("UrlBase with leading /: should pass")
	}
}

func TestSettings_CategoryRequired(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.TvCategory = ""
	if !hasFailure(s.Validate(), "TvCategory") {
		t.Errorf("empty category: want TvCategory failure")
	}
}

func TestSettings_JSONRoundTrip(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: utorrent.Implementation,
		ConfigContract: utorrent.ConfigContract,
		Settings: `{
			"host":"utor.example",
			"port":8443,
			"useSsl":true,
			"urlBase":"/utorrent",
			"username":"sonarr",
			"password":"hunter2",
			"tvCategory":"tv-sonarr",
			"tvImportedCategory":"tv-done",
			"recentTvPriority":1,
			"olderTvPriority":0,
			"intialState":2
		}`,
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	rt, ok := p.(*utorrent.UTorrent)
	if !ok {
		t.Fatalf("Construct: %T, want *utorrent.UTorrent", p)
	}
	got := rt.Settings()
	if got.Host != "utor.example" || got.Port != 8443 || !got.UseSsl || got.UrlBase != "/utorrent" {
		t.Errorf("transport: %+v", got)
	}
	if got.Username != "sonarr" || got.Password != "hunter2" {
		t.Errorf("auth: %+v", got)
	}
	if got.TvCategory != "tv-sonarr" || got.TvImportedCategory != "tv-done" {
		t.Errorf("category: %+v", got)
	}
	if got.RecentTvPriority != int(utorrent.PriorityFirst) || got.OlderTvPriority != int(utorrent.PriorityLast) {
		t.Errorf("priority: %+v", got)
	}
	if got.IntialState != int(utorrent.StatePause) {
		t.Errorf("initial state: %d, want StatePause=2", got.IntialState)
	}
}

// ============================================================================
// Provider identity + stubs — slice 5.J46.
// ============================================================================

func TestProvider_NameAndProtocol(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil)
	if u.Name() != "uTorrent" {
		t.Errorf("Name: %q, want uTorrent (lowercase u)", u.Name())
	}
	if u.Protocol() != indexers.DownloadProtocolTorrent {
		t.Errorf("Protocol: %v, want torrent", u.Protocol())
	}
}

func TestProvider_StubsReturnNotImplementedYet(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil)
	ctx := context.Background()
	if _, err := u.Download(ctx, &downloadclients.GrabRequest{}); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("Download: %v, want ErrNotImplementedYet", err)
	}
	if _, err := u.GetItems(ctx); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetItems: %v, want ErrNotImplementedYet", err)
	}
	if err := u.RemoveItem(ctx, &downloadclients.Item{}, false); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("RemoveItem: %v, want ErrNotImplementedYet", err)
	}
	if _, err := u.GetStatus(ctx); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("GetStatus: %v, want ErrNotImplementedYet", err)
	}
	if err := u.MarkItemAsImported(ctx, &downloadclients.Item{}); !errors.Is(err, downloadclients.ErrNotImplementedYet) {
		t.Errorf("MarkItemAsImported: %v, want ErrNotImplementedYet", err)
	}
}

func TestProvider_GetImportItemPassthrough(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil)
	in := &downloadclients.Item{DownloadID: "abc"}
	got, err := u.GetImportItem(context.Background(), in, nil)
	if err != nil {
		t.Fatalf("GetImportItem: %v", err)
	}
	if got != in {
		t.Errorf("GetImportItem: %+v, want passthrough of in", got)
	}
}

func TestProvider_RequestActionReturnsNilNil(t *testing.T) {
	t.Parallel()
	u := utorrent.New(nil, utorrent.NewSettings(), nil)
	out, err := u.RequestAction(context.Background(), "getCategories", nil)
	if err != nil || out != nil {
		t.Errorf("RequestAction: got (%v, %v), want (nil, nil)", out, err)
	}
}

func TestProvider_TestSurfacesSettingsFailures(t *testing.T) {
	t.Parallel()
	s := utorrent.NewSettings()
	s.Host = ""
	u := utorrent.New(nil, s, nil)
	failures, err := u.Test(context.Background())
	if err != nil {
		t.Errorf("Test: %v", err)
	}
	if !hasFailure(failures, "Host") {
		t.Errorf("Test: %+v, want Host failure surfaced", failures)
	}
}

// ============================================================================
// Registry registration — slice 5.J46.
// ============================================================================

func TestRegistry_UTorrentRegistered(t *testing.T) {
	t.Parallel()
	if !downloadclients.Default().Has(utorrent.Implementation, utorrent.ConfigContract) {
		t.Fatalf("registry has no entry for (%s, %s)", utorrent.Implementation, utorrent.ConfigContract)
	}
}

func TestRegistry_ConstructBadJSON(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: utorrent.Implementation,
		ConfigContract: utorrent.ConfigContract,
		Settings:       `{bad json`,
	}
	if _, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{}); err == nil {
		t.Errorf("Construct(bad JSON): want error")
	}
}

func TestRegistry_DefaultsUsedWhenSettingsEmpty(t *testing.T) {
	t.Parallel()
	def := &downloadclients.DownloadClient{
		Implementation: utorrent.Implementation,
		ConfigContract: utorrent.ConfigContract,
		Settings:       "",
	}
	p, err := downloadclients.Default().Construct(def, downloadclients.Dependencies{})
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	got := p.(*utorrent.UTorrent).Settings()
	if got.Host != "localhost" || got.Port != 8080 {
		t.Errorf("default Settings: %+v, want NewSettings defaults", got)
	}
}

// ============================================================================
// Priority + State String round-trips — slice 5.J46.
// ============================================================================

func TestPriority_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		p    utorrent.Priority
		want string
	}{
		{utorrent.PriorityLast, "Last"},
		{utorrent.PriorityFirst, "First"},
	}
	for _, tc := range cases {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", int(tc.p), got, tc.want)
		}
	}
}

func TestState_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    utorrent.State
		want string
	}{
		{utorrent.StateStart, "Start"},
		{utorrent.StateForceStart, "ForceStart"},
		{utorrent.StatePause, "Pause"},
		{utorrent.StateStop, "Stop"},
	}
	for _, tc := range cases {
		if got := tc.s.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", int(tc.s), got, tc.want)
		}
	}
}

func hasFailure(failures []downloadclients.TestFailure, prop string) bool {
	for _, f := range failures {
		if f.PropertyName == prop {
			return true
		}
	}
	return false
}
