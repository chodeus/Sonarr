package replayer

import "testing"

func TestReplaySkipsWhenUnconfigured(t *testing.T) {
	// With no -contract.base flag, Replay should self-skip cleanly.
	Replay(t)
	if t.Skipped() {
		return
	}
	t.Fatalf("expected Replay to skip when flags are unset")
}
