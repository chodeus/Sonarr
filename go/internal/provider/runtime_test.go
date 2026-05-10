package provider

import (
	"errors"
	"strings"
	"testing"
)

// fakeRow implements Row for testing.
type fakeRow struct {
	id           int
	name         string
	impl         string
	contract     string
	enable       bool
	settingsJSON string
	tags         []int
	message      *Message
}

func (r fakeRow) GetID() int                { return r.id }
func (r fakeRow) GetName() string           { return r.name }
func (r fakeRow) GetImplementation() string { return r.impl }
func (r fakeRow) GetConfigContract() string { return r.contract }
func (r fakeRow) GetEnable() bool           { return r.enable }
func (r fakeRow) GetSettingsJSON() string   { return r.settingsJSON }
func (r fakeRow) GetTags() []int            { return r.tags }
func (r fakeRow) GetMessage() *Message      { return r.message }

// fakeSettings implements Config.
type fakeSettings struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
}

func (f *fakeSettings) Validate() ValidationResult {
	if f.BaseURL == "" {
		return ValidationResult{Errors: []ValidationFailure{NewValidationFailure("baseUrl", "required")}}
	}
	return ValidResult()
}

// nonConfigSettings does NOT implement Config — used to exercise the
// ErrSettingsNotConfig path.
type nonConfigSettings struct{}

func TestFactory_LoadDefinition_HappyPath(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(FamilyIndexer, Key{Impl: "Newznab", Contract: "NewznabSettings"}, func() any {
		return &fakeSettings{}
	})

	row := fakeRow{
		id:           42,
		name:         "Test Indexer",
		impl:         "Newznab",
		contract:     "NewznabSettings",
		enable:       true,
		settingsJSON: `{"baseUrl":"https://example.com","apiKey":"abc"}`,
		tags:         []int{1, 2},
	}
	f := NewFactory(reg)
	def, err := f.LoadDefinition(FamilyIndexer, row)
	if err != nil {
		t.Fatal(err)
	}
	if def.ID != 42 || def.Name != "Test Indexer" {
		t.Errorf("ID/Name: %d %q", def.ID, def.Name)
	}
	if def.Implementation != "Newznab" || def.ConfigContract != "NewznabSettings" {
		t.Errorf("impl/contract: %q %q", def.Implementation, def.ConfigContract)
	}
	if !def.Enable {
		t.Error("Enable")
	}
	cfg, ok := def.Settings.(*fakeSettings)
	if !ok {
		t.Fatalf("Settings type = %T", def.Settings)
	}
	if cfg.BaseURL != "https://example.com" || cfg.APIKey != "abc" {
		t.Errorf("settings = %+v", cfg)
	}
	if len(def.Tags) != 2 || def.Tags[0] != 1 || def.Tags[1] != 2 {
		t.Errorf("tags = %v", def.Tags)
	}
	if def.SettingsRaw != row.settingsJSON {
		t.Errorf("SettingsRaw lost")
	}
}

func TestFactory_LoadDefinition_NilRow(t *testing.T) {
	f := NewFactory(NewRegistry())
	_, err := f.LoadDefinition(FamilyIndexer, nil)
	if !errors.Is(err, ErrNilRow) {
		t.Errorf("err = %v want ErrNilRow", err)
	}
}

func TestFactory_LoadDefinition_EmptyImplementation(t *testing.T) {
	f := NewFactory(NewRegistry())
	_, err := f.LoadDefinition(FamilyIndexer, fakeRow{name: "broken"})
	if !errors.Is(err, ErrMissingImplementation) {
		t.Errorf("err = %v want ErrMissingImplementation", err)
	}
}

