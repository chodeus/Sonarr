package commands_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/commands"
)

// fixedClock returns an injectable clock that returns the given t.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// staticProps is a QueueProperties resolver backed by a fixed map.
func staticProps(m map[string]commands.QueueProperties) func(string) commands.QueueProperties {
	return func(name string) commands.QueueProperties {
		return m[name]
	}
}

func TestQueue_AddAndPull_Happy(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	c := &commands.Command{ID: 1, Name: "Backup", Priority: 0, QueuedAt: time.Now()}
	q.Add(c)

	got, err := q.Pull(t.Context())
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if got != c {
		t.Errorf("Pull: returned different pointer")
	}
	if commands.Status(got.Status) != commands.StatusStarted {
		t.Errorf("Pull: Status=%v, want Started", commands.Status(got.Status))
	}
	if got.StartedAt == nil {
		t.Errorf("Pull: StartedAt not stamped")
	}
}

func TestQueue_PriorityOrdering(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	low := &commands.Command{ID: 1, Name: "Low", Priority: int(commands.PriorityLow), QueuedAt: t0}
	high := &commands.Command{ID: 2, Name: "High", Priority: int(commands.PriorityHigh), QueuedAt: t0.Add(time.Second)}
	normal := &commands.Command{ID: 3, Name: "Normal", Priority: int(commands.PriorityNormal), QueuedAt: t0}
	q.Add(low)
	q.Add(high)
	q.Add(normal)

	first, _ := q.Pull(t.Context())
	if first.ID != high.ID {
		t.Errorf("first pull: id=%d, want High(2)", first.ID)
	}
	// Mark high terminal so it won't gate further Pulls.
	first.Status = int(commands.StatusCompleted)
	q.RemoveCompleted(first.ID)

	second, _ := q.Pull(t.Context())
	if second.ID != normal.ID {
		t.Errorf("second pull: id=%d, want Normal(3)", second.ID)
	}
	second.Status = int(commands.StatusCompleted)
	q.RemoveCompleted(second.ID)

	third, _ := q.Pull(t.Context())
	if third.ID != low.ID {
		t.Errorf("third pull: id=%d, want Low(1)", third.ID)
	}
}

func TestQueue_QueuedAtTiebreak(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	older := &commands.Command{ID: 1, Name: "A", Priority: 0, QueuedAt: t0}
	newer := &commands.Command{ID: 2, Name: "B", Priority: 0, QueuedAt: t0.Add(time.Second)}
	q.Add(newer) // insert in reverse order
	q.Add(older)

	got, _ := q.Pull(t.Context())
	if got.ID != older.ID {
		t.Errorf("tiebreak: id=%d, want older(1) because of earlier QueuedAt", got.ID)
	}
}

func TestQueue_DiskAccessGate(t *testing.T) {
	t.Parallel()
	props := staticProps(map[string]commands.QueueProperties{
		"DiskHeavy": {RequiresDiskAccess: true},
		"OtherDisk": {RequiresDiskAccess: true},
		"Light":     {},
	})
	q := commands.NewQueue(props)
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	first := &commands.Command{ID: 1, Name: "DiskHeavy", QueuedAt: t0}
	second := &commands.Command{ID: 2, Name: "OtherDisk", QueuedAt: t0.Add(time.Second)}
	light := &commands.Command{ID: 3, Name: "Light", QueuedAt: t0.Add(2 * time.Second)}
	q.Add(first)
	q.Add(second)
	q.Add(light)

	got, _ := q.Pull(t.Context())
	if got.ID != first.ID {
		t.Fatalf("first pull: id=%d, want 1", got.ID)
	}
	// first is now Started + RequiresDiskAccess. Next Pull should
	// skip OtherDisk (also disk-bound) and return Light instead.
	got2, _ := q.Pull(t.Context())
	if got2.ID != light.ID {
		t.Errorf("disk gate: id=%d, want Light(3) — disk-bound second should be skipped", got2.ID)
	}
}

