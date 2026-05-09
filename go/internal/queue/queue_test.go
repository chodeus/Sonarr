package queue_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/queue"
)

func TestQueueStatusStringMatchesCSharpNames(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   queue.QueueStatus
		want string
	}{
		{queue.QueueStatusUnknown, "Unknown"},
		{queue.QueueStatusQueued, "Queued"},
		{queue.QueueStatusPaused, "Paused"},
		{queue.QueueStatusDownloading, "Downloading"},
		{queue.QueueStatusCompleted, "Completed"},
		{queue.QueueStatusFailed, "Failed"},
		{queue.QueueStatusWarning, "Warning"},
		{queue.QueueStatusDelay, "Delay"},
		{queue.QueueStatusDownloadClientUnavailable, "DownloadClientUnavailable"},
		{queue.QueueStatusFallback, "Fallback"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Errorf("QueueStatus(%d).String() = %q, want %q", int(c.in), got, c.want)
		}
	}
}

func TestQueueStatusStringOutOfRange(t *testing.T) {
	t.Parallel()
	if got := queue.QueueStatus(99).String(); got != "QueueStatus(99)" {
		t.Errorf("QueueStatus(99).String() = %q, want fallback", got)
	}
}

func TestQueueStatusOrdinalsArePinned(t *testing.T) {
	t.Parallel()
	// Wire-stable: int values must not drift. v3 API JSON envelope
	// passes them by ordinal.
	cases := []struct {
		in   queue.QueueStatus
		want int
	}{
		{queue.QueueStatusUnknown, 0},
		{queue.QueueStatusQueued, 1},
		{queue.QueueStatusPaused, 2},
		{queue.QueueStatusDownloading, 3},
		{queue.QueueStatusCompleted, 4},
		{queue.QueueStatusFailed, 5},
		{queue.QueueStatusWarning, 6},
		{queue.QueueStatusDelay, 7},
		{queue.QueueStatusDownloadClientUnavailable, 8},
		{queue.QueueStatusFallback, 9},
	}
	for _, c := range cases {
		if int(c.in) != c.want {
			t.Errorf("ord(%s) = %d, want %d", c.in.String(), int(c.in), c.want)
		}
	}
}
