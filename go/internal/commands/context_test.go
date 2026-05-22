package commands_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/commands"
)

func TestTriggerFromContext_DefaultsToUnspecified(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	if got := commands.TriggerFromContext(ctx); got != commands.TriggerUnspecified {
		t.Errorf("default Trigger: got %v, want TriggerUnspecified", got)
	}
}

func TestWithTrigger_RoundTrip(t *testing.T) {
	t.Parallel()
	cases := []commands.Trigger{
		commands.TriggerUnspecified,
		commands.TriggerManual,
		commands.TriggerScheduled,
	}
	for _, tc := range cases {
		ctx := commands.WithTrigger(t.Context(), tc)
		if got := commands.TriggerFromContext(ctx); got != tc {
			t.Errorf("round-trip for %v: got %v", tc, got)
		}
	}
}

func TestWithTrigger_NestedOverrides(t *testing.T) {
	t.Parallel()
	// The dispatcher attaches the trigger once per command; nested
	// attachments should resolve to the most-recent value (standard
	// context.WithValue chain semantics).
	ctx := commands.WithTrigger(t.Context(), commands.TriggerScheduled)
	ctx = commands.WithTrigger(ctx, commands.TriggerManual)
	if got := commands.TriggerFromContext(ctx); got != commands.TriggerManual {
		t.Errorf("nested override: got %v, want TriggerManual", got)
	}
}

// ---- J199 — ReportResult helper ----

func TestReportResult_NoSlot_NoOp(t *testing.T) {
	t.Parallel()
	// ReportResult on a context with no slot attached is a silent
	// no-op — matches C# CommandResultReporter.cs:23
	// (`if (command == null) return`). The function should not panic
	// and should not raise an error.
	commands.ReportResult(t.Context(), commands.ResultIndeterminate)
}
