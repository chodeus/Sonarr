package healthcheck_test

import (
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/healthcheck"
)

// fakeImportMechCfg satisfies healthcheck.ImportMechanismCheckConfig.
type fakeImportMechCfg struct {
	defined bool
	enabled bool
}

func (f fakeImportMechCfg) IsDefined(_ string) bool               { return f.defined }
func (f fakeImportMechCfg) EnableCompletedDownloadHandling() bool { return f.enabled }

func TestImportMechanismCheck_NewRejectsNils(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewImportMechanismCheck(nil, fakeDCStatusEnumerator{}, nil); err == nil {
		t.Errorf("nil cfg should error")
	}
	if _, err := healthcheck.NewImportMechanismCheck(fakeImportMechCfg{}, nil, nil); err == nil {
		t.Errorf("nil enumerator should error")
	}
}

func TestImportMechanismCheck_OKWhenDefinedAndEnabled(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewImportMechanismCheck(
		fakeImportMechCfg{defined: true, enabled: true},
		fakeDCStatusEnumerator{},
		quietLogger(),
	)
	if got := chk.Run(t.Context()); got.Type != healthcheck.ResultOK {
		t.Errorf("type = %v, want OK", got.Type)
	}
}

func TestImportMechanismCheck_WarnsWhenDefinedAndDisabled(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewImportMechanismCheck(
		fakeImportMechCfg{defined: true, enabled: false},
		fakeDCStatusEnumerator{},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v, want Warning", got.Type)
	}
	if got.Reason != healthcheck.ReasonImportMechanismHandlingDisabled {
		t.Errorf("reason = %v", got.Reason)
	}
}

func TestImportMechanismCheck_WarnsRemoteWhenUndefined(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "RemoteSab", Implementation: "Sabnzbd",
			Status: &downloadclients.ClientInfo{IsLocalhost: false}},
	}
	chk, _ := healthcheck.NewImportMechanismCheck(
		fakeImportMechCfg{defined: false},
		fakeDCStatusEnumerator{rows: rows},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonImportMechanismEnableCompletedDownloadHandlingIfPossibleMultiComputer {
		t.Errorf("reason = %v, want MultiComputer", got.Reason)
	}
}

func TestImportMechanismCheck_WarnsSabSpecific(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "LocalSab", Implementation: "Sabnzbd",
			Status: &downloadclients.ClientInfo{IsLocalhost: true}},
	}
	chk, _ := healthcheck.NewImportMechanismCheck(
		fakeImportMechCfg{defined: false},
		fakeDCStatusEnumerator{rows: rows},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v", got.Type)
	}
	if !strings.Contains(got.Message, "Sabnzbd") {
		t.Errorf("Sab-only message should mention Sabnzbd: %q", got.Message)
	}
}

func TestImportMechanismCheck_WarnsNzbgetSpecific(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "LocalNzb", Implementation: "Nzbget",
			Status: &downloadclients.ClientInfo{IsLocalhost: true}},
	}
	chk, _ := healthcheck.NewImportMechanismCheck(
		fakeImportMechCfg{defined: false},
		fakeDCStatusEnumerator{rows: rows},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if !strings.Contains(got.Message, "Nzbget") {
		t.Errorf("Nzbget-only message should mention Nzbget: %q", got.Message)
	}
}

func TestImportMechanismCheck_WarnsGenericWhenMixed(t *testing.T) {
	t.Parallel()
	rows := []healthcheck.EnumeratedDownloadClient{
		{Name: "LocalSab", Implementation: "Sabnzbd",
			Status: &downloadclients.ClientInfo{IsLocalhost: true}},
		{Name: "LocalQbit", Implementation: "QBittorrent",
			Status: &downloadclients.ClientInfo{IsLocalhost: true}},
	}
	chk, _ := healthcheck.NewImportMechanismCheck(
		fakeImportMechCfg{defined: false},
		fakeDCStatusEnumerator{rows: rows},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Fatalf("type = %v", got.Type)
	}
	// Mixed family → generic message (no Sabnzbd / Nzbget suffix).
	if strings.Contains(got.Message, "Sabnzbd") || strings.Contains(got.Message, "Nzbget") {
		t.Errorf("mixed-family message should be generic: %q", got.Message)
	}
}

func TestImportMechanismCheck_GenericWhenUndefinedNoClients(t *testing.T) {
	t.Parallel()
	chk, _ := healthcheck.NewImportMechanismCheck(
		fakeImportMechCfg{defined: false},
		fakeDCStatusEnumerator{},
		quietLogger(),
	)
	got := chk.Run(t.Context())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("type = %v, want Warning (no clients + undefined)", got.Type)
	}
}
