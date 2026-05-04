package providerstatus

import (
	"testing"
	"time"
)

// Reference instant used as "now" in every test below. Picked to be
// well past any plausible RuntimeStart so the default tests are
// outside the startup-grace window.
var refNow = time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)

// runtimeLongAgo returns a Tunables whose RuntimeStart is far enough
// in the past that the startup-grace window has lapsed.
func runtimeLongAgo() Tunables {
	t := DefaultTunables(refNow.Add(-1 * time.Hour))
	return t
}

// runtimeJustNow returns a Tunables whose RuntimeStart is recent
// enough that we're still inside the 15-minute startup-grace
// window.
func runtimeJustNow() Tunables {
	t := DefaultTunables(refNow.Add(-1 * time.Minute))
	return t
}

func TestApplySuccess_NoChangeWhenZero(t *testing.T) {
	s := &State{}
	if ApplySuccess(s) {
		t.Errorf("ApplySuccess(level=0): got true, want false")
	}
	if s.EscalationLevel != 0 {
		t.Errorf("EscalationLevel changed: %d", s.EscalationLevel)
	}
}

func TestApplySuccess_DecrementsAndClearsDisabled(t *testing.T) {
	disabled := refNow.Add(time.Hour)
	s := &State{EscalationLevel: 3, DisabledTill: &disabled}
	if !ApplySuccess(s) {
		t.Errorf("ApplySuccess(level=3): got false, want true")
	}
	if s.EscalationLevel != 2 {
		t.Errorf("EscalationLevel: %d, want 2", s.EscalationLevel)
	}
	if s.DisabledTill != nil {
		t.Errorf("DisabledTill: %v, want nil", s.DisabledTill)
	}
}

// First failure on a fresh provider: InitialFailure + MostRecentFailure
// are seeded to now, EscalationLevel goes 0→1, escalate=true is forced
// to false (the bump from 0 to 1 already covers it). Mirrors C#
// `:99-104`.
func TestApplyFailure_FirstFailureSeedsAndDoesNotEscalate(t *testing.T) {
	s := &State{}
	ApplyFailure(s, refNow, 0, true, runtimeLongAgo())
	if s.EscalationLevel != 1 {
		t.Errorf("EscalationLevel: %d, want 1", s.EscalationLevel)
	}
	if s.InitialFailure == nil || !s.InitialFailure.Equal(refNow) {
		t.Errorf("InitialFailure: %v", s.InitialFailure)
	}
	if s.MostRecentFailure == nil || !s.MostRecentFailure.Equal(refNow) {
		t.Errorf("MostRecentFailure: %v", s.MostRecentFailure)
	}
	if s.DisabledTill == nil {
		t.Errorf("DisabledTill should be set")
	} else if !s.DisabledTill.Equal(refNow.Add(BackOffPeriods[1])) {
		t.Errorf("DisabledTill: %v, want %v", s.DisabledTill, refNow.Add(BackOffPeriods[1]))
	}
}

// Subsequent failure with escalate=true outside grace bumps level by 1.
func TestApplyFailure_SubsequentEscalates(t *testing.T) {
	initial := refNow.Add(-1 * time.Hour)
	s := &State{InitialFailure: &initial, EscalationLevel: 1}
	ApplyFailure(s, refNow, 0, true, runtimeLongAgo())
	if s.EscalationLevel != 2 {
		t.Errorf("EscalationLevel: %d, want 2 (1→2 escalate)", s.EscalationLevel)
	}
}

// Subsequent failure with escalate=false (RecordConnectionFailure
// path) does NOT bump the level but still refreshes DisabledTill.
func TestApplyFailure_ConnectionFailureDoesNotEscalate(t *testing.T) {
	initial := refNow.Add(-1 * time.Hour)
	s := &State{InitialFailure: &initial, EscalationLevel: 3}
	ApplyFailure(s, refNow, 0, false, runtimeLongAgo())
	if s.EscalationLevel != 3 {
		t.Errorf("EscalationLevel: %d, want 3 (no bump)", s.EscalationLevel)
	}
	if s.DisabledTill == nil || !s.DisabledTill.Equal(refNow.Add(BackOffPeriods[3])) {
		t.Errorf("DisabledTill: %v, want %v", s.DisabledTill, refNow.Add(BackOffPeriods[3]))
	}
}

// Escalation is clamped at MaxEscalationLevel.
func TestApplyFailure_EscalationClampedAtMax(t *testing.T) {
	initial := refNow.Add(-1 * time.Hour)
	s := &State{InitialFailure: &initial, EscalationLevel: MaxEscalationLevel}
	ApplyFailure(s, refNow, 0, true, runtimeLongAgo())
	if s.EscalationLevel != MaxEscalationLevel {
		t.Errorf("EscalationLevel: %d, want %d (clamped)", s.EscalationLevel, MaxEscalationLevel)
	}
}

