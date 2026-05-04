package providerstatus

import (
	"testing"
	"time"
)

// Pin the table values + length parity between the duration form and
// the seconds form. Both are referenced directly by the per-type
// services at different call-sites and must agree byte-for-byte with
// the C# constant.
func TestBackOffPeriodsMatchC(t *testing.T) {
	wantSeconds := []int{0, 60, 300, 900, 1800, 3600, 10800, 21600, 43200, 86400}
	if len(BackOffPeriodSeconds) != len(wantSeconds) {
		t.Fatalf("BackOffPeriodSeconds length: got %d want %d", len(BackOffPeriodSeconds), len(wantSeconds))
	}
	for i, want := range wantSeconds {
		if BackOffPeriodSeconds[i] != want {
			t.Errorf("BackOffPeriodSeconds[%d] = %d, want %d", i, BackOffPeriodSeconds[i], want)
		}
	}
	if len(BackOffPeriods) != len(BackOffPeriodSeconds) {
		t.Fatalf("BackOffPeriods/Seconds length mismatch")
	}
	for i, d := range BackOffPeriods {
		if d != time.Duration(BackOffPeriodSeconds[i])*time.Second {
			t.Errorf("BackOffPeriods[%d] = %v, want %ds", i, d, BackOffPeriodSeconds[i])
		}
	}
}

func TestMaxEscalationLevelMatchesTable(t *testing.T) {
	if MaxEscalationLevel != len(BackOffPeriods)-1 {
		t.Fatalf("MaxEscalationLevel: got %d want %d", MaxEscalationLevel, len(BackOffPeriods)-1)
	}
	if MaxEscalationLevel != 9 {
		t.Fatalf("MaxEscalationLevel: got %d want 9 (matches C# default at ProviderStatusServiceBase.cs:31)", MaxEscalationLevel)
	}
}
