package rtorrent

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Minimal XML-RPC encoder + decoder for rTorrent's methodCall /
// methodResponse wire shape.
//
// rTorrent / libtorrent-rakshasa speaks the XML-RPC 1.0 spec
// (Userland, 1999) over HTTP. The subset of value types Sonarr's
// rTorrent code touches:
//
//   - <string>       — labels, hashes, names, paths.
//   - <int>/<i4>/<i8>— priority, sizes, timestamps.
//   - <base64>       — .torrent file bytes (AddTorrentFromFile).
//   - <array><data>  — return shape of d.multicall.
//   - <struct>       — fault envelopes only.
//
// The encoder produces only <string>, <int>, and <base64> arg types
// — the wire-direction shapes Sonarr CALLS rTorrent with. Decoding
// consumes the response shapes rTorrent CALLS BACK with (which
// includes arrays for d.multicall — handled in a follow-up slice
// once GetItems lands).

// methodCall is the XML-RPC request envelope. Encoded to bytes via
// encoding/xml.
type methodCall struct {
	XMLName    xml.Name        `xml:"methodCall"`
	MethodName string          `xml:"methodName"`
	Params     methodCallParam `xml:"params"`
}

type methodCallParam struct {
	Param []xmlValue `xml:"param"`
}

// xmlValue carries one <param><value>...</value></param> entry. We
// only encode <string>, <int>, and <base64> shapes — the three the
// Sonarr-side calls use. Exactly one of these fields is populated
// per encoded value; the rest stay zero so encoding/xml skips them.
type xmlValue struct {
	XMLName xml.Name `xml:"param"`
	Value   xmlVal   `xml:"value"`
}

type xmlVal struct {
	String string  `xml:"string,omitempty"`
	Int    *int    `xml:"int,omitempty"`
	Base64 *string `xml:"base64,omitempty"`
}

// encodeCall builds the XML-RPC methodCall body bytes. Args are
// reflected to <string>, <int>, or <base64> per Go type:
//
//   - string             → <string>
//   - int / int32 / int64→ <int>
//   - []byte             → <base64>
//
// Returns an error for unsupported arg types so a future caller
// hitting an unmodeled shape gets a clear failure at the boundary
// rather than a silently-malformed request.
func encodeCall(method string, args ...any) ([]byte, error) {
	call := methodCall{MethodName: method}
	for i, raw := range args {
		val, err := encodeArg(raw)
		if err != nil {
			return nil, fmt.Errorf("rtorrent: encode arg %d (%T): %w", i, raw, err)
		}
		call.Params.Param = append(call.Params.Param, xmlValue{Value: val})
	}
	body, err := xml.Marshal(&call)
	if err != nil {
		return nil, fmt.Errorf("rtorrent: marshal methodCall: %w", err)
	}
	// Prepend the XML declaration — most rTorrent installs are
	// permissive but some scgi-fronted setups reject bodies without
	// the prolog.
	return append([]byte(xml.Header), body...), nil
}

func encodeArg(raw any) (xmlVal, error) {
	switch v := raw.(type) {
	case string:
		return xmlVal{String: v}, nil
	case int:
		i := v
		return xmlVal{Int: &i}, nil
	case int32:
		i := int(v)
		return xmlVal{Int: &i}, nil
	case int64:
		i := int(v)
		return xmlVal{Int: &i}, nil
	case []byte:
		// XML-RPC base64 uses standard MIME base64 (no URL-safe
		// alphabet, no padding stripping). encoding/xml's
		// `,chardata` mode would emit the bytes verbatim; we
		// instead pre-encode to standard base64 and emit as a
		// string field.
		encoded := stdBase64Encode(v)
		return xmlVal{Base64: &encoded}, nil
	default:
		return xmlVal{}, fmt.Errorf("unsupported arg type %T (only string / int / []byte are encoded)", raw)
	}
}

// stdBase64Encode returns standard MIME base64 (no URL-safe
// alphabet, no padding stripping). XML-RPC's <base64> spec uses
// this encoding exactly.
func stdBase64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// methodResponse is the XML-RPC response envelope. The parser uses
// loose decoding (Params + Fault are optional; the live response
// has exactly one).
type methodResponse struct {
	XMLName xml.Name        `xml:"methodResponse"`
	Params  *responseParams `xml:"params,omitempty"`
	Fault   *responseFault  `xml:"fault,omitempty"`
}

type responseParams struct {
	Param []responseValue `xml:"param"`
}

