package notifications_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
	"sonarr.tv/sonarr/internal/series"
)

// TestNewSeriesDeleteMessage_FilesDeleted mirrors the C# constructor
// at `Notifications/SeriesDeleteMessage.cs:14-23` for the
// deleteFiles=true arm. The "Series removed and all files were
// deleted" sentence is part of the user-visible alert body — pin
// the exact text so a future refactor can't silently regress it.
func TestNewSeriesDeleteMessage_FilesDeleted(t *testing.T) {
	t.Parallel()
	s := &series.Series{Title: "Test Show"}
	got := notifications.NewSeriesDeleteMessage(s, true)
	if !got.DeletedFiles {
		t.Errorf("DeletedFiles = false, want true")
	}
	want := "Series removed and all files were deleted"
	if got.DeletedFilesMessage != want {
		t.Errorf("DeletedFilesMessage = %q, want %q", got.DeletedFilesMessage, want)
	}
	if got.Message != "Test Show - "+want {
		t.Errorf("Message = %q, want %q", got.Message, "Test Show - "+want)
	}
	if got.Series != s {
		t.Errorf("Series pointer not preserved")
	}
}

// TestNewSeriesDeleteMessage_FilesKept mirrors the C# constructor
// for the deleteFiles=false arm.
func TestNewSeriesDeleteMessage_FilesKept(t *testing.T) {
	t.Parallel()
	s := &series.Series{Title: "Another Show"}
	got := notifications.NewSeriesDeleteMessage(s, false)
	if got.DeletedFiles {
		t.Errorf("DeletedFiles = true, want false")
	}
	want := "Series removed, files were not deleted"
	if got.DeletedFilesMessage != want {
		t.Errorf("DeletedFilesMessage = %q, want %q", got.DeletedFilesMessage, want)
	}
	if got.Message != "Another Show - "+want {
		t.Errorf("Message = %q, want %q", got.Message, "Another Show - "+want)
	}
}

// TestNewSeriesDeleteMessage_NilSeries guards the nil-series arm —
// the C# constructor at `SeriesDeleteMessage.cs:21` reads
// `series.Title` unconditionally and would NRE on a null series.
// The Go port tolerates a nil series and leaves the title prefix
// empty so the orchestrator at queue [N4] can build an empty-series
// "library cleared" message without crashing.
func TestNewSeriesDeleteMessage_NilSeries(t *testing.T) {
	t.Parallel()
	got := notifications.NewSeriesDeleteMessage(nil, true)
	want := " - Series removed and all files were deleted"
	if got.Message != want {
		t.Errorf("Message = %q, want %q", got.Message, want)
	}
}

// TestSettings_ValidateMarker verifies the [notifications.Settings]
// marker interface is satisfied by a value-receiver Validate
// method. Concrete settings types (queue [N3] — Webhook + Discord)
// must satisfy this contract.
func TestSettings_ValidateMarker(t *testing.T) {
	t.Parallel()
	var _ notifications.Settings = &dummySettings{}
}

type dummySettings struct{}

func (d *dummySettings) Validate() []notifications.TestFailure { return nil }
