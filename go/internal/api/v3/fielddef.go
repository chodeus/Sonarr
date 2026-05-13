package v3

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
)

// FieldDef is the wire-shape POCO that descibes one input field on
// a provider/spec schema endpoint. Mirrors
// `src/Sonarr.Http/ClientSchema/Field.cs` exactly — every field on
// the C# class lands here verbatim. Used by the schema endpoints
// (`/api/v3/customformat/schema`, `/api/v3/autotagging/schema`, the
// provider-schema endpoints under indexer/downloadclient/etc.) plus
// every populated provider list emit (each list item carries a
// resolved fields[] array).
//
// JSON shape quirks the wire requires:
//
//   - String fields default to null in C# and System.Text.Json
//     omits null via WhenWritingNull. Modeled here as `string`
//     with `,omitempty`; this matches except for explicit empty
//     strings which the C# side doesn't emit either.
//   - Int/bool fields default to 0/false in C# and emit
//     (WhenWritingNull doesn't fire for value types). Modeled as
//     `int` / `bool` without omitempty — the wire ALWAYS shows
//     Order, Advanced, Privacy, IsFloat.
//   - `Value` is the tricky one. In C# it's `object` which holds
//     either an int (select/checkbox), bool (checkbox), float
//     (number), or null (textbox with no default). null → omitted
//     by WhenWritingNull; everything else emits even when zero.
//     Modeled here as `any` plus a `ValueSet bool` discriminator —
//     the custom MarshalJSON below emits `value` only when
//     ValueSet=true. Callers building schema tables set the field
//     via the `WithValue(x)` constructor helper for ergonomic call
//     sites; bare struct literals omit the value automatically.
//   - SelectOptions is `null` → omitted when empty. Modeled as
//     []SelectOption with omitempty so an empty slice doesn't emit.
type FieldDef struct {
	Order                       int            `json:"order"`
	Name                        string         `json:"name"`
	Label                       string         `json:"label"`
	Unit                        string         `json:"unit,omitempty"`
	HelpText                    string         `json:"helpText,omitempty"`
	HelpTextWarning             string         `json:"helpTextWarning,omitempty"`
	HelpLink                    string         `json:"helpLink,omitempty"`
	Value                       any            `json:"-"`
	ValueSet                    bool           `json:"-"`
	Type                        string         `json:"type"`
	Advanced                    bool           `json:"advanced"`
	SelectOptions               []SelectOption `json:"selectOptions,omitempty"`
	SelectOptionsProviderAction string         `json:"selectOptionsProviderAction,omitempty"`
	Section                     string         `json:"section,omitempty"`
	Hidden                      string         `json:"hidden,omitempty"`
	Privacy                     string         `json:"privacy"`
	Placeholder                 string         `json:"placeholder,omitempty"`
	IsFloat                     bool           `json:"isFloat"`
}

// WithValue returns a copy of f with Value set + ValueSet=true.
// Use at construction time to attach a default value to a field;
// without it the value key is omitted from the wire (matching the
// C# `null` default for textbox/string fields).
func (f FieldDef) WithValue(v any) FieldDef {
	f.Value = v
	f.ValueSet = true
	return f
}

// MarshalJSON is hand-rolled so the `value` key emits ONLY when
// ValueSet=true. Standard struct marshaling can't distinguish "field
// is the zero value" from "field is intentionally absent" — the C#
// `object Value` + WhenWritingNull semantic requires that
// distinction.
//
// Implementation: marshal the struct via an alias (to skip
// MarshalJSON recursion), then surgically inject `"value":…` before
// the closing brace when ValueSet=true. Cheaper than building a
// map[string]any and re-marshaling; the splice point is the only
// position-dependent edit needed.
func (f FieldDef) MarshalJSON() ([]byte, error) {
	type alias FieldDef
	base, err := json.Marshal(alias(f))
	if err != nil {
		return nil, err
	}
	if !f.ValueSet {
		return base, nil
	}
	v, err := json.Marshal(f.Value)
	if err != nil {
		return nil, err
	}
	// Insert `,"value":<v>` right before the closing brace. The
	// alias type's tags leave `-` on Value+ValueSet, so neither
	// shows up in `base` — appending `,"value":…` keeps the key
	// alphabetization the contract-test normalizer applies anyway.
	if len(base) < 2 || base[len(base)-1] != '}' {
		return nil, errors.New("fielddef: malformed alias marshal")
	}
	var out bytes.Buffer
	out.Grow(len(base) + len(v) + 10)
	out.Write(base[:len(base)-1])
	if len(base) > 2 {
		out.WriteByte(',')
	}
	out.WriteString(`"value":`)
	out.Write(v)
	out.WriteByte('}')
	return out.Bytes(), nil
}

// SelectOption is one entry inside FieldDef.SelectOptions. Mirrors
// `src/NzbDrone.Core/Annotations/SelectOption.cs` verbatim — Value
// + Name + Order + Hint.
type SelectOption struct {
	Value int    `json:"value"`
	Name  string `json:"name"`
	Order int    `json:"order"`
	Hint  string `json:"hint,omitempty"`
}

// fieldDefValueAsInt is a small helper that's useful when echoing a
// FieldDef value back into a wire-shape assertion — strconv-style
// since the test rig only inspects field counts/keys, not the
// numeric value.
//
// nolint:unused // exported via _ for the schema-endpoint follow-up
// slices that round-trip integer Value fields through the wire.
func fieldDefValueAsInt(v any) string {
	switch x := v.(type) {
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}

var _ = fieldDefValueAsInt // future-use suppressor
