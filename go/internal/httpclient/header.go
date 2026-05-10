package httpclient

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
)

// Header is the Sonarr-flavoured wrapper around http.Header. C# cite:
// src/NzbDrone.Common/Http/HttpHeader.cs:27. The C# class extends
// NameValueCollection (case-insensitive multi-value); Go's http.Header is
// already case-insensitive multi-value (canonicalised) so we wrap it
// directly and add the Sonarr helpers (GetSingleValue / ContainsKey /
// ContentLength etc.).
type Header struct {
	values http.Header
}

// NewHeader returns a fresh empty Header.
func NewHeader() *Header { return &Header{values: http.Header{}} }

// HeaderFromHTTP builds a Header from a stdlib http.Header. Port-side
// equivalent of HttpHeader(HttpHeaders) at HttpHeader.cs:34. The values
// are shared, not copied — callers that mutate the original after passing
// it in will see the change here too.
func HeaderFromHTTP(h http.Header) *Header { return &Header{values: h} }

// HTTP returns the underlying http.Header for use with net/http.
func (h *Header) HTTP() http.Header { return h.values }

// Add appends a value under the given key. Key matching is case-insensitive.
func (h *Header) Add(key, value string) { h.values.Add(key, value) }

// Set replaces all values under the given key.
func (h *Header) Set(key, value string) { h.values.Set(key, value) }

// Get returns the first value under the given key, or "" if missing.
func (h *Header) Get(key string) string { return h.values.Get(key) }

// Remove deletes all values under the given key. Mirrors NameValueCollection.Remove.
func (h *Header) Remove(key string) { h.values.Del(key) }

// Values returns the slice of values registered under the given key. Returns
// nil if the key is absent. Mirrors HttpHeader.GetValues at HttpHeader.cs:51.
func (h *Header) Values(key string) []string { return h.values.Values(key) }

// AllKeys returns the canonical keys present, in iteration-order
// (non-deterministic, matching map ordering).
func (h *Header) AllKeys() []string {
	keys := make([]string, 0, len(h.values))
	for k := range h.values {
		keys = append(keys, k)
	}
	return keys
}

// ContainsKey reports whether the header has any values under the key.
// C# cite: HttpHeader.cs:43. Performs case-insensitive match (http.Header
// canonicalises so a normal lookup suffices, but we also accept arbitrary
// casing for parity with the C# `AllKeys.Any(...ToLowerInvariant())` form).
func (h *Header) ContainsKey(key string) bool {
	if _, ok := h.values[http.CanonicalHeaderKey(key)]; ok {
		return true
	}
	target := strings.ToLower(key)
	for k := range h.values {
		if strings.ToLower(k) == target {
			return true
		}
	}
	return false
}

// ErrMultipleHeaderValues mirrors the C# ApplicationException at HttpHeader.cs:59.
var ErrMultipleHeaderValues = errors.New("httpclient: header expected to occur once but had multiple values")

// GetSingleValue returns the lone value under the key, or "" if absent.
// Returns ErrMultipleHeaderValues if the key has more than one value.
// C# cite: HttpHeader.cs:49-63.
func (h *Header) GetSingleValue(key string) (string, error) {
	v := h.values.Values(key)
	if len(v) == 0 {
		return "", nil
	}
	if len(v) > 1 {
		return "", fmt.Errorf("%w: %s = %s", ErrMultipleHeaderValues, key, strings.Join(v, "|"))
	}
	return v[0], nil
}

// ContentLength reads the Content-Length header as an int64. Returns
// (0, false, nil) when absent and (n, true, nil) when present and parseable.
// C# cite: HttpHeader.cs:106-116.
func (h *Header) ContentLength() (int64, bool, error) {
	s, err := h.GetSingleValue("Content-Length")
	if err != nil {
		return 0, false, err
	}
	if s == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("httpclient: parse Content-Length: %w", err)
	}
	return n, true, nil
}

// SetContentLength writes Content-Length. Pass -1 to clear.
func (h *Header) SetContentLength(n int64) {
	if n < 0 {
		h.values.Del("Content-Length")
		return
	}
	h.values.Set("Content-Length", strconv.FormatInt(n, 10))
}