func TestQueue_LongRunningGate(t *testing.T) {
	t.Parallel()
	props := staticProps(map[string]commands.QueueProperties{
		"LongOne": {IsLongRunning: true},
		"ExclOne": {IsExclusive: true},
		"Plain":   {},
	})
	q := commands.NewQueue(props)
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	first := &commands.Command{ID: 1, Name: "LongOne", QueuedAt: t0}
	excl := &commands.Command{ID: 2, Name: "ExclOne", QueuedAt: t0.Add(time.Second)}
	plain := &commands.Command{ID: 3, Name: "Plain", QueuedAt: t0.Add(2 * time.Second)}
	q.Add(first)
	q.Add(excl)
	q.Add(plain)

	got, _ := q.Pull(t.Context())
	if got.ID != first.ID {
		t.Fatalf("first pull: id=%d, want 1", got.ID)
	}
	// LongOne is Started; the C# rule skips IsExclusive candidates
	// while a long-runner holds the floor. Plain wins.
	got2, _ := q.Pull(t.Context())
	if got2.ID != plain.ID {
		t.Errorf("long-running gate: id=%d, want Plain(3) — exclusive should be skipped", got2.ID)
	}
}

func TestQueue_StartedExclusive_BlocksAll(t *testing.T) {
	t.Parallel()
	props := staticProps(map[string]commands.QueueProperties{
		"E": {IsExclusive: true},
		"P": {},
	})
	q := commands.NewQueue(props)
	first := &commands.Command{ID: 1, Name: "E", QueuedAt: time.Now()}
	plain := &commands.Command{ID: 2, Name: "P", QueuedAt: time.Now().Add(time.Second)}
	q.Add(first)
	q.Add(plain)

	got, _ := q.Pull(t.Context())
	if got.ID != first.ID {
		t.Fatalf("first pull: id=%d, want 1", got.ID)
	}
	// Exclusive is Started → no further pulls until it terminates.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := q.Pull(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected DeadlineExceeded while exclusive holds queue, got %v", err)
	}
	// Drain the started exclusive.
	first.Status = int(commands.StatusCompleted)
	q.RemoveCompleted(first.ID)
	got2, err := q.Pull(t.Context())
	if err != nil {
		t.Fatalf("Pull after exclusive done: %v", err)
	}
	if got2.ID != plain.ID {
		t.Errorf("post-exclusive pull: id=%d, want Plain", got2.ID)
	}
}

func TestQueue_ExclusiveCandidate_WaitsForStartedSet(t *testing.T) {
	t.Parallel()
	props := staticProps(map[string]commands.QueueProperties{
		"P": {},
		"E": {IsExclusive: true},
	})
	q := commands.NewQueue(props)
	first := &commands.Command{ID: 1, Name: "P", QueuedAt: time.Now()}
	excl := &commands.Command{ID: 2, Name: "E", QueuedAt: time.Now().Add(time.Second)}
	q.Add(first)
	q.Add(excl)

	got, _ := q.Pull(t.Context())
	if got.ID != first.ID {
		t.Fatalf("first pull: id=%d, want 1", got.ID)
	}
	// First is Started (non-exclusive). Excl is the only Queued
	// candidate but it shouldn't run while anything is Started.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := q.Pull(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("exclusive candidate should wait, got %v", err)
	}
}

func TestQueue_RemoveIfQueued(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	c := &commands.Command{ID: 7, Name: "X", QueuedAt: time.Now()}
	q.Add(c)
	if !q.RemoveIfQueued(7) {
		t.Errorf("RemoveIfQueued: want true")
	}
	if q.Len() != 0 {
		t.Errorf("Len after remove: got %d, want 0", q.Len())
	}
	if q.RemoveIfQueued(7) {
		t.Errorf("RemoveIfQueued on missing id: want false")
	}
}

func TestQueue_RemoveIfQueued_RejectsStarted(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	c := &commands.Command{ID: 7, Name: "X", QueuedAt: time.Now()}
	q.Add(c)
	got, _ := q.Pull(t.Context())
	if got.ID != 7 {
		t.Fatalf("Pull: %d", got.ID)
	}
	if q.RemoveIfQueued(7) {
		t.Errorf("RemoveIfQueued on Started row: want false")
	}
	if q.Len() != 1 {
		t.Errorf("Len: got %d, want 1 (Started rows stay until RemoveCompleted)", q.Len())
	}
}

