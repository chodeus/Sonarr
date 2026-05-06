package healthcheck_test

import (
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
)

// stubRecyclingBinConfig satisfies RecyclingBinCheckConfig.
type stubRecyclingBinConfig struct {
	bin string
}

func (s stubRecyclingBinConfig) RecycleBin() string { return s.bin }

// stubRecyclingBinDisk satisfies RecyclingBinDiskProvider with a
// fixed answer. Captures the path it was queried with so tests can
// assert the check passes the configured folder through unchanged.
type stubRecyclingBinDisk struct {
	writable bool
	calls    []string
}

func (s *stubRecyclingBinDisk) FolderWritable(path string) bool {
	s.calls = append(s.calls, path)
	return s.writable
}

func makeRecyclingBinCheck(t *testing.T, cfg healthcheck.RecyclingBinCheckConfig, disk healthcheck.RecyclingBinDiskProvider) *healthcheck.RecyclingBinCheck {
	t.Helper()
	c, err := healthcheck.NewRecyclingBinCheck(cfg, disk)
	if err != nil {
		t.Fatalf("NewRecyclingBinCheck: %v", err)
	}
	return c
}

func TestRecyclingBinCheck_NewRejectsNilConfig(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewRecyclingBinCheck(nil, &stubRecyclingBinDisk{writable: true}); err == nil {
		t.Errorf("expected error on nil config")
	}
}

func TestRecyclingBinCheck_NewRejectsNilDisk(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewRecyclingBinCheck(stubRecyclingBinConfig{}, nil); err == nil {
		t.Errorf("expected error on nil disk")
	}
}

func TestRecyclingBinCheck_EmptyPathReturnsOK(t *testing.T) {
	t.Parallel()
	disk := &stubRecyclingBinDisk{writable: false}
	c := makeRecyclingBinCheck(t, stubRecyclingBinConfig{bin: ""}, disk)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on empty path, got %v: %s", got.Type, got.Message)
	}
	if len(disk.calls) != 0 {
		t.Errorf("disk should not be probed on empty path, got calls=%v", disk.calls)
	}
}

func TestRecyclingBinCheck_WhitespacePathReturnsOK(t *testing.T) {
	t.Parallel()
	// Mirrors the C# `IsNullOrWhiteSpace` short-circuit — pure
	// whitespace is treated identically to empty string.
	disk := &stubRecyclingBinDisk{writable: false}
	c := makeRecyclingBinCheck(t, stubRecyclingBinConfig{bin: "   \t\n"}, disk)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on whitespace-only path, got %v", got.Type)
	}
	if len(disk.calls) != 0 {
		t.Errorf("disk should not be probed on whitespace-only path, got calls=%v", disk.calls)
	}
}

func TestRecyclingBinCheck_WritableFolderReturnsOK(t *testing.T) {
	t.Parallel()
	disk := &stubRecyclingBinDisk{writable: true}
	c := makeRecyclingBinCheck(t, stubRecyclingBinConfig{bin: "/data/recycle"}, disk)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on writable folder, got %v: %s", got.Type, got.Message)
	}
	if len(disk.calls) != 1 || disk.calls[0] != "/data/recycle" {
		t.Errorf("expected single FolderWritable call with configured path, got %v", disk.calls)
	}
}

func TestRecyclingBinCheck_NonWritableFolderReturnsError(t *testing.T) {
	t.Parallel()
	disk := &stubRecyclingBinDisk{writable: false}
	c := makeRecyclingBinCheck(t, stubRecyclingBinConfig{bin: "/data/recycle"}, disk)
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error on non-writable folder, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonRecycleBinUnableToWrite {
		t.Errorf("Reason = %v, want ReasonRecycleBinUnableToWrite", got.Reason)
	}
	if !strings.Contains(got.Message, "/data/recycle") {
		t.Errorf("Message %q should mention configured path", got.Message)
	}
	if !strings.Contains(got.WikiURL, "cannot-write-recycle-bin") {
		t.Errorf("WikiURL %q should include #cannot-write-recycle-bin fragment", got.WikiURL)
	}
}

func TestRecyclingBinCheck_CheckOnStartupAndScheduleDefaults(t *testing.T) {
	t.Parallel()
	c := makeRecyclingBinCheck(t, stubRecyclingBinConfig{}, &stubRecyclingBinDisk{writable: true})
	if !c.CheckOnStartup() {
		t.Errorf("CheckOnStartup should default true")
	}
	if !c.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule should default true")
	}
}

func TestRecyclingBinCheck_EventTriggersEmpty(t *testing.T) {
	t.Parallel()
	// EpisodeImportedEvent + EpisodeImportFailedEvent aren't ported
	// yet; the check returns no triggers in the meantime. The check
	// still runs on startup + scheduled batches.
	c := makeRecyclingBinCheck(t, stubRecyclingBinConfig{}, &stubRecyclingBinDisk{writable: true})
	if got := c.EventTriggers(); len(got) != 0 {
		t.Errorf("expected no triggers until import events are ported, got %v", got)
	}
}

func TestRecyclingBinCheck_NameAndInterfaceShape(t *testing.T) {
	t.Parallel()
	c := makeRecyclingBinCheck(t, stubRecyclingBinConfig{}, &stubRecyclingBinDisk{writable: true})
	if c.Name() != healthcheck.RecyclingBinCheckName {
		t.Errorf("Name = %q", c.Name())
	}
	var iface healthcheck.Check = c
	_ = iface
	var iface2 healthcheck.EventDrivenCheck = c
	_ = iface2
}
