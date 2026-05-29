package main

import (
	"context"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
)

// fakeGrabber records ProcessDecisions calls (G1/J423).
type fakeGrabber struct {
	calls int
	lastN int
}

func (f *fakeGrabber) ProcessDecisions(_ context.Context, d []decisionengine.Decision) *download.ProcessedDecisions {
	f.calls++
	f.lastN = len(d)
	return &download.ProcessedDecisions{}
}

// J423 (G1): grabSearchDecisions feeds non-empty decisions to the grabber
// (the bridge that makes search actually download), and is a safe no-op
// for empty decisions or a nil grabber.
func TestGrabSearchDecisions_J423(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	g := &fakeGrabber{}
	grabSearchDecisions(ctx, g, []decisionengine.Decision{{}, {}})
	if g.calls != 1 || g.lastN != 2 {
		t.Errorf("with 2 decisions: calls=%d lastN=%d, want 1/2", g.calls, g.lastN)
	}

	// Empty decisions → no grab.
	grabSearchDecisions(ctx, g, nil)
	if g.calls != 1 {
		t.Errorf("empty decisions should not grab; calls=%d, want 1", g.calls)
	}

	// nil grabber → no panic, no-op.
	grabSearchDecisions(ctx, nil, []decisionengine.Decision{{}})

	// Compile-time guard: the real grabber satisfies decisionGrabber.
	var _ decisionGrabber = (*download.ProcessDownloadDecisions)(nil)
}
