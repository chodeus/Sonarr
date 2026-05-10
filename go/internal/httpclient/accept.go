package httpclient

// Accept is a tiny value type that carries an Accept-header string.
// C# cite: src/NzbDrone.Common/Http/HttpAccept.cs:1-22.
//
// The C# class is `sealed` with four `static readonly` instances. We mirror
// that with package-level `var` instances of Accept; the type is exported
// so callers can construct their own custom Accept strings without touching
// the standard four.
type Accept struct{ Value string }

// String returns the underlying header value.
func (a Accept) String() string { return a.Value }

// IsZero reports whether the Accept is the zero value (no header to set).
func (a Accept) IsZero() bool { return a.Value == "" }

var (
	// AcceptRSS mirrors HttpAccept.Rss at HttpAccept.cs:5.
	AcceptRSS = Accept{Value: "application/rss+xml, text/rss+xml, application/xml, text/xml"}

	// AcceptJSON mirrors HttpAccept.Json at HttpAccept.cs:6.
	AcceptJSON = Accept{Value: "application/json"}

	// AcceptJSONCharset mirrors HttpAccept.JsonCharset at HttpAccept.cs:7.
	AcceptJSONCharset = Accept{Value: "application/json; charset=utf-8"}

	// AcceptHTML mirrors HttpAccept.Html at HttpAccept.cs:8.
	AcceptHTML = Accept{Value: "text/html"}
)