func TestQueue_PullCtxCancel(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := q.Pull(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Pull on cancelled ctx: got %v, want context.Canceled", err)
	}
}

func TestQueue_PullCtxCancel_WhileWaiting(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := q.Pull(ctx)
		errCh <- err
	}()
	// Give the Pull goroutine time to enter Wait.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Pull while waiting: got %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Pull did not return after ctx cancel")
	}
}

func TestQueue_Close_DrainsWaiters(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	errCh := make(chan error, 1)
	go func() {
		_, err := q.Pull(t.Context())
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond)
	q.Close()
	select {
	case err := <-errCh:
		if !errors.Is(err, commands.ErrQueueClosed) {
			t.Errorf("Pull after Close: got %v, want ErrQueueClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Pull did not return after Close")
	}
}

func TestQueue_AddMany(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	rows := []*commands.Command{
		{ID: 1, Name: "A", Priority: 0, QueuedAt: t0},
		{ID: 2, Name: "B", Priority: 1, QueuedAt: t0.Add(time.Second)},
		nil,
	}
	q.AddMany(rows)
	if q.Len() != 2 {
		t.Errorf("Len after AddMany: got %d, want 2 (nils dropped)", q.Len())
	}
	first, _ := q.Pull(t.Context())
	if first.ID != 2 {
		t.Errorf("highest priority first: got id=%d, want 2", first.ID)
	}
}

func TestQueue_FindAndAll(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	c := &commands.Command{ID: 11, Name: "F", QueuedAt: time.Now()}
	q.Add(c)
	if got := q.Find(11); got != c {
		t.Errorf("Find(11): pointer mismatch")
	}
	if got := q.Find(99); got != nil {
		t.Errorf("Find(99): want nil, got %+v", got)
	}
	all := q.All()
	if len(all) != 1 || all[0].ID != 11 {
		t.Errorf("All: got %+v", all)
	}
}

func TestQueue_QueuedOrStarted(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	t0 := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	a := &commands.Command{ID: 1, Name: "A", QueuedAt: t0}
	b := &commands.Command{ID: 2, Name: "B", QueuedAt: t0.Add(time.Second)}
	q.Add(a)
	q.Add(b)
	// Pull a → Started.
	if _, err := q.Pull(t.Context()); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	got := q.QueuedOrStarted()
	if len(got) != 2 {
		t.Errorf("QueuedOrStarted: len=%d, want 2", len(got))
	}
}

func TestQueue_SetClock(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	pinned := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	q.SetClock(fixedClock(pinned))
	c := &commands.Command{ID: 1, Name: "X", QueuedAt: time.Now()}
	q.Add(c)
	got, _ := q.Pull(t.Context())
	if got.StartedAt == nil || !got.StartedAt.Equal(pinned) {
		t.Errorf("StartedAt: %v, want %v", got.StartedAt, pinned)
	}
}

// TestQueue_ConcurrentProducerConsumer races several producers against
// one consumer to flush out lock-ordering bugs under -race.
func TestQueue_ConcurrentProducerConsumer(t *testing.T) {
	t.Parallel()
	q := commands.NewQueue(nil)
	const total = 50
	var seen sync.Map
	consumerDone := make(chan struct{})
	go func() {
		for i := 0; i < total; i++ {
			c, err := q.Pull(t.Context())
			if err != nil {
				t.Errorf("Pull %d: %v", i, err)
				close(consumerDone)
				return
			}
			seen.Store(c.ID, true)
			c.Status = int(commands.StatusCompleted)
			q.RemoveCompleted(c.ID)
		}
		close(consumerDone)
	}()

	var wg sync.WaitGroup
	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			q.Add(&commands.Command{ID: id + 1, Name: "X", QueuedAt: time.Now()})
		}(i)
	}
	wg.Wait()
	<-consumerDone
	count := 0
	seen.Range(func(_, _ any) bool { count++; return true })
	if count != total {
		t.Errorf("seen distinct ids: %d, want %d", count, total)
	}
}
