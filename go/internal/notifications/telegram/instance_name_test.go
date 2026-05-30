package telegram

import (
	"encoding/json"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
)

// stubNamer satisfies the instanceNamer surface a
// Dependencies.ConfigProvider must expose for the instance-name suffix.
type stubNamer struct{ name string }

func (s stubNamer) InstanceName() string { return s.name }

// TestInstanceNameFromConfigProvider is the J460 pin for Telegram: the
// instance name supplied via Dependencies.ConfigProvider must surface
// in the per-event title suffix when IncludeInstanceNameInTitle is set
// (C# Telegram.cs:23,28 — `title - {_configFileProvider.InstanceName}`).
// Before J460 the binary wired the app-config config.Provider (no
// InstanceName accessor) into deps.ConfigProvider, so the suffix never
// appeared. This is an internal test because applyBrandAndInstance and
// the instanceName field are unexported; it avoids the network entirely
// (no shared-var override) while exercising the New() → instanceName →
// title path end to end.
func TestInstanceNameFromConfigProvider(t *testing.T) {
	t.Parallel()
	settings, err := json.Marshal(Settings{
		BotToken:                   "token",
		ChatId:                     "1",
		IncludeInstanceNameInTitle: true,
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	def := &notifications.Notification{
		Implementation: Implementation,
		Settings:       string(settings),
	}
	p, err := New(def, notifications.Dependencies{ConfigProvider: stubNamer{name: "MyInstance"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tg, ok := p.(*Telegram)
	if !ok {
		t.Fatalf("New returned %T, want *Telegram", p)
	}
	got := tg.applyBrandAndInstance("Episode Grabbed")
	if want := "Episode Grabbed - MyInstance"; got != want {
		t.Errorf("title suffix: got %q, want %q (instance name from ConfigProvider not wired)", got, want)
	}
}

// TestInstanceName_NoConfigProvider verifies the suffix is omitted when
// no ConfigProvider is wired — the instanceName is empty and
// applyBrandAndInstance must not append a trailing " - " (C# guards the
// same with a non-null InstanceName; the Go port guards on
// instanceName != "").
func TestInstanceName_NoConfigProvider(t *testing.T) {
	t.Parallel()
	settings, err := json.Marshal(Settings{
		BotToken:                   "token",
		ChatId:                     "1",
		IncludeInstanceNameInTitle: true,
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	def := &notifications.Notification{Implementation: Implementation, Settings: string(settings)}
	p, err := New(def, notifications.Dependencies{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tg := p.(*Telegram)
	if got := tg.applyBrandAndInstance("Episode Grabbed"); strings.Contains(got, " - ") {
		t.Errorf("title got %q, want no instance suffix when ConfigProvider absent", got)
	}
}