type responseValue struct {
	Value rawValue `xml:"value"`
}

// rawValue captures any of the value-typed children. The decoder
// reads each tag — only one is populated per response.
type rawValue struct {
	String  *string   `xml:"string,omitempty"`
	Int     *int      `xml:"int,omitempty"`
	I4      *int      `xml:"i4,omitempty"`
	I8      *int64    `xml:"i8,omitempty"`
	Boolean *int      `xml:"boolean,omitempty"`
	Array   *rawArray `xml:"array,omitempty"`
}

// rawArray captures <array><data><value>...</value>...</data></array>.
// d.multicall.2 returns a nested array (outer = per-torrent rows;
// each inner = the projected column values per row).
type rawArray struct {
	Data rawArrayData `xml:"data"`
}

type rawArrayData struct {
	Value []rawValue `xml:"value"`
}

// elements returns the array's row slice or nil for non-arrays.
func (v rawValue) elements() []rawValue {
	if v.Array == nil {
		return nil
	}
	return v.Array.Data.Value
}

// int64Value returns the i8/i4/int/boolean as an int64. Falls back
// to int64 parse on string responses to tolerate the older rTorrent
// builds that emit numerics as <string>.
func (v rawValue) int64Value() int64 {
	switch {
	case v.I8 != nil:
		return *v.I8
	case v.Int != nil:
		return int64(*v.Int)
	case v.I4 != nil:
		return int64(*v.I4)
	case v.Boolean != nil:
		return int64(*v.Boolean)
	case v.String != nil:
		if n, err := strconv.ParseInt(strings.TrimSpace(*v.String), 10, 64); err == nil {
			return n
		}
	}
	return 0
}

// responseFault is the XML-RPC <fault> envelope. faultCode +
// faultString are the only two members rTorrent surfaces.
type responseFault struct {
	Value faultStruct `xml:"value"`
}

type faultStruct struct {
	Struct struct {
		Members []struct {
			Name  string   `xml:"name"`
			Value rawValue `xml:"value"`
		} `xml:"member"`
	} `xml:"struct"`
}

// FaultError represents a non-zero XML-RPC fault returned by
// rTorrent. The C# tree raises DownloadClientException; the Go
// port surfaces a typed error so callers can match on it.
type FaultError struct {
	Code    int
	Message string
}

// Error renders the human-readable form used in log lines.
func (e *FaultError) Error() string {
	return fmt.Sprintf("rtorrent: fault %d: %s", e.Code, e.Message)
}

// ErrEmptyResponse is returned when the response body is well-formed
// XML-RPC but contains no <params> and no <fault> — a degenerate
// shape some older rTorrent builds emit on void RPCs. Callers
// expecting a return value should treat this as a failure; void
// callers can ignore it.
var ErrEmptyResponse = errors.New("rtorrent: empty methodResponse (neither params nor fault)")

// decodeResponse parses the XML-RPC methodResponse body and surfaces
// either the first param's value (when present) or a typed
// [FaultError]. Returns ErrEmptyResponse when neither envelope is
// present.
func decodeResponse(body []byte) (*rawValue, error) {
	var resp methodResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("rtorrent: unmarshal methodResponse: %w", err)
	}
	if resp.Fault != nil {
		code, msg := resp.Fault.faultFields()
		return nil, &FaultError{Code: code, Message: msg}
	}
	if resp.Params == nil || len(resp.Params.Param) == 0 {
		return nil, ErrEmptyResponse
	}
	v := resp.Params.Param[0].Value
	return &v, nil
}

func (f *responseFault) faultFields() (code int, msg string) {
	for _, m := range f.Value.Struct.Members {
		switch m.Name {
		case "faultCode":
			code = m.Value.intValue()
		case "faultString":
			msg = strings.TrimSpace(m.Value.stringValue())
		}
	}
	return code, msg
}

func (v rawValue) stringValue() string {
	if v.String != nil {
		return *v.String
	}
	return ""
}

func (v rawValue) intValue() int {
	switch {
	case v.Int != nil:
		return *v.Int
	case v.I4 != nil:
		return *v.I4
	case v.I8 != nil:
		return int(*v.I8)
	case v.Boolean != nil:
		return *v.Boolean
	case v.String != nil:
		// Some rTorrent builds emit the integer as a <string> in
		// the methodResponse — tolerate by parsing.
		if n, err := strconv.Atoi(strings.TrimSpace(*v.String)); err == nil {
			return n
		}
	}
	return 0
}
