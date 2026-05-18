package validation

import (
	"encoding/json"
	"net/http/httptest"
	"regexp"
	"testing"

	"sonarr.tv/sonarr/internal/httperr"
)

func TestNew_Empty(t *testing.T) {
	t.Parallel()
	e := New()
	if !e.OK() {
		t.Errorf("fresh Errors must be OK")
	}
	if e.Len() != 0 {
		t.Errorf("fresh Errors len: got %d want 0", e.Len())
	}
	if got := e.Failures(); got != nil {
		t.Errorf("Failures on OK must be nil, got %v", got)
	}
}

func TestAdd_DefaultSeverityError(t *testing.T) {
	t.Parallel()
	e := New()
	e.Add("Name", "is required")
	if e.OK() {
		t.Errorf("OK must be false after Add")
	}
	f := e.Failures()
	if len(f) != 1 || f[0].PropertyName != "Name" || f[0].ErrorMessage != "is required" || f[0].Severity != "error" {
		t.Errorf("Failure shape: got %+v", f[0])
	}
}

func TestAddSeverity(t *testing.T) {
	t.Parallel()
	e := New()
	e.AddSeverity("X", "msg", "warning")
	if e.Failures()[0].Severity != "warning" {
		t.Errorf("severity: got %q", e.Failures()[0].Severity)
	}
	e2 := New()
	e2.AddSeverity("X", "msg", "")
	if e2.Failures()[0].Severity != "error" {
		t.Errorf("empty severity must normalise to error, got %q", e2.Failures()[0].Severity)
	}
}

func TestRequired_StringEmptyFails(t *testing.T) {
	t.Parallel()
	e := New()
	if ok := e.Required("Name", ""); ok {
		t.Errorf("Required('') must return false")
	}
	if e.OK() {
		t.Errorf("OK must be false")
	}
	if e.Failures()[0].ErrorMessage != "'Name' is required." {
		t.Errorf("message: got %q", e.Failures()[0].ErrorMessage)
	}
}

func TestRequired_StringNonEmptyPasses(t *testing.T) {
	t.Parallel()
	e := New()
	if ok := e.Required("Name", "bob"); !ok {
		t.Errorf("Required(non-empty) must return true")
	}
	if !e.OK() {
		t.Errorf("OK must be true")
	}
}

func TestPositive(t *testing.T) {
	t.Parallel()
	cases := []struct {
		val  int
		want bool
	}{
		{-1, false}, {0, false}, {1, true}, {1000, true},
	}
	for _, c := range cases {
		e := New()
		if got := e.Positive("X", c.val); got != c.want {
			t.Errorf("Positive(%d): got %v want %v", c.val, got, c.want)
		}
	}
}

func TestNonNegative(t *testing.T) {
	t.Parallel()
	e := New()
	if !e.NonNegative("X", 0) {
		t.Errorf("0 must be non-negative")
	}
	if e.NonNegative("X", -1) {
		t.Errorf("-1 must fail")
	}
}

func TestRange(t *testing.T) {
	t.Parallel()
	e := New()
	if !e.Range("priority", 50, 0, 100) {
		t.Errorf("50 must be in [0,100]")
	}
	if !e.Range("priority", 0, 0, 100) {
		t.Errorf("0 (lower bound, inclusive) must pass")
	}
	if !e.Range("priority", 100, 0, 100) {
		t.Errorf("100 (upper bound, inclusive) must pass")
	}
	if e.Range("priority", -1, 0, 100) {
		t.Errorf("-1 must fail")
	}
	if e.Range("priority", 101, 0, 100) {
		t.Errorf("101 must fail")
	}
	// 2 failures accumulated
	if e.Len() != 2 {
		t.Errorf("len: got %d want 2", e.Len())
	}
	if got := e.Failures()[0].ErrorMessage; got != "'priority' must be between 0 and 100 (inclusive)." {
		t.Errorf("message: got %q", got)
	}
}

