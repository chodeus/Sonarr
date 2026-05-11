package metadatasource

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// TestErrorIsSentinels covers the errors.Is contract for each of
// the three sentinel/constructor pairs.
func TestErrorIsSentinels(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		sentinel error
	}{
		{
			name:     "ServiceUnavailable matches ErrServiceUnavailable",
			err:      NewServiceUnavailableError("transport failure", io.ErrUnexpectedEOF),
			sentinel: ErrServiceUnavailable,
		},
		{
			name:     "SeriesNotFound matches ErrSeriesNotFound",
			err:      NewSeriesNotFoundError(81189),
			sentinel: ErrSeriesNotFound,
		},
		{
			name:     "InvalidSearchTerm matches ErrInvalidSearchTerm",
			err:      NewInvalidSearchTermError("../bad"),
			sentinel: ErrInvalidSearchTerm,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.sentinel) {
				t.Fatalf("errors.Is(%v, %v) = false, want true", tc.err, tc.sentinel)
			}
		})
	}
}

// TestErrorIsSentinels_Negatives covers the cross-product — each
// sentinel must NOT match any of the other two kinds.
func TestErrorIsSentinels_Negatives(t *testing.T) {
	su := NewServiceUnavailableError("oops", nil)
	nf := NewSeriesNotFoundError(1)
	is := NewInvalidSearchTermError("term")

	cases := []struct {
		name     string
		err      error
		sentinel error
	}{
		{"ServiceUnavailable !~ ErrSeriesNotFound", su, ErrSeriesNotFound},
		{"ServiceUnavailable !~ ErrInvalidSearchTerm", su, ErrInvalidSearchTerm},
		{"SeriesNotFound !~ ErrServiceUnavailable", nf, ErrServiceUnavailable},
		{"SeriesNotFound !~ ErrInvalidSearchTerm", nf, ErrInvalidSearchTerm},
		{"InvalidSearchTerm !~ ErrServiceUnavailable", is, ErrServiceUnavailable},
		{"InvalidSearchTerm !~ ErrSeriesNotFound", is, ErrSeriesNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if errors.Is(tc.err, tc.sentinel) {
				t.Fatalf("errors.Is(%v, %v) = true, want false", tc.err, tc.sentinel)
			}
		})
	}
}

// TestErrorUnwrap confirms the Cause chain participates in errors.Is
// for arbitrary wrapped errors. This is the path the HTTP layer uses
// to surface "this 503 was caused by a context deadline" without
// losing the sentinel match at the top level.
func TestErrorUnwrap(t *testing.T) {
	inner := errors.New("net read: connection refused")
	wrapped := NewServiceUnavailableError("skyhook unreachable", inner)

	// Top-level sentinel match still works.
	if !errors.Is(wrapped, ErrServiceUnavailable) {
		t.Fatalf("errors.Is(wrapped, ErrServiceUnavailable) = false, want true")
	}
	// Inner cause is reachable via errors.Is.
	if !errors.Is(wrapped, inner) {
		t.Fatalf("errors.Is(wrapped, inner) = false, want true")
	}
	// errors.Unwrap returns the cause directly. Pointer-identity
	// compare is intentional here — we want to confirm the *exact*
	// inner sentinel is preserved (not a wrapped variant), so the
	// errorlint "use errors.Is" guidance doesn't apply.
	//nolint:errorlint // identity check on the constructor-supplied cause.
	if errors.Unwrap(wrapped) != inner {
		t.Fatalf("errors.Unwrap(wrapped) = %v, want %v", errors.Unwrap(wrapped), inner)
	}
}

// TestErrorUnwrap_NilCause confirms that constructors which take no
// cause (e.g. NewSeriesNotFoundError / NewInvalidSearchTermError)
// surface nil from errors.Unwrap rather than panicking.
func TestErrorUnwrap_NilCause(t *testing.T) {
	err := NewSeriesNotFoundError(42)
	if got := errors.Unwrap(err); got != nil {
		t.Fatalf("errors.Unwrap(SeriesNotFound) = %v, want nil", got)
	}
}

// TestErrorMessage confirms the human-facing message matches the C#
// `Exception.Message` shape so existing log-scraping survives.
func TestErrorMessage(t *testing.T) {
	cases := []struct {
		name     string
		err      *Error
		contains string
	}{
		{
			name:     "ServiceUnavailable carries supplied message",
			err:      NewServiceUnavailableError("search for 'breaking bad' failed", nil),
			contains: "search for 'breaking bad' failed",
		},
		{
			name:     "SeriesNotFound formats tvdbId",
			err:      NewSeriesNotFoundError(81189),
			contains: "81189",
		},
		{
			name:     "SeriesNotFound message mirrors C# shape",
			err:      NewSeriesNotFoundError(81189),
			contains: "was not found",
		},
		{
			name:     "InvalidSearchTerm quotes the term",
			err:      NewInvalidSearchTermError("../etc/passwd"),
			contains: "'../etc/passwd'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.err.Error(), tc.contains) {
				t.Fatalf("Error() = %q, want substring %q", tc.err.Error(), tc.contains)
			}
		})
	}
}

// TestNilError covers the *Error nil-receiver paths. The Phase-5
// API layer may pre-check err != nil before calling Error()/Unwrap()
// but defensive nil-tolerance avoids a panic in obscure paths.
func TestNilError(t *testing.T) {
	var e *Error
	if got := e.Error(); got != "" {
		t.Errorf("nil.Error() = %q, want empty", got)
	}
	if got := e.Unwrap(); got != nil {
		t.Errorf("nil.Unwrap() = %v, want nil", got)
	}
	if e.Is(ErrServiceUnavailable) {
		t.Errorf("nil.Is(any) = true, want false")
	}
}
