package aria2

import (
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// XML-RPC encoder + decoder for aria2c's RPC wire shape. Aria2
// speaks the standard XML-RPC 1.0 spec with the addition of
// `<struct>` value types (rTorrent's slice didn't need these — it
// only emitted scalar response shapes). The encoder here supports:
//
//   - <string>          — token strings, magnet URLs, GIDs.
//   - <int>             — offset / num args on tellWaiting / tellStopped.
//   - <base64>          — .torrent file bytes.
//   - <array><data>     — uri-lists, args-list.
//   - <struct>          — options dicts (dir → /downloads, etc).
//
// Decoder handles all the shapes the proxy reads back, including
// nested arrays + structs (aria2.tellStatus returns a struct
// containing nested file lists, etc).

// methodCall is the XML-RPC request envelope.
type methodCall struct {
	XMLName    xml.Name        `xml:"methodCall"`
	MethodName string          `xml:"methodName"`
	Params     methodCallParam `xml:"params"`
}

type methodCallParam struct {
	Param []callParam `xml:"param"`
}

type callParam struct {
	XMLName xml.Name `xml:"param"`
	Value   callVal  `xml:"value"`
}

// callVal is the union shape — exactly one field is set per
// encoded value.
type callVal struct {
	String *string     `xml:"string,omitempty"`
	Int    *int        `xml:"int,omitempty"`
	Base64 *string     `xml:"base64,omitempty"`
	Array  *callArray  `xml:"array,omitempty"`
	Struct *callStruct `xml:"struct,omitempty"`
}

type callArray struct {
	Data callArrayData `xml:"data"`
}

type callArrayData struct {
	Value []callVal `xml:"value"`
}

type callStruct struct {
	Member []callMember `xml:"member"`
}

type callMember struct {
	Name  string  `xml:"name"`
	Value callVal `xml:"value"`
}

// encodeCall builds the XML-RPC methodCall body bytes. Supports
// string / int / int64 / []byte / []string / map[string]string.
// Unsupported types surface as a typed error.
func encodeCall(method string, args ...any) ([]byte, error) {
	call := methodCall{MethodName: method}
	for i, raw := range args {
		v, err := encodeArg(raw)
		if err != nil {
			return nil, fmt.Errorf("aria2: encode arg %d (%T): %w", i, raw, err)
		}
		call.Params.Param = append(call.Params.Param, callParam{Value: v})
	}
	body, err := xml.Marshal(&call)
	if err != nil {
		return nil, fmt.Errorf("aria2: marshal methodCall: %w", err)
	}
	return append([]byte(xml.Header), body...), nil
}

func encodeArg(raw any) (callVal, error) {
	switch v := raw.(type) {
	case string:
		return callVal{String: &v}, nil
	case int:
		i := v
		return callVal{Int: &i}, nil
	case int32:
		i := int(v)
		return callVal{Int: &i}, nil
	case int64:
		i := int(v)
		return callVal{Int: &i}, nil
	case []byte:
		s := base64.StdEncoding.EncodeToString(v)
		return callVal{Base64: &s}, nil
	case []string:
		arr := callArray{}
		for _, s := range v {
			s := s
			arr.Data.Value = append(arr.Data.Value, callVal{String: &s})
		}
		return callVal{Array: &arr}, nil
	case map[string]string:
		st := callStruct{}
		// Stable iteration order so encoded body is deterministic
		// (matters for body-shape assertions in tests).
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			val := v[k]
			st.Member = append(st.Member, callMember{
				Name:  k,
				Value: callVal{String: &val},
			})
		}
		return callVal{Struct: &st}, nil
	default:
		return callVal{}, fmt.Errorf("unsupported arg type %T", raw)
	}
}

// sortStrings inlines a small sort to avoid the sort import for
// one call site.
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// methodResponse is the XML-RPC response envelope.
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

// rawValue captures any of the response value-typed children.
type rawValue struct {
	String  *string    `xml:"string,omitempty"`
	Int     *int       `xml:"int,omitempty"`
	I4      *int       `xml:"i4,omitempty"`
	I8      *int64     `xml:"i8,omitempty"`
	Boolean *int       `xml:"boolean,omitempty"`
	Double  *float64   `xml:"double,omitempty"`
	Array   *rawArray  `xml:"array,omitempty"`
	Struct  *rawStruct `xml:"struct,omitempty"`
}

type rawArray struct {
	Data rawArrayData `xml:"data"`
}

type rawArrayData struct {
	Value []rawValue `xml:"value"`
}

type rawStruct struct {
	Member []rawMember `xml:"member"`
}

type rawMember struct {
	Name  string   `xml:"name"`
	Value rawValue `xml:"value"`
}

// responseFault is the XML-RPC <fault> envelope.
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

// FaultError represents a non-zero XML-RPC fault returned by aria2.
type FaultError struct {
	Code    int
	Message string
}

// Error renders the human-readable form used in log lines.
func (e *FaultError) Error() string {
	return fmt.Sprintf("aria2: fault %d: %s", e.Code, e.Message)
}

// ErrEmptyResponse is returned when the response body is well-formed
// XML-RPC but contains no <params> and no <fault>.
var ErrEmptyResponse = errors.New("aria2: empty methodResponse (neither params nor fault)")

// decodeResponse parses the XML-RPC methodResponse body and surfaces
// either the first param's value or a typed [FaultError].
func decodeResponse(body []byte) (*rawValue, error) {
	var resp methodResponse
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("aria2: unmarshal methodResponse: %w", err)
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
		if n, err := strconv.Atoi(strings.TrimSpace(*v.String)); err == nil {
			return n
		}
	}
	return 0
}

// structField returns the value of the named struct member, or
// (zero, false) when the response isn't a struct or doesn't have
// that member.
func (v rawValue) structField(name string) (rawValue, bool) {
	if v.Struct == nil {
		return rawValue{}, false
	}
	for _, m := range v.Struct.Member {
		if m.Name == name {
			return m.Value, true
		}
	}
	return rawValue{}, false
}

// arrayElements returns the array's row slice or nil for non-arrays.
// Unused in slice 5.J51 — wired now so the GetItems follow-up can
// consume aria2.tellActive's array-of-structs response without
// re-touching xmlrpc.go.
//
//nolint:unused // wired for the GetItems follow-up slice
func (v rawValue) arrayElements() []rawValue {
	if v.Array == nil {
		return nil
	}
	return v.Array.Data.Value
}
