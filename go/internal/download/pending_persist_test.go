package download_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/download"
	"sonarr.tv/sonarr/internal/downloadclients"
	"sonarr.tv/sonarr/internal/indexers"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/pendingreleases"
)

// capturingPendingWriter records every AddMany batch so tests can
// assert the persisted reason tags + serialised payloads. Satisfies
// download.PendingWriter.
type capturingPendingWriter struct {
	items []pendingreleases.AddItem
	calls int
	err   error
}

func (c *capturingPendingWriter) AddMany(_ context.Context, items []pendingreleases.AddItem) error {
	c.calls++
	c.items = append(c.items, items...)
	return c.err
}

func newPDDWithPending(t *testing.T, lister *stubClientLister, w download.PendingWriter) *download.ProcessDownloadDecisions {
	t.Helper()
	return newPDD(t, lister).SetPendingWriter(w)
}

// TestProcessDecisions_PersistsTemporaryRejectAsDelay — a
// TemporarilyRejected decision is persisted with reason Delay
// (ports ProcessDownloadDecisions.cs:63) and still surfaces in the
// in-memory Pending bucket.
func TestProcessDecisions_PersistsTemporaryRejectAsDelay(t *testing.T) {
	t.Parallel()

	dec := mkTemporarilyRejected("delayed.release", indexers.DownloadProtocolUsenet, 100)
	w := &capturingPendingWriter{}
	// A client is present but a TemporarilyRejected decision never
	// reaches dispatch — bucket 4 short-circuits to Pending.
	lister := &stubClientLister{usenet: []downloadclients.Provider{
		&stubProvider{name: "sab", protocol: indexers.DownloadProtocolUsenet, downloadID: "x"},
	}}
	p := newPDDWithPending(t, lister, w)

	got := p.ProcessDecisions(t.Context(), []decisionengine.Decision{dec})

	if len(got.Pending) != 1 {
		t.Fatalf("Pending = %d, want 1", len(got.Pending))
	}
	if len(got.Grabbed) != 0 {
		t.Fatalf("Grabbed = %d, want 0", len(got.Grabbed))
	}
	if w.calls != 1 {
		t.Fatalf("AddMany calls = %d, want 1", w.calls)
	}
	if len(w.items) != 1 {
		t.Fatalf("persisted items = %d, want 1", len(w.items))
	}
	if w.items[0].Reason != pendingreleases.ReasonDelay {
		t.Errorf("reason = %v, want Delay", w.items[0].Reason)
	}
	in := w.items[0].Input
	if in.SeriesID != 1 || in.MatchTitle != "delayed.release" {
		t.Errorf("Input mismatch: %+v", in)
	}
	// ReleaseInfoJSON must round-trip into parser.ReleaseInfo (the
	// queue/grab + dedup read side).
	var ri parser.ReleaseInfo
	if err := json.Unmarshal([]byte(in.ReleaseInfoJSON), &ri); err != nil {
		t.Fatalf("ReleaseInfoJSON not round-trippable: %v", err)
	}
	if ri.Title != "delayed.release" {
		t.Errorf("round-tripped Title = %q, want %q", ri.Title, "delayed.release")
	}
}

// TestProcessDecisions_PersistsFailedAsDownloadClientUnavailable — a
// dispatch that fails because no client serves the protocol is
// persisted with reason DownloadClientUnavailable
// (ports ProcessDownloadDecisions.cs:98).
func TestProcessDecisions_PersistsFailedAsDownloadClientUnavailable(t *testing.T) {
	t.Parallel()

	dec := mkDecision("unavail.release", indexers.DownloadProtocolUsenet, 200)
	w := &capturingPendingWriter{}
	lister := &stubClientLister{} // no usenet clients → dispatch ResultFailed
	p := newPDDWithPending(t, lister, w)

	got := p.ProcessDecisions(t.Context(), []decisionengine.Decision{dec})

	if len(got.Pending) != 1 {
		t.Fatalf("Pending = %d, want 1", len(got.Pending))
	}
	if len(w.items) != 1 {
		t.Fatalf("persisted items = %d, want 1", len(w.items))
	}
	if w.items[0].Reason != pendingreleases.ReasonDownloadClientUnavailable {
		t.Errorf("reason = %v, want DownloadClientUnavailable", w.items[0].Reason)
	}
}

// TestProcessDecisions_FallbackOnSecondSameEpisode — two
// TemporarilyRejected decisions for the same episode: the first is
// stored with its real reason (Delay), the second is downgraded to
// Fallback (ports PreparePending's IsEpisodeProcessed(pending) guard
// at :186-190). Both appear in the in-memory Pending bucket.
func TestProcessDecisions_FallbackOnSecondSameEpisode(t *testing.T) {
	t.Parallel()

	first := mkTemporarilyRejected("first.release", indexers.DownloadProtocolUsenet, 300)
	second := mkTemporarilyRejected("second.release", indexers.DownloadProtocolUsenet, 300)
	w := &capturingPendingWriter{}
	p := newPDDWithPending(t, &stubClientLister{}, w)

	got := p.ProcessDecisions(t.Context(), []decisionengine.Decision{first, second})

	if len(got.Pending) != 2 {
		t.Fatalf("Pending = %d, want 2", len(got.Pending))
	}
	if len(w.items) != 2 {
		t.Fatalf("persisted items = %d, want 2", len(w.items))
	}
	if w.items[0].Reason != pendingreleases.ReasonDelay {
		t.Errorf("first reason = %v, want Delay", w.items[0].Reason)
	}
	if w.items[1].Reason != pendingreleases.ReasonFallback {
		t.Errorf("second reason = %v, want Fallback", w.items[1].Reason)
	}
}

// TestProcessDecisions_NoPendingWriterIsInert — with no writer wired,
// decisions still bucket into Pending but nothing is persisted and no
// panic occurs (the optional-dep contract).
func TestProcessDecisions_NoPendingWriterIsInert(t *testing.T) {
	t.Parallel()

	dec := mkTemporarilyRejected("nowriter.release", indexers.DownloadProtocolUsenet, 400)
	p := newPDD(t, &stubClientLister{})

	got := p.ProcessDecisions(t.Context(), []decisionengine.Decision{dec})
	if len(got.Pending) != 1 {
		t.Fatalf("Pending = %d, want 1", len(got.Pending))
	}
}

// TestProcessDecisions_PendingWriteErrorSwallowed — an AddMany failure
// is logged, not propagated: the already-computed buckets are still
// returned (irreversible grabs in the batch must not be unwound).
func TestProcessDecisions_PendingWriteErrorSwallowed(t *testing.T) {
	t.Parallel()

	dec := mkTemporarilyRejected("err.release", indexers.DownloadProtocolUsenet, 500)
	w := &capturingPendingWriter{err: errors.New("db down")}
	p := newPDDWithPending(t, &stubClientLister{}, w)

	got := p.ProcessDecisions(t.Context(), []decisionengine.Decision{dec})
	if len(got.Pending) != 1 {
		t.Fatalf("Pending = %d, want 1 (write error must not drop the bucket)", len(got.Pending))
	}
	if w.calls != 1 {
		t.Fatalf("AddMany calls = %d, want 1", w.calls)
	}
}
