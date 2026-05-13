package mediaserverqueue_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/notifications/mediaserverqueue"
	"sonarr.tv/sonarr/internal/series"
)

func TestQueue_AddCoalescesPerSeries(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[string]()
	ser := &series.Series{ID: 1}
	q.Add("hostA", ser, "Created")
	q.Add("hostA", ser, "Modified")
	q.Add("hostA", ser, "Created") // duplicate

	var got []*mediaserverqueue.Item[string]
	q.Process("hostA", func(items []*mediaserverqueue.Item[string]) {
		got = items
	})
	if len(got) != 1 {
		t.Fatalf("expected 1 coalesced item, got %d", len(got))
	}
	if got[0].Series.ID != 1 {
		t.Errorf("series.ID=%d want 1", got[0].Series.ID)
	}
	if len(got[0].Info) != 2 {
		t.Errorf("Info set size=%d want 2 (Created+Modified)", len(got[0].Info))
	}
	if _, ok := got[0].Info["Created"]; !ok {
		t.Errorf("missing Created")
	}
	if _, ok := got[0].Info["Modified"]; !ok {
		t.Errorf("missing Modified")
	}
}

func TestQueue_AddDistinctSeriesEmitsMultipleItems(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[bool]()
	q.Add("hostA", &series.Series{ID: 1}, true)
	q.Add("hostA", &series.Series{ID: 2}, false)

	var got []*mediaserverqueue.Item[bool]
	q.Process("hostA", func(items []*mediaserverqueue.Item[bool]) {
		got = items
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d", len(got))
	}
}

func TestQueue_PerHostIsolation(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[string]()
	q.Add("hostA", &series.Series{ID: 1}, "x")
	q.Add("hostB", &series.Series{ID: 1}, "y")

	var gotA, gotB []*mediaserverqueue.Item[string]
	q.Process("hostA", func(items []*mediaserverqueue.Item[string]) { gotA = items })
	q.Process("hostB", func(items []*mediaserverqueue.Item[string]) { gotB = items })
	if len(gotA) != 1 || len(gotB) != 1 {
		t.Fatalf("hostA=%d hostB=%d want 1/1", len(gotA), len(gotB))
	}
	if _, ok := gotA[0].Info["x"]; !ok {
		t.Errorf("hostA lost x")
	}
	if _, ok := gotB[0].Info["y"]; !ok {
		t.Errorf("hostB lost y")
	}
}

func TestQueue_ProcessUnknownHostNoOp(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[string]()
	called := false
	q.Process("ghost", func(_ []*mediaserverqueue.Item[string]) { called = true })
	if called {
		t.Errorf("update callback fired for unknown host")
	}
}

func TestQueue_AddNilSeriesIgnored(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[string]()
	q.Add("hostA", nil, "x")
	called := false
	q.Process("hostA", func(_ []*mediaserverqueue.Item[string]) { called = true })
	if called {
		t.Errorf("update fired despite no real Add")
	}
}

func TestQueue_ProcessDrainsItemsAddedDuringCallback(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[string]()
	q.Add("hostA", &series.Series{ID: 1}, "first")

	var batches [][]int
	q.Process("hostA", func(items []*mediaserverqueue.Item[string]) {
		batch := make([]int, 0, len(items))
		for _, it := range items {
			batch = append(batch, it.Series.ID)
		}
		batches = append(batches, batch)
		// Add during the first callback — should be drained in the
		// next loop iteration of the same Process call.
		if len(batches) == 1 {
			q.Add("hostA", &series.Series{ID: 2}, "second")
		}
	})
	if len(batches) != 2 {
		t.Fatalf("expected 2 batches (drain-loop), got %d: %v", len(batches), batches)
	}
	if batches[0][0] != 1 || batches[1][0] != 2 {
		t.Errorf("batch order wrong: %v", batches)
	}
}

func TestQueue_ConcurrentProcessSecondCallReturnsImmediately(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[string]()
	q.Add("hostA", &series.Series{ID: 1}, "x")

	inCallback := make(chan struct{})
	release := make(chan struct{})
	var first, second atomic.Int32

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		q.Process("hostA", func(_ []*mediaserverqueue.Item[string]) {
			if first.Add(1) == 1 {
				close(inCallback)
				<-release
			}
		})
	}()
	<-inCallback
	// While the first Process is parked in its callback, fire a
	// second Process call against the same host — it must hit the
	// refreshing-flag gate and return immediately without invoking
	// the update callback.
	q.Process("hostA", func(_ []*mediaserverqueue.Item[string]) {
		second.Add(1)
	})
	if second.Load() != 0 {
		t.Errorf("second.update fired=%d want 0 (refresh-gated)", second.Load())
	}
	close(release)
	wg.Wait()
	if first.Load() != 1 {
		t.Errorf("first.update calls=%d want 1", first.Load())
	}
}

func TestQueue_ProcessClearsRefreshFlagAfterReturn(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[string]()
	q.Add("hostA", &series.Series{ID: 1}, "x")

	calls := 0
	q.Process("hostA", func(_ []*mediaserverqueue.Item[string]) { calls++ })
	q.Add("hostA", &series.Series{ID: 2}, "y")
	q.Process("hostA", func(_ []*mediaserverqueue.Item[string]) { calls++ })
	if calls != 2 {
		t.Errorf("calls=%d want 2 (flag must clear after first Process)", calls)
	}
}

func TestQueue_ProcessClearsRefreshFlagOnPanic(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[string]()
	q.Add("hostA", &series.Series{ID: 1}, "x")

	func() {
		defer func() { _ = recover() }()
		q.Process("hostA", func(_ []*mediaserverqueue.Item[string]) {
			panic("boom")
		})
	}()
	// Flag should have cleared via defer — re-Process must drain
	// successfully.
	q.Add("hostA", &series.Series{ID: 2}, "y")
	called := false
	q.Process("hostA", func(_ []*mediaserverqueue.Item[string]) { called = true })
	if !called {
		t.Errorf("refresh flag not cleared after panic — second Process did not fire")
	}
}

func TestQueue_ConcurrentAddsAreSafe(t *testing.T) {
	t.Parallel()
	q := mediaserverqueue.New[int]()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(v int) {
			defer wg.Done()
			q.Add("hostA", &series.Series{ID: 1}, v)
		}(i)
	}
	wg.Wait()
	var got *mediaserverqueue.Item[int]
	q.Process("hostA", func(items []*mediaserverqueue.Item[int]) {
		if len(items) == 1 {
			got = items[0]
		}
	})
	if got == nil {
		t.Fatalf("no item drained")
	}
	if len(got.Info) != 50 {
		t.Errorf("Info set size=%d want 50", len(got.Info))
	}
}
