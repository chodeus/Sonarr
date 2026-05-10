package httpclient

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Request is the Sonarr-flavoured HTTP request DTO. C# cite:
// src/NzbDrone.Common/Http/HttpRequest.cs:12-93.
//
// Why a DTO instead of *http.Request: Sonarr threads request-flavour flags
// (rate-limit key, suppress-error status set, log-content toggle, store-cookie
// toggles, response-stream sink) through the dispatcher chain. The C# code
// builds an *http.Request inside ManagedHttpDispatcher only at the last
// moment; we'll do the same in sub-arc 1b.
//
// All fields are exported so callers (indexers, import lists, the HTTP
// builder factory in 1b) can populate them directly.
type Request struct {
	Method                     string            // HTTP verb. Defaults to GET via NewRequest.
	URL                        URI               // Target URL.
	Headers                    *Header           // Request headers (always non-nil after NewRequest).
	ContentData                []byte            // Request body bytes.
	ContentSummary             string            // Optional human-readable summary used in log lines.
	BasicAuthUser              string            // Set by builder; nil-credentials in C# = empty here.
	BasicAuthPass              string            // Together with BasicAuthUser → Basic-Auth header.
	SuppressHTTPError          bool              // When true, the client returns the response instead of throwing.
	SuppressHTTPErrorStatusSet map[int]struct{}  // When non-nil, codes in the set bypass error throwing.
	UseSimplifiedUserAgent     bool              // Strip OS info from UA for sites that fingerprint.
	AllowAutoRedirect          bool              // Defaults to true via NewRequest. C# turns this OFF in dev mode.
	ConnectionKeepAlive        bool              // Defaults to true via NewRequest.
	LogResponseContent         bool              // When true, the dispatcher logs the body bytes.
	LogHTTPError               bool              // Defaults to true via NewRequest.
	Cookies                    map[string]string // Per-request transient cookies (merged with the persistent jar).
	StoreRequestCookie         bool              // Defaults to true via NewRequest. C# cite: HttpRequest.cs:21.
	StoreResponseCookie        bool              // C# cite: HttpRequest.cs:51.
	RequestTimeout             time.Duration     // 0 = use client default.
	RateLimit                  time.Duration     // Per-host rate-limit interval. 0 = no rate limit.
	RateLimitKey               string            // Optional sub-key for rate-limiter (host+key bucket).
	ResponseStream             io.Writer         // When non-nil, the dispatcher streams the body to this Writer.
	UserAgentOverride          string            // When non-empty, overrides the dispatcher's User-Agent.
}

// NewRequest constructs a Request with the C# constructor defaults applied
// (HttpRequest.cs:14-34): Method=GET, AllowAutoRedirect=true,
// StoreRequestCookie=true, LogHTTPError=true, ConnectionKeepAlive=true,
// fresh empty Headers and Cookies map.
//
// If accept is non-zero, the Accept header is set to its value.
//
// NOTE on the C# AllowAutoRedirect-in-dev-mode behaviour: HttpRequest.cs:25-28
// flips AllowAutoRedirect to false when RuntimeInfo.IsProduction is false.
// We do NOT mirror that here — Sonarr's dev-mode trap is a debugging aid that
// has no equivalent in the Go port (no NUnit test harness to catch it).
// Callers that want strict-no-redirect can set AllowAutoRedirect=false.
func NewRequest(rawURL string, accept ...Accept) (*Request, error) {
	u, err := ParseURI(rawURL)
	if err != nil {
		return nil, err
	}
	r := &Request{
		Method:              http.MethodGet,
		URL:                 u,
		Headers:             NewHeader(),
		Cookies:             map[string]string{},
		ConnectionKeepAlive: true,
		AllowAutoRedirect:   true,
		StoreRequestCookie:  true,
		LogHTTPError:        true,
	}
	for _, a := range accept {
		if !a.IsZero() {
			r.Headers.SetAccept(a.Value)
		}
	}
	return r, nil
}

// String produces the same shape as HttpRequest.ToString(true,true) at
// HttpRequest.cs:62-79: `Req: [METHOD] URL[: SUMMARY]`.
func (r *Request) String() string {
	return r.formatString(true, true)
}

// Format ports the parameterised HttpRequest.ToString(includeMethod,includeSummary).
func (r *Request) Format(includeMethod, includeSummary bool) string {
	return r.formatString(includeMethod, includeSummary)
}

func (r *Request) formatString(includeMethod, includeSummary bool) string {
	var b strings.Builder
	if includeMethod {
		fmt.Fprintf(&b, "Req: [%s] ", r.Method)
	}
	b.WriteString(r.URL.String())
	if includeSummary && strings.TrimSpace(r.ContentSummary) != "" {
		b.WriteString(": ")
		b.WriteString(r.ContentSummary)
	}
	return b.String()
}

// SetContentBytes assigns the raw request body. Mirrors HttpRequest.SetContent(byte[])
// at HttpRequest.cs:82-85.
func (r *Request) SetContentBytes(data []byte) { r.ContentData = data }

// SetContentString encodes the string per the current Content-Type charset
// and stores it. Mirrors HttpRequest.SetContent(string) at HttpRequest.cs:87-91.
//
// Behaviour: if Content-Type carries a charset, use it; otherwise default to
// UTF-8. The encoding lookup goes through EncodingFromContentType which
// matches the C# semantics.
func (r *Request) SetContentString(s string) error {
	enc := r.Headers.EncodingFromContentType()
	if enc == nil { // belt-and-braces; encoding.Nop is non-nil
		r.ContentData = []byte(s)
		return nil
	}
	encoded, err := enc.NewEncoder().Bytes([]byte(s))
	if err != nil {
		return fmt.Errorf("httpclient: encode body for %q: %w", r.Headers.ContentType(), err)
	}
	r.ContentData = encoded
	return nil
}
