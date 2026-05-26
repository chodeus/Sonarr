package releasecache

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/decisionengine"
)

func TestKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		idx  int
		guid string
		want string
	}{
		{42, "abc", "42_abc"},
		{0, "x", "0_x"},
		{-1, "y", "-1_y"},
		{12345, "https://example.com/foo", "12345_https://example.com/foo"},
	}
	for _, tc := range cases {
		if got := Key(tc.idx, tc.guid); got != tc.want {
			t.Errorf("Key(%d, %q) = %q, want %q", tc.idx, tc.guid, got, tc.want)
		}
	}
}

func TestCache_PutGet(t *testing.T) {
	t.Parallel()
	c := New(0) // DefaultTTL
	d := decisionengine.NewDecision("subject-1")
	c.Put("42_abc", d)
	got, ok := c.Get("42_abc")
	if !ok {
		t.Fatalf("Get: miss after Put")
	}
	if got.Subject != "subject-1" {
		t.Errorf("Subject: got %v want subject-1", got.Subject)
	}
}

func TestCache_GetMiss(t *testing.T) {
	t.Parallel()
	c := New(0)
	if _, ok := c.Get("nonexistent"); ok {
		t.Errorf("Get on empty cache should miss")
	}
}

func TestCache_ExpiredEvicted(t *testing.T) {
	t.Parallel()
	c := New(100 * time.Millisecond)
	clock := time.Unix(1000, 0)
	c.SetClock(func() time.Time { return clock })
	c.Put("k", decisionengine.NewDecision("s"))
	if _, ok := c.Get("k"); !ok {
		t.Fatalf("Get immediately after Put should hit")
	}
	clock = clock.Add(200 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Errorf("Get past TTL should miss")
	}
	if c.Len() != 0 {
		t.Errorf("expired entry should be evicted on read; Len=%d", c.Len())
	}
}

func TestCache_PutResetsTTL(t *testing.T) {
	t.Parallel()
	c := New(100 * time.Millisecond)
	clock := time.Unix(1000, 0)
	c.SetClock(func() time.Time { return clock })
	c.Put("k", decisionengine.NewDecision("first"))
	clock = clock.Add(80 * time.Millisecond)
	// Re-Put extends TTL.
	c.Put("k", decisionengine.NewDecision("second"))
	clock = clock.Add(50 * time.Millisecond) // total 130ms — beyond original TTL but within new
	got, ok := c.Get("k")
	if !ok {
		t.Fatalf("re-Put should reset TTL; got miss at 130ms")
	}
	if got.Subject != "second" {
		t.Errorf("Subject: got %v want second", got.Subject)
	}
}

func TestCache_PutMany(t *testing.T) {
	t.Parallel()
	c := New(0)
	batch := map[string]decisionengine.Decision{
		"a": decisionengine.NewDecision("subj-a"),
		"b": decisionengine.NewDecision("subj-b"),
		"c": decisionengine.NewDecision("subj-c"),
	}
	c.PutMany(batch)
	if c.Len() != 3 {
		t.Errorf("Len after PutMany: got %d want 3", c.Len())
	}
	for k, expected := range batch {
		got, ok := c.Get(k)
		if !ok {
			t.Errorf("Get(%q) missed", k)
			continue
		}
		if got.Subject != expected.Subject {
			t.Errorf("Get(%q).Subject = %v want %v", k, got.Subject, expected.Subject)
		}
	}
}

func TestCache_Reset(t *testing.T) {
	t.Parallel()
	c := New(0)
	c.Put("a", decisionengine.NewDecision("x"))
	c.Put("b", decisionengine.NewDecision("y"))
	c.Reset()
	if c.Len() != 0 {
		t.Errorf("Len after Reset: got %d want 0", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Errorf("Get after Reset should miss")
	}
}

func TestCache_DefaultTTLAppliedOnZero(t *testing.T) {
	t.Parallel()
	c := New(0)
	if c.ttl != DefaultTTL {
		t.Errorf("DefaultTTL not applied: got %v want %v", c.ttl, DefaultTTL)
	}
}

func TestCache_ConcurrentPutGet(t *testing.T) {
	t.Parallel()
	c := New(0)
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		i := i
		go func() {
			c.Put(Key(i, "g"), decisionengine.NewDecision(i))
			done <- struct{}{}
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
	if c.Len() != 50 {
		t.Errorf("concurrent Put: Len=%d want 50", c.Len())
	}
	// Concurrent reads.
	for i := 0; i < 50; i++ {
		i := i
		go func() {
			_, _ = c.Get(Key(i, "g"))
			done <- struct{}{}
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
}
