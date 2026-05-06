package healthcheck_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/healthcheck"
)

// fixedFetcher returns a fixed time / error.
func fixedFetcher(t time.Time, err error) healthcheck.SystemTimeFetcher {
	return func(_ context.Context) (time.Time, error) { return t, err }
}

// fixedClock returns a fixed local "now".
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func makeSystemTimeCheck(t *testing.T, f healthcheck.SystemTimeFetcher, now func() time.Time) *healthcheck.SystemTimeCheck {
	t.Helper()
	c, err := healthcheck.NewSystemTimeCheck(f, now, slog.Default())
	if err != nil {
		t.Fatalf("NewSystemTimeCheck: %v", err)
	}
	return c
}

func TestSystemTimeCheck_NewRejectsNilFetcher(t *testing.T) {
	t.Parallel()
	if _, err := healthcheck.NewSystemTimeCheck(nil, time.Now, slog.Default()); err == nil {
		t.Errorf("expected error on nil fetcher")
	}
}

func TestSystemTimeCheck_DefaultsNowAndLog(t *testing.T) {
	t.Parallel()
	// nil now + nil log should be acceptable — defaults wire to
	// time.Now.UTC and slog.Default. Construction success suffices.
	c, err := healthcheck.NewSystemTimeCheck(fixedFetcher(time.Now().UTC(), nil), nil, nil)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if c.Name() != healthcheck.SystemTimeCheckName {
		t.Errorf("Name = %q", c.Name())
	}
}

func TestSystemTimeCheck_FetcherErrorReturnsOK(t *testing.T) {
	t.Parallel()
	// Mirrors the C# `catch (Exception e)` block — error from the
	// upstream `/time` call is swallowed; the check stays passing.
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	c := makeSystemTimeCheck(t, fixedFetcher(time.Time{}, errors.New("connection refused")), fixedClock(now))
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK on fetcher error, got %v: %s", got.Type, got.Message)
	}
}

func TestSystemTimeCheck_InSyncReturnsOK(t *testing.T) {
	t.Parallel()
	// Drift well below the 24h threshold — passes.
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	upstream := now.Add(2 * time.Hour) // +2h drift, below 24h
	c := makeSystemTimeCheck(t, fixedFetcher(upstream, nil), fixedClock(now))
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK at +2h drift, got %v: %s", got.Type, got.Message)
	}
}

func TestSystemTimeCheck_JustUnderThresholdReturnsOK(t *testing.T) {
	t.Parallel()
	// Mirrors the C# `>= 1` strict-greater-or-equal predicate —
	// anything strictly under 24h passes.
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	upstream := now.Add(23*time.Hour + 59*time.Minute)
	c := makeSystemTimeCheck(t, fixedFetcher(upstream, nil), fixedClock(now))
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultOK {
		t.Errorf("expected OK at 23h59m drift, got %v: %s", got.Type, got.Message)
	}
}

func TestSystemTimeCheck_PositiveDriftReturnsError(t *testing.T) {
	t.Parallel()
	// Local clock 2 days behind upstream → Error.
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	upstream := now.Add(48 * time.Hour)
	c := makeSystemTimeCheck(t, fixedFetcher(upstream, nil), fixedClock(now))
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error at +48h drift, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonSystemTime {
		t.Errorf("Reason = %v, want ReasonSystemTime", got.Reason)
	}
	if !strings.Contains(got.WikiURL, "system-time-off") {
		t.Errorf("WikiURL %q should include #system-time-off fragment", got.WikiURL)
	}
}

func TestSystemTimeCheck_NegativeDriftReturnsError(t *testing.T) {
	t.Parallel()
	// Local clock 2 days ahead of upstream — symmetric Error.
	// Mirrors the C# `Math.Abs(...).TotalDays >= 1` predicate.
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	upstream := now.Add(-48 * time.Hour)
	c := makeSystemTimeCheck(t, fixedFetcher(upstream, nil), fixedClock(now))
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error at -48h drift, got %v", got.Type)
	}
	if got.Reason != healthcheck.ReasonSystemTime {
		t.Errorf("Reason = %v, want ReasonSystemTime", got.Reason)
	}
}

func TestSystemTimeCheck_ExactlyThresholdReturnsError(t *testing.T) {
	t.Parallel()
	// Mirrors the C# `>= 1` at the boundary.
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	upstream := now.Add(24 * time.Hour)
	c := makeSystemTimeCheck(t, fixedFetcher(upstream, nil), fixedClock(now))
	got := c.Run(t.Context())
	if got.Type != healthcheck.ResultError {
		t.Errorf("expected Error at exactly 24h drift, got %v", got.Type)
	}
}

func TestSystemTimeCheck_CheckOnStartupAndScheduleDefaults(t *testing.T) {
	t.Parallel()
	c := makeSystemTimeCheck(t, fixedFetcher(time.Now().UTC(), nil), time.Now)
	if !c.CheckOnStartup() {
		t.Errorf("CheckOnStartup should default true")
	}
	if !c.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule should default true")
	}
}

func TestSystemTimeCheck_EventTriggersEmpty(t *testing.T) {
	t.Parallel()
	// C# class declares no [CheckOn] attributes — trigger list is empty.
	c := makeSystemTimeCheck(t, fixedFetcher(time.Now().UTC(), nil), time.Now)
	if got := c.EventTriggers(); len(got) != 0 {
		t.Errorf("expected no triggers, got %v", got)
	}
}

func TestSystemTimeCheck_NameAndInterfaceShape(t *testing.T) {
	t.Parallel()
	c := makeSystemTimeCheck(t, fixedFetcher(time.Now().UTC(), nil), time.Now)
	if c.Name() != healthcheck.SystemTimeCheckName {
		t.Errorf("Name = %q", c.Name())
	}
	var iface healthcheck.Check = c
	_ = iface
	var iface2 healthcheck.EventDrivenCheck = c
	_ = iface2
}
