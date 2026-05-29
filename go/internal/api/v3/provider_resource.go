package v3

import (
	"encoding/json"
	"sort"
	"strings"
)

// providerResourceCore carries the inherited fields from
// `src/Sonarr.Api.V3/ProviderResource.cs` that every provider list
// endpoint emits (Notifications, Indexers, ImportLists,
// DownloadClients, Metadata). Each concrete resource embeds this
// core and adds its own per-controller fields.
//
// Empty-DB lists never trigger the populated-row mapper, so the
// minimal-fields stub below is byte-eq-safe today. Populated rows
// will land their full wire shape when the schema-builder port adds
// Fields/Presets/Message support — DEFERRED, flagged in STATUS.
//
// Field-source mapping (C# → Go), shared base:
//
//	id                 → ProviderDefinition.Id
//	name               → ProviderDefinition.Name
//	implementation     → ProviderDefinition.Implementation
//	implementationName → ProviderDefinition.ImplementationName
//	configContract     → ProviderDefinition.ConfigContract
//	tags               → ProviderDefinition.Tags (HashSet<int> → []int{})
//	fields             → SchemaBuilder.ToSchema(Settings) — DEFERRED
//	infoLink           → ProviderDefinition.InfoLink — DEFERRED
//	message            → ProviderDefinition.Message — DEFERRED
//	presets            → ProviderDefinition.Presets — DEFERRED
type providerResourceCore struct {
	ID                 int    `json:"id"`
	Name               string `json:"name"`
	Implementation     string `json:"implementation"`
	ImplementationName string `json:"implementationName"`
	ConfigContract     string `json:"configContract"`
	Tags               []int  `json:"tags"`
}

// ProviderField is one entry in a provider resource's `fields[]`
// array — the C# wire shape produced by `SchemaBuilder.ToSchema(Settings)`
// at `src/NzbDrone.Core/Annotations/FieldDefinition.cs`. The Go port
// only round-trips the (Name, Value) pair right now; type/label/help-
// text/select-options come from the C# annotations on the Settings
// class and are DEFERRED until the schema-builder ports.
//
// On POST/PUT this is the input the handler consumes — the
// per-implementation Settings JSON is reconstructed as a
// `map[Name]Value` and stored in the `Settings` DB column. On GET the
// inverse is currently a no-op (empty array) so the round-trip is
// asymmetric — flagged in STATUS as a follow-up alongside the
// schema-builder port.
type ProviderField struct {
	Name  string `json:"name"`
	Value any    `json:"value,omitempty"`
}

// settingsJSONFromFields packs a `fields[]` array (POST/PUT input)
// into the JSON object the DB row's `Settings` column stores. Empty
// `fields[]` produces "{}" (matches C# default `Settings.ToJson()`
// for a settings class with no populated properties).
func settingsJSONFromFields(fields []ProviderField) (string, error) {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if f.Name == "" {
			continue
		}
		out[f.Name] = f.Value
	}
	buf, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// fieldsFromSettingsJSON unpacks a DB `Settings` JSON object into the
// `fields[]` GET projection — the inverse of [settingsJSONFromFields]
// (G4/J425), closing the previously-asymmetric provider round-trip
// (POST consumed fields[] but GET emitted nothing). Fields are sorted by
// name for deterministic output; the React UI merges instance fields
// with the schema by name, so order is cosmetic and the reduced
// {name,value} shape (no type/label — those come from the schema
// endpoint) is the established Go-port ProviderField contract. An empty
// / "{}" / unparseable settings string yields nil → omitempty drops the
// key (matching the pre-G4 shape for setting-less rows, so empty-DB
// contract goldens are unaffected).
func fieldsFromSettingsJSON(settingsJSON string) []ProviderField {
	s := strings.TrimSpace(settingsJSON)
	if s == "" || s == "{}" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil || len(m) == 0 {
		return nil
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]ProviderField, 0, len(names))
	for _, k := range names {
		out = append(out, ProviderField{Name: k, Value: m[k]})
	}
	return out
}

// derefSettings returns the string value of a nullable Settings column.
func derefSettings(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