func TestMaxLen(t *testing.T) {
	t.Parallel()
	e := New()
	if !e.MaxLen("X", "hello", 10) {
		t.Errorf("len 5 ≤ 10 must pass")
	}
	if !e.MaxLen("X", "hello", 5) {
		t.Errorf("len 5 = 5 must pass")
	}
	if e.MaxLen("X", "hello!", 5) {
		t.Errorf("len 6 > 5 must fail")
	}
	if got := e.Failures()[0].ErrorMessage; got != "The length of 'X' must be 5 characters or fewer." {
		t.Errorf("message: got %q", got)
	}
}

func TestMinLen(t *testing.T) {
	t.Parallel()
	e := New()
	if !e.MinLen("X", "hello", 3) {
		t.Errorf("len 5 ≥ 3 must pass")
	}
	if e.MinLen("X", "hi", 3) {
		t.Errorf("len 2 < 3 must fail")
	}
	if got := e.Failures()[0].ErrorMessage; got != "The length of 'X' must be at least 3 characters." {
		t.Errorf("message: got %q", got)
	}
}

func TestPattern(t *testing.T) {
	t.Parallel()
	re := regexp.MustCompile(`^[a-z]+$`)
	e := New()
	if !e.Pattern("X", "abc", re) {
		t.Errorf("abc must match")
	}
	if e.Pattern("X", "abc123", re) {
		t.Errorf("abc123 must not match")
	}
	if e.Pattern("X", "abc", nil) {
		t.Errorf("nil regex must fail")
	}
}

func TestMust(t *testing.T) {
	t.Parallel()
	e := New()
	if !e.Must("X", true, "msg") {
		t.Errorf("Must(true) must pass")
	}
	if e.Must("X", false, "custom message") {
		t.Errorf("Must(false) must fail")
	}
	if got := e.Failures()[0].ErrorMessage; got != "custom message" {
		t.Errorf("message: got %q", got)
	}
}

func TestFailures_OwnedByCaller(t *testing.T) {
	t.Parallel()
	// Returned slice must be a copy — mutating it shouldn't affect
	// the Errors. Catches accidental shared-state bugs that would
	// leak per-request failure lists across handlers.
	e := New()
	e.Add("X", "first")
	out := e.Failures()
	out[0].PropertyName = "MUTATED"
	if e.Failures()[0].PropertyName != "X" {
		t.Errorf("mutation leaked back: %v", e.Failures()[0])
	}
}

func TestFailures_PreservesInsertionOrder(t *testing.T) {
	t.Parallel()
	e := New()
	e.Add("A", "1")
	e.Add("B", "2")
	e.Add("C", "3")
	got := e.Failures()
	for i, want := range []string{"A", "B", "C"} {
		if got[i].PropertyName != want {
			t.Errorf("order[%d]: got %q want %q", i, got[i].PropertyName, want)
		}
	}
}

func TestIntegration_WriteValidationProducesCSharpShape(t *testing.T) {
	t.Parallel()
	// End-to-end pin: failures accumulated here, fed through
	// httperr.WriteValidation, emit a body the C# UI can parse.
	e := New()
	e.Required("Name", "")
	e.Positive("Port", 0)
	e.MaxLen("Path", "this-is-too-long-for-five", 5)
	rr := httptest.NewRecorder()
	httperr.WriteValidation(rr, e.Failures())
	if rr.Code != 400 {
		t.Errorf("status: got %d want 400", rr.Code)
	}
	var got []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("failures: got %d want 3", len(got))
	}
	if got[0]["propertyName"] != "Name" || got[0]["errorMessage"] != "'Name' is required." {
		t.Errorf("[0]: %v", got[0])
	}
	if got[1]["propertyName"] != "Port" {
		t.Errorf("[1]: %v", got[1])
	}
	if got[2]["propertyName"] != "Path" {
		t.Errorf("[2]: %v", got[2])
	}
	for i, f := range got {
		if f["severity"] != "error" {
			t.Errorf("[%d].severity: got %v want error", i, f["severity"])
		}
	}
}

func TestItoa(t *testing.T) {
	t.Parallel()
	cases := map[int]string{
		0: "0", 1: "1", 9: "9", 10: "10", 99: "99", 100: "100",
		-1: "-1", -100: "-100", 65535: "65535", -65535: "-65535",
	}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d): got %q want %q", in, got, want)
		}
	}
}
