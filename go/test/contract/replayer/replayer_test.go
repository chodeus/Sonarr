package replayer

import (
	"strings"
	"testing"

	"sonarr.tv/sonarr/test/contract/internal/spec"
)

func TestReplaySkipsWhenUnconfigured(t *testing.T) {
	// With no -contract.base flag, Replay should self-skip cleanly.
	Replay(t)
	if t.Skipped() {
		return
	}
	t.Fatalf("expected Replay to skip when flags are unset")
}

func TestCompare_EqualReturnsEmpty(t *testing.T) {
	t.Parallel()
	g := &spec.Golden{Status: 200, BodyIsJSON: true, BodyJSON: []byte(`{"a":1,"b":[1,2]}`)}
	w := &spec.Golden{Status: 200, BodyIsJSON: true, BodyJSON: []byte(`{"a":1,"b":[1,2]}`)}
	if got := compare(g, w); got != "" {
		t.Errorf("expected empty diff; got %q", got)
	}
}

func TestCompare_StatusMismatchSurfaces(t *testing.T) {
	t.Parallel()
	g := &spec.Golden{Status: 500, BodyIsJSON: true, BodyJSON: []byte(`{}`)}
	w := &spec.Golden{Status: 200, BodyIsJSON: true, BodyJSON: []byte(`{}`)}
	out := compare(g, w)
	if !strings.Contains(out, "status: got 500 want 200") {
		t.Errorf("missing status line: %q", out)
	}
}

func TestCompare_StructuredJSONDiff(t *testing.T) {
	t.Parallel()
	// Two JSON bodies that differ at $.b[1] — JSONDiff should
	// surface that path, not the raw bytes.
	g := &spec.Golden{Status: 200, BodyIsJSON: true, BodyJSON: []byte(`{"a":1,"b":[1,9]}`)}
	w := &spec.Golden{Status: 200, BodyIsJSON: true, BodyJSON: []byte(`{"a":1,"b":[1,2]}`)}
	out := compare(g, w)
	if !strings.Contains(out, "structured diff") {
		t.Errorf("expected structured diff header; got %q", out)
	}
	if !strings.Contains(out, "$.b[1]") {
		t.Errorf("missing path $.b[1]: %q", out)
	}
	if !strings.Contains(out, "got=9") || !strings.Contains(out, "want=2") {
		t.Errorf("missing scalar values: %q", out)
	}
	// Raw dump shouldn't appear when the structured path is taken.
	if strings.Contains(out, "got={") {
		t.Errorf("raw got= dump should be suppressed when structured diff is available: %q", out)
	}
}

func TestCompare_TruncatesLargeDiff(t *testing.T) {
	t.Parallel()
	// Build two JSON objects with 60 differing keys so the diff
	// exceeds the 50-entry cap.
	var gb, wb strings.Builder
	gb.WriteString("{")
	wb.WriteString("{")
	for i := 0; i < 60; i++ {
		if i > 0 {
			gb.WriteString(",")
			wb.WriteString(",")
		}
		// k00..k59 sort lexically so the cap drops k50..k59.
		fmtKey := func(i int) string {
			if i < 10 {
				return "\"k0" + string(rune('0'+i)) + "\""
			}
			return "\"k" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + "\""
		}
		gb.WriteString(fmtKey(i) + ":1")
		wb.WriteString(fmtKey(i) + ":2")
	}
	gb.WriteString("}")
	wb.WriteString("}")
	g := &spec.Golden{Status: 200, BodyIsJSON: true, BodyJSON: []byte(gb.String())}
	w := &spec.Golden{Status: 200, BodyIsJSON: true, BodyJSON: []byte(wb.String())}
	out := compare(g, w)
	if !strings.Contains(out, "60 structured diff(s)") {
		t.Errorf("expected total-count header: %q", out)
	}
	if !strings.Contains(out, "(10 more;") {
		t.Errorf("expected truncation marker: %q", out)
	}
	if !strings.Contains(out, "$.k00") {
		t.Errorf("expected first key in output (sorted): %q", out)
	}
	if strings.Contains(out, "$.k55") {
		t.Errorf("expected k55 to be truncated past the cap: %q", out)
	}
}

func TestCompare_FallsBackToRawDumpOnMalformedJSON(t *testing.T) {
	t.Parallel()
	// Force the byte-compare to fire but make JSONDiff error so the
	// fallback raw dump shows up.
	g := &spec.Golden{Status: 200, BodyIsJSON: true, BodyJSON: []byte(`{not json`)}
	w := &spec.Golden{Status: 200, BodyIsJSON: true, BodyJSON: []byte(`{"a":1}`)}
	out := compare(g, w)
	if !strings.Contains(out, "body json:") {
		t.Errorf("expected raw dump header: %q", out)
	}
	if strings.Contains(out, "structured diff") {
		t.Errorf("structured-diff branch should not fire on malformed JSON: %q", out)
	}
}