func TestFactory_LoadDefinition_UnregisteredImplKeepsRawAndAddsErrorMessage(t *testing.T) {
	// Mirrors C# RemoveMissingImplementations precondition — Factory must
	// preserve the row so the service layer can surface it; not destroy.
	reg := NewRegistry()
	row := fakeRow{
		id:           7,
		name:         "Stale",
		impl:         "VanishedClass",
		contract:     "VanishedSettings",
		enable:       true,
		settingsJSON: `{"foo":"bar"}`,
	}
	f := NewFactory(reg)
	def, err := f.LoadDefinition(FamilyIndexer, row)
	if err != nil {
		t.Fatal(err)
	}
	if def.Settings != nil {
		t.Error("Settings should be nil for unregistered impl")
	}
	if def.SettingsRaw != row.settingsJSON {
		t.Error("SettingsRaw should be preserved")
	}
	if def.Message == nil || def.Message.Type != MessageError {
		t.Errorf("expected error-severity Message, got %+v", def.Message)
	}
	if !strings.Contains(def.Message.Text, "VanishedClass") {
		t.Errorf("Message text should name the impl: %q", def.Message.Text)
	}
}

func TestFactory_LoadDefinition_RegisteredButNilConstructor(t *testing.T) {
	// Phase-1 catalog shape: Default() registers nil constructors. The
	// factory must not panic; should produce the same "missing impl"
	// shape as an unregistered key.
	reg := NewRegistry()
	reg.MustRegister(FamilyIndexer, Key{Impl: "Newznab", Contract: "NewznabSettings"}, nil)
	f := NewFactory(reg)
	def, err := f.LoadDefinition(FamilyIndexer, fakeRow{
		impl: "Newznab", contract: "NewznabSettings", settingsJSON: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if def.Settings != nil {
		t.Error("Settings should be nil for nil constructor")
	}
	if def.Message == nil || def.Message.Type != MessageError {
		t.Errorf("expected error-severity Message")
	}
}

func TestFactory_LoadDefinition_NonConfigConstructor(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(FamilyIndexer, Key{Impl: "Bad", Contract: "BadSettings"}, func() any {
		return &nonConfigSettings{}
	})
	f := NewFactory(reg)
	_, err := f.LoadDefinition(FamilyIndexer, fakeRow{
		impl: "Bad", contract: "BadSettings", settingsJSON: `{}`,
	})
	if !errors.Is(err, ErrSettingsNotConfig) {
		t.Errorf("err = %v want ErrSettingsNotConfig", err)
	}
}

func TestFactory_LoadDefinition_BadJSON(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(FamilyIndexer, Key{Impl: "Newznab", Contract: "NewznabSettings"}, func() any {
		return &fakeSettings{}
	})
	f := NewFactory(reg)
	_, err := f.LoadDefinition(FamilyIndexer, fakeRow{
		impl: "Newznab", contract: "NewznabSettings", settingsJSON: `{not valid`,
	})
	if err == nil || !strings.Contains(err.Error(), "unmarshal settings") {
		t.Errorf("expected unmarshal error, got %v", err)
	}
}

func TestFactory_LoadDefinition_EmptySettingsJSON(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(FamilyIndexer, Key{Impl: "Newznab", Contract: "NewznabSettings"}, func() any {
		return &fakeSettings{}
	})
	f := NewFactory(reg)
	def, err := f.LoadDefinition(FamilyIndexer, fakeRow{
		impl: "Newznab", contract: "NewznabSettings",
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := def.Settings.(*fakeSettings)
	if !ok {
		t.Fatalf("Settings type = %T", def.Settings)
	}
	if cfg.BaseURL != "" {
		t.Errorf("expected zero-value Settings, got %+v", cfg)
	}
}

func TestFactory_LoadDefinition_TagsDefaultEmpty(t *testing.T) {
	reg := NewRegistry()
	reg.MustRegister(FamilyIndexer, Key{Impl: "Newznab", Contract: "NewznabSettings"}, func() any {
		return &fakeSettings{}
	})
	f := NewFactory(reg)
	def, err := f.LoadDefinition(FamilyIndexer, fakeRow{
		impl: "Newznab", contract: "NewznabSettings",
	})
	if err != nil {
		t.Fatal(err)
	}
	if def.Tags == nil || len(def.Tags) != 0 {
		t.Errorf("Tags = %v want []int{}", def.Tags)
	}
}

func TestMarshalSettings_RoundTrip(t *testing.T) {
	def := &Definition{
		Implementation: "Newznab",
		ConfigContract: "NewznabSettings",
		Settings:       &fakeSettings{BaseURL: "https://x", APIKey: "k"},
	}
	if err := MarshalSettings(def); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def.SettingsRaw, `"baseUrl":"https://x"`) {
		t.Errorf("SettingsRaw missing baseUrl: %q", def.SettingsRaw)
	}
}

func TestMarshalSettings_NilSettingsLeavesRawAlone(t *testing.T) {
	def := &Definition{
		SettingsRaw: `{"preserve":"me"}`,
	}
	if err := MarshalSettings(def); err != nil {
		t.Fatal(err)
	}
	if def.SettingsRaw != `{"preserve":"me"}` {
		t.Errorf("SettingsRaw was clobbered: %q", def.SettingsRaw)
	}
}

func TestMarshalSettings_Nil(t *testing.T) {
	if err := MarshalSettings(nil); !errors.Is(err, ErrNilDefinition) {
		t.Errorf("err = %v want ErrNilDefinition", err)
	}
}

// ValidationResult / ValidationFailure tests
func TestValidationResult_IsValid(t *testing.T) {
	cases := []struct {
		name  string
		fails []ValidationFailure
		valid bool
	}{
		{"empty", nil, true},
		{"only warnings", []ValidationFailure{NewValidationWarning("p", "warn")}, true},
		{"one error", []ValidationFailure{NewValidationFailure("p", "err")}, false},
		{"warning + error", []ValidationFailure{NewValidationWarning("p", "warn"), NewValidationFailure("q", "err")}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ValidationResult{Errors: c.fails}
			if r.IsValid() != c.valid {
				t.Errorf("IsValid() = %v want %v", r.IsValid(), c.valid)
			}
		})
	}
}

func TestValidationResult_HasWarnings(t *testing.T) {
	r := ValidationResult{Errors: []ValidationFailure{NewValidationWarning("p", "warn")}}
	if !r.HasWarnings() {
		t.Error("expected HasWarnings true")
	}
	r2 := ValidationResult{Errors: []ValidationFailure{NewValidationFailure("p", "err")}}
	if r2.HasWarnings() {
		t.Error("expected HasWarnings false for error-only")
	}
}

func TestValidationResult_String(t *testing.T) {
	r := ValidResult()
	if r.String() != "valid" {
		t.Errorf("empty result = %q", r.String())
	}
	r2 := ValidationResult{Errors: []ValidationFailure{
		NewValidationFailure("baseUrl", "required"),
		NewValidationWarning("apiKey", "weak"),
	}}
	got := r2.String()
	if !strings.Contains(got, "baseUrl: required") {
		t.Errorf("missing baseUrl: %q", got)
	}
	if !strings.Contains(got, "warning: apiKey") {
		t.Errorf("missing warning: %q", got)
	}
}

func TestMessageType_String(t *testing.T) {
	cases := map[MessageType]string{
		MessageInfo:    "info",
		MessageWarning: "warning",
		MessageError:   "error",
	}
	for typ, want := range cases {
		if got := typ.String(); got != want {
			t.Errorf("MessageType(%d).String() = %q want %q", typ, got, want)
		}
	}
}

func TestMessageConstructors(t *testing.T) {
	if NewInfoMessage("x").Type != MessageInfo {
		t.Error("NewInfoMessage type")
	}
	if NewWarningMessage("x").Type != MessageWarning {
		t.Error("NewWarningMessage type")
	}
	if NewErrorMessage("x").Type != MessageError {
		t.Error("NewErrorMessage type")
	}
}