// Minimum back-off bumps EscalationLevel until BackOffFor >=
// minimumBackOff. Index 4 is 30min; 25min must bump up to level 4
// (30min back-off) starting from 1.
func TestApplyFailure_MinimumBackOffSeeksLevel(t *testing.T) {
	initial := refNow.Add(-1 * time.Hour)
	s := &State{InitialFailure: &initial, EscalationLevel: 1}
	ApplyFailure(s, refNow, 25*time.Minute, false, runtimeLongAgo())
	if s.EscalationLevel != 4 {
		t.Errorf("EscalationLevel: %d, want 4 (25min→30min tier)", s.EscalationLevel)
	}
	if !s.DisabledTill.Equal(refNow.Add(30 * time.Minute)) {
		t.Errorf("DisabledTill: %v, want +30m", s.DisabledTill)
	}
}

// Inside startup-grace, no minimum-back-off, DisabledTill is capped
// at now + BackOffPeriods[2] (15 minutes). Level 5 (1h back-off)
// would normally disable for 1h; the cap brings it down to 15min.
func TestApplyFailure_StartupGraceCapsDisabledTill(t *testing.T) {
	initial := refNow.Add(-1 * time.Hour)
	s := &State{InitialFailure: &initial, EscalationLevel: 5}
	ApplyFailure(s, refNow, 0, true, runtimeJustNow())
	if s.EscalationLevel != 5 {
		t.Errorf("escalate inside startup-grace: level changed to %d (should not bump)", s.EscalationLevel)
	}
	maxAllowed := refNow.Add(BackOffPeriods[2])
	if !s.DisabledTill.Equal(maxAllowed) {
		t.Errorf("DisabledTill: %v, want %v (15min cap)", s.DisabledTill, maxAllowed)
	}
}

// Inside startup-grace WITH a minimum back-off: the cap is bypassed
// (the if-condition at C#:127 requires minimumBackOff == 0).
func TestApplyFailure_StartupGraceCapBypassedByMinBackOff(t *testing.T) {
	initial := refNow.Add(-1 * time.Hour)
	s := &State{InitialFailure: &initial, EscalationLevel: 5}
	ApplyFailure(s, refNow, 2*time.Hour, false, runtimeJustNow())
	// Level seeks to ≥ 2h: level 6 = 3h.
	if s.EscalationLevel != 6 {
		t.Errorf("EscalationLevel: %d, want 6 (2h→3h tier)", s.EscalationLevel)
	}
	if !s.DisabledTill.Equal(refNow.Add(3 * time.Hour)) {
		t.Errorf("DisabledTill: %v, want +3h (cap bypassed)", s.DisabledTill)
	}
}

// Inside initial-grace AND no minimum back-off: DisabledTill is
// preserved (no overwrite). C#:122 condition: `if (!inGracePeriod ||
// minimumBackOff != 0)`.
func TestApplyFailure_InitialGracePreservesDisabledTill(t *testing.T) {
	tun := runtimeLongAgo()
	tun.MinTimeSinceInitialFailure = 1 * time.Hour
	initial := refNow.Add(-30 * time.Minute) // inside the 1h grace window
	original := refNow.Add(5 * time.Minute)
	s := &State{
		InitialFailure:  &initial,
		EscalationLevel: 1,
		DisabledTill:    &original,
	}
	ApplyFailure(s, refNow, 0, true, tun)
	// EscalationLevel preserved (escalate suppressed inside grace).
	if s.EscalationLevel != 1 {
		t.Errorf("EscalationLevel: %d, want 1 (no escalate inside initial-grace)", s.EscalationLevel)
	}
	// DisabledTill preserved.
	if s.DisabledTill == nil || !s.DisabledTill.Equal(original) {
		t.Errorf("DisabledTill: %v, want %v (preserved)", s.DisabledTill, original)
	}
}

// Inside initial-grace WITH a minimum back-off: DisabledTill IS
// overwritten because the second arm of the OR triggers.
func TestApplyFailure_InitialGraceWithMinBackOffOverwrites(t *testing.T) {
	tun := runtimeLongAgo()
	tun.MinTimeSinceInitialFailure = 1 * time.Hour
	initial := refNow.Add(-30 * time.Minute)
	s := &State{InitialFailure: &initial, EscalationLevel: 1}
	ApplyFailure(s, refNow, 5*time.Minute, false, tun)
	// Level 2 = 5min back-off — exactly meets minimumBackOff.
	if s.EscalationLevel != 2 {
		t.Errorf("EscalationLevel: %d, want 2 (5min tier)", s.EscalationLevel)
	}
	if s.DisabledTill == nil || !s.DisabledTill.Equal(refNow.Add(5*time.Minute)) {
		t.Errorf("DisabledTill: %v, want +5m", s.DisabledTill)
	}
}

func TestBackOffFor_ClampsAtBounds(t *testing.T) {
	if BackOffFor(-5) != 0 {
		t.Errorf("BackOffFor(-5): got %v, want 0", BackOffFor(-5))
	}
	if BackOffFor(100) != BackOffPeriods[MaxEscalationLevel] {
		t.Errorf("BackOffFor(100): got %v, want %v", BackOffFor(100), BackOffPeriods[MaxEscalationLevel])
	}
	if BackOffFor(3) != BackOffPeriods[3] {
		t.Errorf("BackOffFor(3): got %v, want %v", BackOffFor(3), BackOffPeriods[3])
	}
}
