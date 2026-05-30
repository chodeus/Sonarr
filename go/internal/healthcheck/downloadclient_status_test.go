package healthcheck_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclientstatus"
	"sonarr.tv/sonarr/internal/healthcheck"
)

type stubEnabledDownloadClientLister struct {
	enabled []healthcheck.EnabledDownloadClient
	err     error
}

func (s stubEnabledDownloadClientLister) EnabledDownloadClientsForStatus(_ context.Context) ([]healthcheck.EnabledDownloadClient, error) {
	return s.enabled, s.err
}

type stubDownloadClientStatusBlockedLister struct {
	blocked []downloadclientstatus.Status
	err     error
}

func (s stubDownloadClientStatusBlockedLister) GetBlockedProviders(_ context.Context) ([]downloadclientstatus.Status, error) {
	return s.blocked, s.err
}

func makeDownloadClientStatusCheck(t *testing.T, enabled []healthcheck.EnabledDownloadClient, blocked []downloadclientstatus.Status) *healthcheck.DownloadClientStatusCheck {
	t.Helper()
	c, err := healthcheck.NewDownloadClientStatusCheck(
		stubEnabledDownloadClientLister{enabled: enabled},
		stubDownloadClientStatusBlockedLister{blocked: blocked},
	)
	if err != nil {
		t.Fatalf("NewDownloadClientStatusCheck: %v", err)
	}
	return c
}

func TestDownloadClientStatusCheck_NewRejectsNil(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewDownloadClientStatusCheck(nil, stubDownloadClientStatusBlockedLister{}); err == nil {
		t.Error("expected error on nil clients lister")
	}
	if _, err := healthcheck.NewDownloadClientStatusCheck(stubEnabledDownloadClientLister{}, nil); err == nil {
		t.Error("expected error on nil statuses lister")
	}
}

func TestDownloadClientStatusCheck_NoEnabled_OK(t *testing.T) {
	t.Parallel()
	got := makeDownloadClientStatusCheck(t, nil, nil).Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on no enabled clients, got %v: %s", got.Type, got.Message)
	}
}

func TestDownloadClientStatusCheck_AllHealthy_OK(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledDownloadClient{{ID: 1, Name: "qBittorrent"}, {ID: 2, Name: "SABnzbd"}}
	got := makeDownloadClientStatusCheck(t, enabled, nil).Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on all-healthy, got %v: %s", got.Type, got.Message)
	}
}

func TestDownloadClientStatusCheck_SomeBlocked_Warning(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledDownloadClient{{ID: 1, Name: "qBittorrent"}, {ID: 2, Name: "SABnzbd"}}
	blocked := []downloadclientstatus.Status{{ProviderID: 2}}
	got := makeDownloadClientStatusCheck(t, enabled, blocked).Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("expected Warning on some-blocked, got %v: %s", got.Type, got.Message)
	}
	if got.Reason != healthcheck.ReasonDownloadClientStatusSingleClient {
		t.Errorf("reason = %v, want SingleClient", got.Reason)
	}
	if !strings.Contains(got.Message, "SABnzbd") || strings.Contains(got.Message, "qBittorrent") {
		t.Errorf("message should name only the blocked client SABnzbd: %q", got.Message)
	}
}

func TestDownloadClientStatusCheck_AllBlocked_Error(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledDownloadClient{{ID: 1, Name: "qBittorrent"}, {ID: 2, Name: "SABnzbd"}}
	blocked := []downloadclientstatus.Status{{ProviderID: 1}, {ProviderID: 2}}
	got := makeDownloadClientStatusCheck(t, enabled, blocked).Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Fatalf("expected Error on all-blocked, got %v: %s", got.Type, got.Message)
	}
	if got.Reason != healthcheck.ReasonDownloadClientStatusAllClients {
		t.Errorf("reason = %v, want AllClients", got.Reason)
	}
}

func TestDownloadClientStatusCheck_NamesSorted(t *testing.T) {
	t.Parallel()
	enabled := []healthcheck.EnabledDownloadClient{
		{ID: 1, Name: "zeta"}, {ID: 2, Name: "alpha"}, {ID: 3, Name: "mike"}, {ID: 4, Name: "healthy"},
	}
	blocked := []downloadclientstatus.Status{{ProviderID: 1}, {ProviderID: 2}, {ProviderID: 3}}
	got := makeDownloadClientStatusCheck(t, enabled, blocked).Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("expected Warning, got %v", got.Type)
	}
	if !strings.Contains(got.Message, "alpha, mike, zeta") {
		t.Errorf("names not sorted case-insensitively: %q", got.Message)
	}
}

func TestDownloadClientStatusCheck_ListerErrorsWarn(t *testing.T) {
	t.Parallel()
	c1, _ := healthcheck.NewDownloadClientStatusCheck(
		stubEnabledDownloadClientLister{err: errors.New("boom")},
		stubDownloadClientStatusBlockedLister{},
	)
	if got := c1.Run(t.Context()); got.Type != healthcheck.ResultWarning {
		t.Errorf("clients lister error → Warning, got %v", got.Type)
	}
	c2, _ := healthcheck.NewDownloadClientStatusCheck(
		stubEnabledDownloadClientLister{enabled: []healthcheck.EnabledDownloadClient{{ID: 1, Name: "x"}}},
		stubDownloadClientStatusBlockedLister{err: errors.New("boom")},
	)
	if got := c2.Run(t.Context()); got.Type != healthcheck.ResultWarning {
		t.Errorf("statuses lister error → Warning, got %v", got.Type)
	}
}

func TestDownloadClientStatusCheck_Metadata(t *testing.T) {
	t.Parallel()
	c := makeDownloadClientStatusCheck(t, nil, nil)
	if c.Name() != healthcheck.DownloadClientStatusCheckName {
		t.Errorf("Name() = %q", c.Name())
	}
	if !c.CheckOnStartup() || !c.CheckOnSchedule() {
		t.Error("expected CheckOnStartup + CheckOnSchedule true")
	}
}
