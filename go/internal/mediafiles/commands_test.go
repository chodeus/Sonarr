package mediafiles

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestRescanSeriesCommandJSONShape pins the wire shape per Option A
// (go/JSON_CONVENTIONS.md). RescanSeriesCommand.SeriesID is `*int`
// (mirroring C# `int?`); nil emits explicit `null`, non-nil emits
// the integer. Drift here silently breaks the messaging-bus payloads
// queued by the API.
func TestRescanSeriesCommandJSONShape(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		id := 42
		cmd := RescanSeriesCommand{SeriesID: &id}
		raw, err := json.Marshal(cmd)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		want := `{"seriesId":42}`
		if string(raw) != want {
			t.Fatalf("marshal: got %s want %s", raw, want)
		}
		var back RescanSeriesCommand
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if back.SeriesID == nil || *back.SeriesID != id {
			t.Fatalf("unmarshal: got %v want %d", back.SeriesID, id)
		}
	})

	t.Run("empty", func(t *testing.T) {
		var cmd RescanSeriesCommand
		raw, err := json.Marshal(cmd)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// Per Option A: nullable C# int? emits explicit null, not
		// field-absent.
		want := `{"seriesId":null}`
		if string(raw) != want {
			t.Fatalf("marshal empty: got %s want %s", raw, want)
		}
		var back RescanSeriesCommand
		if err := json.Unmarshal([]byte(`{}`), &back); err != nil {
			t.Fatalf("unmarshal empty: %v", err)
		}
		if back.SeriesID != nil {
			t.Fatalf("unmarshal empty: got %v want nil", back.SeriesID)
		}
	})

	// Nil-vs-zero distinction: `{"seriesId": 0}` parses to a non-nil
	// pointer holding 0, mirroring the C# `int?` semantic.
	t.Run("zero is non-nil", func(t *testing.T) {
		var cmd RescanSeriesCommand
		if err := json.Unmarshal([]byte(`{"seriesId":0}`), &cmd); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if cmd.SeriesID == nil {
			t.Fatal("expected non-nil pointer for explicit 0")
		}
		if *cmd.SeriesID != 0 {
			t.Fatalf("got %d want 0", *cmd.SeriesID)
		}
	})
}

// TestRescanSeriesName pins the canonical command name — same token
// used by the registry, the Commands.Name DB column, and the
// ScheduledTasks.TypeName column.
func TestRescanSeriesName(t *testing.T) {
	if RescanSeriesName != "RescanSeries" {
		t.Fatalf("RescanSeriesName drift: got %q", RescanSeriesName)
	}
}

// TestRescanSeriesCommandFieldShape uses reflect to pin the exposed
// field set — a future contributor adding fields will tickle this so
// the C# tree drift gets a deliberate update.
func TestRescanSeriesCommandFieldShape(t *testing.T) {
	typ := reflect.TypeOf(RescanSeriesCommand{})
	if got := typ.NumField(); got != 1 {
		t.Fatalf("RescanSeriesCommand: got %d fields, want 1", got)
	}
	f := typ.Field(0)
	if f.Name != "SeriesID" {
		t.Fatalf("field name: got %q want SeriesID", f.Name)
	}
	if f.Type.Kind().String() != "ptr" {
		t.Fatalf("field kind: got %s want ptr", f.Type.Kind())
	}
	// Per Option A: no omitempty. Nullable C# int? emits explicit null.
	if f.Tag.Get("json") != "seriesId" {
		t.Fatalf("json tag: got %q", f.Tag.Get("json"))
	}
}
