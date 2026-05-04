package commands_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/commands"
)

func TestStatus_String(t *testing.T) {
	t.Parallel()
	cases := []struct {
		v    commands.Status
		want string
	}{
		{commands.StatusQueued, "Queued"},
		{commands.StatusStarted, "Started"},
		{commands.StatusCompleted, "Completed"},
		{commands.StatusFailed, "Failed"},
		{commands.StatusAborted, "Aborted"},
		{commands.StatusCancelled, "Cancelled"},
		{commands.StatusOrphaned, "Orphaned"},
		{commands.Status(99), "Unknown"},
	}
	for _, c := range cases {
		if got := c.v.String(); got != c.want {
			t.Errorf("Status(%d).String() = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestStatus_FrozenValues(t *testing.T) {
	t.Parallel()
	// Persisted on `Commands.Status`. Reordering would break legacy rows.
	pins := map[commands.Status]int{
		commands.StatusQueued:    0,
		commands.StatusStarted:   1,
		commands.StatusCompleted: 2,
		commands.StatusFailed:    3,
		commands.StatusAborted:   4,
		commands.StatusCancelled: 5,
		commands.StatusOrphaned:  6,
	}
	for v, want := range pins {
		if int(v) != want {
			t.Errorf("Status %s: got %d, want %d", v, int(v), want)
		}
	}
}

func TestResult_FrozenValues(t *testing.T) {
	t.Parallel()
	pins := map[commands.Result]int{
		commands.ResultUnknown:       0,
		commands.ResultSuccessful:    1,
		commands.ResultUnsuccessful:  2,
		commands.ResultIndeterminate: 3,
	}
	for v, want := range pins {
		if int(v) != want {
			t.Errorf("Result %s: got %d, want %d", v, int(v), want)
		}
	}
	// String paths.
	if commands.ResultSuccessful.String() != "Successful" {
		t.Errorf("Successful: %s", commands.ResultSuccessful)
	}
	if commands.Result(99).String() != "Unknown" {
		t.Errorf("Result(99) should fall through to Unknown")
	}
}

func TestTrigger_FrozenValues(t *testing.T) {
	t.Parallel()
	pins := map[commands.Trigger]int{
		commands.TriggerUnspecified: 0,
		commands.TriggerManual:      1,
		commands.TriggerScheduled:   2,
	}
	for v, want := range pins {
		if int(v) != want {
			t.Errorf("Trigger %s: got %d, want %d", v, int(v), want)
		}
	}
	if commands.Trigger(99).String() != "Unknown" {
		t.Errorf("Trigger(99) should fall through to Unknown")
	}
}

func TestPriority_FrozenValues(t *testing.T) {
	t.Parallel()
	// Note negative value for Low — pinned because the dispatcher
	// sorts by Priority DESC and depends on the negative ordering.
	pins := map[commands.Priority]int{
		commands.PriorityLow:    -1,
		commands.PriorityNormal: 0,
		commands.PriorityHigh:   1,
	}
	for v, want := range pins {
		if int(v) != want {
			t.Errorf("Priority %s: got %d, want %d", v, int(v), want)
		}
	}
	if commands.PriorityLow.String() != "Low" {
		t.Errorf("Low: %s", commands.PriorityLow)
	}
}