// ContentType reads the Content-Type header. Returns "" when absent.
// C# cite: HttpHeader.cs:118-128.
func (h *Header) ContentType() string { return h.values.Get("Content-Type") }

// SetContentType writes Content-Type. Empty string clears the header.
func (h *Header) SetContentType(v string) {
	if v == "" {
		h.values.Del("Content-Type")
		return
	}
	h.values.Set("Content-Type", v)
}

// Accept reads the Accept header. C# cite: HttpHeader.cs:130-140.
func (h *Header) Accept() string { return h.values.Get("Accept") }

// SetAccept writes the Accept header. Empty string clears.
func (h *Header) SetAccept(v string) {
	if v == "" {
		h.values.Del("Accept")
		return
	}
	h.values.Set("Accept", v)
}

// EncodingFromContentType returns the encoding to use to decode the response
// body, parsed out of the Content-Type charset parameter. C# cite:
// HttpHeader.cs:152-185. Defaults to UTF-8 when no charset is specified.
//
// The C# code has a `// TODO: Find encoding by Byte order mask.` comment at
// HttpHeader.cs:176 — we mirror that behaviour (BOM detection NOT done here).
func (h *Header) EncodingFromContentType() encoding.Encoding {
	return EncodingFromContentType(h.ContentType())
}

// EncodingFromContentType ports the static helper at HttpHeader.cs:157-185.
//
// Behaviour:
//   - Splits Content-Type on ';', '=', ' '.
//   - Walks until the token "charset" is reached, then takes the next token
//     (with optional surrounding quotes stripped) as the charset name.
//   - Looks the name up via golang.org/x/text/encoding/htmlindex which knows
//     the same WHATWG aliases the .NET runtime understands.
//   - Falls back to UTF-8 on lookup failure.
func EncodingFromContentType(ct string) encoding.Encoding {
	if strings.TrimSpace(ct) == "" {
		return encoding.Nop // Nop is byte-identity = utf-8-passthrough for ASCII; callers should treat as UTF-8.
	}
	tokens := splitContentType(ct)
	for i, t := range tokens {
		if strings.EqualFold(t, "charset") && i+1 < len(tokens) {
			name := strings.Trim(tokens[i+1], "\"")
			enc, err := htmlindex.Get(name)
			if err == nil && enc != nil {
				return enc
			}
			// Common aliases htmlindex misses
			switch strings.ToLower(name) {
			case "iso-8859-1", "latin1", "latin-1":
				return charmap.ISO8859_1
			}
			break
		}
	}
	return encoding.Nop
}

func splitContentType(s string) []string {
	out := make([]string, 0, 4)
	cur := strings.Builder{}
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	for _, r := range s {
		switch r {
		case ';', '=', ' ':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// ParseHTTPDate parses an RFC1123 date as written in HTTP headers (e.g.
// the value of Date / Expires / Last-Modified / Retry-After). C# cite:
// HttpHeader.ParseDateTime at HttpHeader.cs:187-190 — uses format "R" with
// AssumeUniversal which means UTC.
func ParseHTTPDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC1123, s); err == nil {
		return t.UTC(), nil
	}
	// .NET's "R" format also accepts RFC1123Z (numeric offset). time.RFC1123
	// is the GMT form; some servers emit a numeric offset.
	t, err := time.Parse(time.RFC1123Z, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("httpclient: parse RFC1123 date %q: %w", s, err)
	}
	return t.UTC(), nil
}

// ParseCookies splits a Cookie or Set-Cookie value list into key/value pairs.
// C# cite: HttpHeader.ParseCookies at HttpHeader.cs:192-198.
//
// Note: this is a naive parser identical in semantics to the C# version. It
// trims whitespace then splits each entry on the first '='. Entries without
// '=' panic in the C# code (`v[1]` on a 1-element split); we instead skip
// malformed entries — divergence documented because the C# behaviour is a
// latent NRE bug nobody actually relies on.
func ParseCookies(s string) []QueryPair {
	out := make([]QueryPair, 0, 4)
	for _, raw := range strings.Split(s, ";") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		idx := strings.Index(raw, "=")
		if idx < 0 {
			continue
		}
		out = append(out, QueryPair{Key: raw[:idx], Value: raw[idx+1:]})
	}
	return out
}
