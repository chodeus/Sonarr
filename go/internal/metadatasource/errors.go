package metadatasource

import (
	"errors"
	"fmt"
)

// errors.go ports the three NzbDroneClientException subclasses the
// MetadataSource family throws — plus the typed wrappers callers use
// to detect each kind. Three classes per C# tree:
//
//   1. SkyHookException → HTTP 503 Service Unavailable.
//      `src/NzbDrone.Core/MetadataSource/SkyHook/SkyHookException.cs`
//   2. InvalidSearchTermException → HTTP 400 Bad Request.
//      `src/NzbDrone.Core/MetadataSource/SkyHook/InvalidSearchTermException.cs`
//   3. SeriesNotFoundException → HTTP 404 Not Found.
//      `src/NzbDrone.Core/Tv/SeriesNotFoundException.cs` (used by
//      SkyHookProxy.GetSeriesInfo when SkyHook returns 404).
//
// The Go port follows the sentinel-error pattern: each kind has an
// exported `Err…` value that callers errors.Is() against, plus a
// typed [Error] struct that captures the human-facing message + the
// underlying cause for logging.

var (
	// ErrServiceUnavailable wraps every transport-layer failure
	// (DNS / connection refused / 5xx non-404 / decode failure /
	// SkyHook protocol violation). Ports C# `SkyHookException`
	// which extends `NzbDroneClientException(HttpStatusCode.ServiceUnavailable)`.
	//
	// The Phase-5 API surface maps this sentinel to HTTP 503 so
	// the add-series UI can render "SkyHook unavailable, try
	// again later" with the right status code.
	ErrServiceUnavailable = errors.New("metadatasource: skyhook unavailable")

	// ErrSeriesNotFound wraps an upstream 404 against
	// `GET /v1/tvdb/shows/en/{id}/`. Ports C#
	// `SeriesNotFoundException` (which the SkyHookProxy throws
	// at `SkyHookProxy.cs:57-60` when HttpStatusCode == NotFound).
	//
	// The Phase-5 API maps this sentinel to HTTP 404. Callers
	// (SeriesController.Lookup, RefreshSeriesService) check
	// errors.Is(err, ErrSeriesNotFound) to distinguish "TVDB ID
	// genuinely doesn't exist" from "SkyHook is unreachable".
	ErrSeriesNotFound = errors.New("metadatasource: series not found")

	// ErrInvalidSearchTerm wraps the path-injection pre-filter
	// at C# `SkyHookProxy.cs:110-113` —
	// `if (title.IsPathValid(PathValidationType.AnyOs))` throws
	// `InvalidSearchTermException`. Ports the same gate.
	//
	// The Phase-5 API maps this sentinel to HTTP 400. The
	// concrete SkyHookProxy slice (4.AT / 4.AU, queues [M3a]/[M3b])
	// is the layer that wraps this sentinel around the actual
	// rejected term; this file only defines the sentinel.
	ErrInvalidSearchTerm = errors.New("metadatasource: invalid search term")
)

// Error is the typed wrapper concrete provider impls return so
// callers get both the matching sentinel (via errors.Is) AND a
// human-facing message for logging.
//
// The C# constructor takes `(message, args, innerException)` and
// the resulting `Exception.Message` is `string.Format(message, args)`.
// The Go port pre-formats Message at construction time (callers
// pass a formatted string) and stores Cause for chained-error
// unwrapping.
//
// One [Error] value carries exactly one sentinel — pick the right
// constructor:
//
//   - [NewServiceUnavailableError] wraps an arbitrary transport
//     failure as ErrServiceUnavailable.
//   - [NewSeriesNotFoundError] wraps a 404 against shows/{id} as
//     ErrSeriesNotFound. The tvdbID gets folded into the message.
//   - [NewInvalidSearchTermError] wraps the path-injection gate
//     as ErrInvalidSearchTerm. The rejected term is the message.
//
// Errors.Is + Errors.Unwrap semantics:
//
//   - errors.Is(e, sentinel) returns true when e.kind matches.
//   - errors.Unwrap(e) returns e.Cause (may be nil).
//
// The Go port intentionally does NOT subclass io.EOF / net.OpError
// / similar — callers wanting to introspect the cause use
// errors.As against the Cause chain.
type Error struct {
	// kind is the sentinel this Error reports as. Compared via
	// errors.Is. Always one of the three Err… values above.
	kind error
	// Message is the human-facing error string. Callers should
	// not parse this for behaviour — use errors.Is on the
	// sentinel instead.
	Message string
	// Cause is the wrapped underlying error, if any. May be nil
	// (e.g. ErrInvalidSearchTerm has no inner cause — the path-
	// validation gate is a pure-logic rejection).
	Cause error
}

// Error implements the error interface.
//
// Format mirrors the C# `Exception.Message` shape — just the
// formatted message — so the slog-rendered output is concise. The
// Cause chain is reachable via errors.Unwrap for callers that
// want the underlying detail.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Unwrap returns the underlying cause for the errors.Unwrap chain.
// Returns nil when no cause was supplied at construction time.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is reports whether this Error matches the supplied target. Only
// the kind sentinel is checked here; the Cause chain handles
// downstream wrappers (caller calls errors.Is which walks Unwrap()).
//
// This is what makes
//
//	errors.Is(err, metadatasource.ErrServiceUnavailable)
//
// work for any [Error] constructed via [NewServiceUnavailableError]
// — the errors.Is walker compares target against e.kind via
// pointer equality (sentinel errors are package-level
// var pointers).
func (e *Error) Is(target error) bool {
	if e == nil {
		return false
	}
	return e.kind == target
}

// NewServiceUnavailableError wraps `cause` as a 503-class error
// reported via [ErrServiceUnavailable]. Caller-supplied formatted
// message (typically `fmt.Sprintf("search for %q failed: %v", title, cause)`).
//
// Returns a non-nil *Error; nil cause is permitted (e.g. for a
// synthesised "decode failure" the caller has no transport-layer
// error to attach).
func NewServiceUnavailableError(msg string, cause error) *Error {
	return &Error{
		kind:    ErrServiceUnavailable,
		Message: msg,
		Cause:   cause,
	}
}

// NewSeriesNotFoundError wraps a 404 against a specific TVDB id.
// Ports the C# `SeriesNotFoundException(int tvdbId)` constructor.
//
// The C# message uses the format `Series with TvDb Id {0} was not
// found, it may have been removed from TheTVDB`; the Go port
// preserves the same shape so log scraping survives the port.
func NewSeriesNotFoundError(tvdbID int) *Error {
	return &Error{
		kind:    ErrSeriesNotFound,
		Message: fmt.Sprintf("series with TvDb Id %d was not found, it may have been removed from TheTVDB", tvdbID),
	}
}

// NewInvalidSearchTermError wraps the path-injection guard hit
// against `term`. Ports the C# `InvalidSearchTermException` thrown
// at `SkyHookProxy.cs:110-113`.
//
// Mirrors the C# message shape (`Invalid search term '{term}'`)
// so existing log/alert patterns survive the port.
func NewInvalidSearchTermError(term string) *Error {
	return &Error{
		kind:    ErrInvalidSearchTerm,
		Message: fmt.Sprintf("invalid search term '%s'", term),
	}
}
