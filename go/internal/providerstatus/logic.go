package providerstatus

import "time"

// Tunables collects the per-service knobs the C# class exposes as
// protected `MaxEscalationLevel` / `MinimumTimeSinceInitialFailure`
// / `MinimumTimeSinceStartup` properties at
// `ProviderStatusServiceBase.cs:31-33`. The C# defaults (max=9,
// startup=15min, initial=0) are the values [DefaultTunables]
// returns; per-type services override before passing into
// [ApplyFailure].
//
// `RuntimeStart` mirrors `_runtimeInfo.StartTime` — read once at
// process start, used to detect "we just booted, don't escalate
// aggressively yet". Each per-package Service threads it through.
type Tunables struct {
	MaxEscalationLevel         int
	MinTimeSinceStartup        time.Duration
	MinTimeSinceInitialFailure time.Duration
	RuntimeStart               time.Time
}

// DefaultTunables returns the C# defaults from
// `ProviderStatusServiceBase.cs:31-33`.
func DefaultTunables(runtimeStart time.Time) Tunables {
	return Tunables{
		MaxEscalationLevel:         MaxEscalationLevel,
		MinTimeSinceStartup:        15 * time.Minute,
		MinTimeSinceInitialFailure: 0,
		RuntimeStart:               runtimeStart,
	}
}

// State is the slice of [ProviderStatusBase] fields the back-off
// logic mutates. The 4 per-type Status structs each carry these
// fields directly (not via embedding); per-package Services marshal
// to/from this struct around the [ApplySuccess] / [ApplyFailure]
// calls.
type State struct {
	InitialFailure    *time.Time
	MostRecentFailure *time.Time
	DisabledTill      *time.Time
	EscalationLevel   int
}

// BackOffFor clamps `level` to [MaxEscalationLevel] and returns the
// disable duration at that tier. Mirrors
// `ProviderStatusServiceBase.CalculateBackOffPeriod` at `:53-58`.
func BackOffFor(level int) time.Duration {
	if level < 0 {
		level = 0
	}
	if level > MaxEscalationLevel {
		level = MaxEscalationLevel
	}
	return BackOffPeriods[level]
}

// ApplySuccess mirrors `ProviderStatusServiceBase.RecordSuccess` at
// `:60-83` — decrements EscalationLevel if non-zero and clears
// DisabledTill. Returns true when the state changed (caller should
// persist + publish); false means EscalationLevel was already 0 and
// nothing happened. The C# version returns void from RecordSuccess
// but explicitly short-circuits at `:71-74` when EscalationLevel ==
// 0; the Go bool return surfaces that branch to the per-package
// service layer.
func ApplySuccess(s *State) bool {
	if s.EscalationLevel == 0 {
		return false
	}
	s.EscalationLevel--
	s.DisabledTill = nil
	return true
}

// ApplyFailure mirrors `ProviderStatusServiceBase.RecordFailure(int,
// TimeSpan, bool)` at `:85-140`. The full decision matrix is
// preserved verbatim:
//
//  1. MostRecentFailure := now.
//  2. If EscalationLevel == 0: this is the first failure; record
//     InitialFailure = now and seed EscalationLevel = 1. Force
//     escalate=false because step 4 already did the work.
//  3. Compute startup-grace window (RuntimeStart +
//     MinTimeSinceStartup > now) and initial-grace window
//     (InitialFailure + MinTimeSinceInitialFailure > now).
//  4. If escalate AND outside both grace windows: bump
//     EscalationLevel by one (clamped to MaxEscalationLevel).
//  5. If a minimum back-off was requested: bump EscalationLevel
//     until BackOffFor(level) >= minimumBackOff (or MaxEscalation
//     reached).
//  6. Set DisabledTill = now + BackOffFor(level), unless we're
//     inside the initial-grace window AND no minimum back-off was
//     requested (the grace branch keeps DisabledTill at its
//     existing value — which on first failure means it stays nil).
//  7. If we're inside the startup-grace window AND no minimum
//     back-off was requested AND DisabledTill is set: cap
//     DisabledTill at now + BackOffPeriods[2] (15 minutes). This
//     prevents a flurry of startup-time failures from disabling a
//     provider for hours just because the host was slow to settle.
//
// `now` is injected by the caller — typically `time.Now().UTC()`.
// Tests pin a fixed time. The function is a pure mutator; no
// persistence, no logging.
func ApplyFailure(s *State, now time.Time, minimumBackOff time.Duration, escalate bool, t Tunables) {
	nowCopy := now
	s.MostRecentFailure = &nowCopy

	if s.EscalationLevel == 0 {
		s.InitialFailure = &nowCopy
		s.EscalationLevel = 1
		escalate = false
	}

	inStartupGracePeriod := t.RuntimeStart.Add(t.MinTimeSinceStartup).After(now)
	// InitialFailure is non-nil here: either it was set above, or it
	// was already set on a prior failure.
	inGracePeriod := s.InitialFailure.Add(t.MinTimeSinceInitialFailure).After(now)

	if escalate && !inGracePeriod && !inStartupGracePeriod {
		next := s.EscalationLevel + 1
		if next > t.MaxEscalationLevel {
			next = t.MaxEscalationLevel
		}
		s.EscalationLevel = next
	}

	if minimumBackOff != 0 {
		for s.EscalationLevel < t.MaxEscalationLevel && BackOffFor(s.EscalationLevel) < minimumBackOff {
			s.EscalationLevel++
		}
	}

	if !inGracePeriod || minimumBackOff != 0 {
		until := now.Add(BackOffFor(s.EscalationLevel))
		s.DisabledTill = &until
	}

	if inStartupGracePeriod && minimumBackOff == 0 && s.DisabledTill != nil {
		maxUntil := now.Add(BackOffPeriods[2])
		if maxUntil.Before(*s.DisabledTill) {
			s.DisabledTill = &maxUntil
		}
	}
}
