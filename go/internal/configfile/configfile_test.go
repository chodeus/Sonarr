package configfile_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"sonarr.tv/sonarr/internal/configfile"
	"sonarr.tv/sonarr/internal/messaging"
)

func newLoader(t *testing.T) *configfile.Loader {
	t.Helper()
	dir := t.TempDir()
	return configfile.NewLoader(filepath.Join(dir, "config.xml"))
}

func mustLoad(t *testing.T, l *configfile.Loader) {
	t.Helper()
	if err := l.Load(t.Context()); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

// stableAPIKey returns a deterministic generator (test-only).
func stableAPIKey(s string) configfile.APIKeyGenerator {
	return func() (string, error) { return s, nil }
}

func TestLoader_MissingFileLoadsEmpty(t *testing.T) {
	t.Parallel()
	l := newLoader(t)
	mustLoad(t, l)
	p := configfile.NewProvider(l, configfile.Overrides{}, nil, nil)
	v, err := p.String(t.Context(), "Anything", "fallback", false)
	if err != nil {
		t.Fatalf("String: %v", err)
	}
	if v != "fallback" {
		t.Errorf("missing key did not fall through: %q", v)
	}
}

func TestLoader_RoundTrip(t *testing.T) {
	t.Parallel()
	l := newLoader(t)
	mustLoad(t, l)
	p := configfile.NewProvider(l, configfile.Overrides{}, nil, nil)

	if err := p.Set(t.Context(), "BindAddress", "0.0.0.0"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := p.Set(t.Context(), "Port", "8989"); err != nil {
		t.Fatalf("Set Port: %v", err)
	}

	// Re-load through a fresh Loader to confirm disk persistence.
	l2 := configfile.NewLoader(l.Path())
	if err := l2.Load(t.Context()); err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	p2 := configfile.NewProvider(l2, configfile.Overrides{}, nil, nil)
	if v, _ := p2.String(t.Context(), "BindAddress", "*", false); v != "0.0.0.0" {
		t.Errorf("BindAddress round-trip: %q", v)
	}
	if v, _ := p2.Int(t.Context(), "Port", 0, false); v != 8989 {
		t.Errorf("Port round-trip: %d", v)
	}
}

func TestLoader_PersistOnDefaultRead(t *testing.T) {
	t.Parallel()
	l := newLoader(t)
	mustLoad(t, l)
	p := configfile.NewProvider(l, configfile.Overrides{}, nil, nil)

	// First read with persist=true writes the default to disk.
	if v, _ := p.String(t.Context(), "BindAddress", "*", true); v != "*" {
		t.Errorf("default read: %q", v)
	}
	contents, err := os.ReadFile(l.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(contents), "<BindAddress>*</BindAddress>") {
		t.Errorf("default not persisted:\n%s", contents)
	}

	// persist=false on a fresh key does NOT write.
	if v, _ := p.String(t.Context(), "Theme", "auto", false); v != "auto" {
		t.Errorf("persist=false read: %q", v)
	}
	contents2, _ := os.ReadFile(l.Path())
	if strings.Contains(string(contents2), "<Theme>") {
		t.Errorf("persist=false wrote anyway:\n%s", contents2)
	}
}

func TestProvider_OverrideWinsOverXML(t *testing.T) {
	t.Parallel()
	l := newLoader(t)
	mustLoad(t, l)
	p := configfile.NewProvider(l, configfile.Overrides{}, nil, nil)
	if err := p.Set(t.Context(), "Port", "1111"); err != nil {
		t.Fatalf("Set Port: %v", err)
	}
	port := 9999
	pOverride := configfile.NewProvider(l, configfile.Overrides{Port: &port}, nil, nil)
	got, err := pOverride.Port(t.Context())
	if err != nil {
		t.Fatalf("Port: %v", err)
	}
	if got != 9999 {
		t.Errorf("override did not win: got %d, want 9999", got)
	}
}

func TestProvider_BindAddressEmptyOverrideUsesDefault(t *testing.T) {
	t.Parallel()
	l := newLoader(t)
	mustLoad(t, l)
	empty := ""
	p := configfile.NewProvider(l, configfile.Overrides{BindAddress: &empty}, nil, nil)
	got, err := p.BindAddress(t.Context())
	if err != nil {
		t.Fatalf("BindAddress: %v", err)
	}
	if got != "*" {
		t.Errorf("empty override did not fall back: %q", got)
	}
}

func TestProvider_APIKeyAutoGenerates(t *testing.T) {
	t.Parallel()
	l := newLoader(t)
	mustLoad(t, l)
	p := configfile.NewProvider(l, configfile.Overrides{}, nil, stableAPIKey("test-fixed-apikey"))

	first, err := p.APIKey(t.Context())
	if err != nil {
		t.Fatalf("APIKey: %v", err)
	}
	if first != "test-fixed-apikey" {
		t.Errorf("APIKey generated: %q", first)
	}
	// Persisted to disk so a fresh Provider reads the same value.
	l2 := configfile.NewLoader(l.Path())
	if err := l2.Load(t.Context()); err != nil {
		t.Fatalf("Load 2: %v", err)
	}
	p2 := configfile.NewProvider(l2, configfile.Overrides{}, nil, stableAPIKey("DIFFERENT-VALUE"))
	second, err := p2.APIKey(t.Context())
	if err != nil {
		t.Fatalf("APIKey 2: %v", err)
	}
	if second != "test-fixed-apikey" {
		t.Errorf("persisted APIKey changed on read: %q", second)
	}
}

func TestProvider_ResetAPIKeyRotates(t *testing.T) {
	t.Parallel()
	l := newLoader(t)
	mustLoad(t, l)
	var counter int32
	gen := configfile.APIKeyGenerator(func() (string, error) {
		n := atomic.AddInt32(&counter, 1)
		return "key-" + string('A'+n-1), nil
	})
	p := configfile.NewProvider(l, configfile.Overrides{}, nil, gen)

	first, _ := p.APIKey(t.Context())
	rotated, err := p.ResetAPIKey(t.Context())
	if err != nil {
		t.Fatalf("ResetAPIKey: %v", err)
	}
	if first == rotated {
		t.Errorf("Reset produced same key: %q", rotated)
	}
	current, _ := p.APIKey(t.Context())
	if current != rotated {
		t.Errorf("APIKey after Reset = %q, want %q", current, rotated)
	}
}

func TestProvider_URLBaseNormalisation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stored, want string
	}{
		{"", ""},
		{"/", ""},
		{"//", ""},
		{"sonarr", "/sonarr"},
		{"/sonarr", "/sonarr"},
		{"/sonarr/", "/sonarr"},
		{"sonarr/", "/sonarr"},
		{"a/b/c", "/a/b/c"},
	}
	for _, c := range cases {
		l := configfile.NewLoader(filepath.Join(t.TempDir(), "config.xml"))
		mustLoad(t, l)
		p := configfile.NewProvider(l, configfile.Overrides{}, nil, nil)
		if c.stored != "" {
			_ = p.Set(t.Context(), "UrlBase", c.stored)
		}
		got, err := p.URLBase(t.Context())
		if err != nil {
			t.Fatalf("URLBase(%q): %v", c.stored, err)
		}
		if got != c.want {
			t.Errorf("URLBase(stored=%q) = %q, want %q", c.stored, got, c.want)
		}
	}
}

func TestProvider_InstanceNameClamp(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stored, want string
	}{
		{"", "Sonarr"},
		{"Sonarr", "Sonarr"},
		{"Sonarr 4K", "Sonarr 4K"},  // starts with AppName
		{"My Sonarr", "My Sonarr"},  // ends with AppName
		{"My Custom App", "Sonarr"}, // neither → fallback
		{"Sonarr-but-not", "Sonarr-but-not"},
	}
	for _, c := range cases {
		l := configfile.NewLoader(filepath.Join(t.TempDir(), "config.xml"))
		mustLoad(t, l)
		p := configfile.NewProvider(l, configfile.Overrides{}, nil, nil)
		if c.stored != "" {
			_ = p.Set(t.Context(), "InstanceName", c.stored)
		}
		got, err := p.InstanceName(t.Context())
		if err != nil {
			t.Fatalf("InstanceName(%q): %v", c.stored, err)
		}
		if got != c.want {
			t.Errorf("InstanceName(stored=%q) = %q, want %q", c.stored, got, c.want)
		}
	}
}

func TestProvider_AuthenticationMethodLegacyAndBasic(t *testing.T) {
	t.Parallel()

	// Legacy AuthenticationEnabled=true → upgrade to Forms.
	l := newLoader(t)
	mustLoad(t, l)
	p := configfile.NewProvider(l, configfile.Overrides{}, nil, nil)
	_ = p.Set(t.Context(), "AuthenticationEnabled", "true")
	got, err := p.AuthenticationMethod(t.Context())
	if err != nil {
		t.Fatalf("AuthenticationMethod: %v", err)
	}
	if got != "Forms" {
		t.Errorf("legacy enabled → method = %q, want Forms", got)
	}

	// Stored "Basic" → upgrade to Forms (deprecated shape).
	l2 := configfile.NewLoader(filepath.Join(t.TempDir(), "config.xml"))
	mustLoad(t, l2)
	p2 := configfile.NewProvider(l2, configfile.Overrides{}, nil, nil)
	_ = p2.Set(t.Context(), "AuthenticationMethod", "Basic")
	got2, err := p2.AuthenticationMethod(t.Context())
	if err != nil {
		t.Fatalf("AuthenticationMethod: %v", err)
	}
	if got2 != "Forms" {
		t.Errorf("Basic upgrade → %q, want Forms", got2)
	}

	// No legacy flag, no stored value → default None.
	l3 := configfile.NewLoader(filepath.Join(t.TempDir(), "config.xml"))
	mustLoad(t, l3)
	p3 := configfile.NewProvider(l3, configfile.Overrides{}, nil, nil)
	got3, err := p3.AuthenticationMethod(t.Context())
	if err != nil {
		t.Fatalf("AuthenticationMethod: %v", err)
	}
	if got3 != "None" {
		t.Errorf("default = %q, want None", got3)
	}
}

func TestProvider_SaveConfigDictionarySkipsApiKeyAndDirtyOnly(t *testing.T) {
	t.Parallel()
	l := newLoader(t)
	mustLoad(t, l)
	bus := messaging.New(slog.New(slog.DiscardHandler))
	p := configfile.NewProvider(l, configfile.Overrides{}, bus, stableAPIKey("seed-key"))

	// Subscribe so we can count ConfigFileSavedEvent emissions.
	var saveCount int32
	messaging.Subscribe(bus, "test.cfg.saved",
		func(_ context.Context, _ configfile.ConfigFileSavedEvent) error {
			atomic.AddInt32(&saveCount, 1)
			return nil
		})

	// Seed two keys via Set so the dictionary path has something to
	// diff against.
	_ = p.Set(t.Context(), "BindAddress", "0.0.0.0")
	_ = p.Set(t.Context(), "Port", "8989")

	// Touch the API key so it's stored.
	if _, err := p.APIKey(t.Context()); err != nil {
		t.Fatalf("APIKey: %v", err)
	}

	updates := map[string]string{
		"BindAddress": "0.0.0.0", // unchanged
		"Port":        "9999",    // changed
		"ApiKey":      "EVIL",    // must be ignored
	}
	changed, err := p.SaveConfigDictionary(t.Context(), updates)
	if err != nil {
		t.Fatalf("SaveConfigDictionary: %v", err)
	}
	if !changed {
		t.Errorf("SaveConfigDictionary returned changed=false despite Port change")
	}
	if v, _ := p.Int(t.Context(), "Port", 0, false); v != 9999 {
		t.Errorf("Port not updated: %d", v)
	}
	if v, _ := p.String(t.Context(), "ApiKey", "", false); v != "seed-key" {
		t.Errorf("ApiKey overwritten via SaveConfigDictionary: %q", v)
	}

	// All-no-change call → false, no event.
	prev := atomic.LoadInt32(&saveCount)
	noop, err := p.SaveConfigDictionary(t.Context(), map[string]string{
		"BindAddress": "0.0.0.0",
		"Port":        "9999",
	})
	if err != nil {
		t.Fatalf("SaveConfigDictionary no-op: %v", err)
	}
	if noop {
		t.Errorf("no-op SaveConfigDictionary returned changed=true")
	}
	bus.Wait()
	if atomic.LoadInt32(&saveCount) != prev {
		t.Errorf("no-op call published an event")
	}

	// Dirty call published exactly one event.
	bus.Wait()
	if got := atomic.LoadInt32(&saveCount); got != 1 {
		t.Errorf("event count = %d, want 1", got)
	}
}

func TestLoader_RejectsCorruptFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, content string
	}{
		{"empty", ""},
		{"control-chars", "\x00\x00\x01\x01"},
		{"not-xml", "this is not xml at all"},
		{"wrong-root", `<?xml version="1.0"?><Other><x>1</x></Other>`},
		{"two-roots", `<?xml version="1.0"?><Config><x>1</x></Config><Config></Config>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.xml")
			if err := os.WriteFile(path, []byte(c.content), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			l := configfile.NewLoader(path)
			err := l.Load(t.Context())
			if !errors.Is(err, configfile.ErrInvalidConfigFile) {
				t.Errorf("Load(%s) = %v, want ErrInvalidConfigFile", c.name, err)
			}
		})
	}
}

func TestLoader_ParseExistingConfig(t *testing.T) {
	t.Parallel()
	// Shape mirrors what Sonarr v3/v4/v5 typically writes — used to
	// confirm the Go parser reads existing user files without
	// reformatting noise.
	const sample = `<?xml version="1.0" encoding="utf-8"?>
<Config>
  <BindAddress>*</BindAddress>
  <Port>8989</Port>
  <SslPort>9898</SslPort>
  <EnableSsl>False</EnableSsl>
  <ApiKey>abc123</ApiKey>
  <UrlBase>/sonarr</UrlBase>
  <InstanceName>Sonarr 4K</InstanceName>
  <Branch>main</Branch>
  <LogLevel>info</LogLevel>
</Config>
`
	path := filepath.Join(t.TempDir(), "config.xml")
	if err := os.WriteFile(path, []byte(sample), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	l := configfile.NewLoader(path)
	mustLoad(t, l)
	p := configfile.NewProvider(l, configfile.Overrides{}, nil, nil)

	if v, _ := p.BindAddress(t.Context()); v != "*" {
		t.Errorf("BindAddress = %q", v)
	}
	if v, _ := p.Port(t.Context()); v != 8989 {
		t.Errorf("Port = %d", v)
	}
	if v, _ := p.URLBase(t.Context()); v != "/sonarr" {
		t.Errorf("URLBase = %q", v)
	}
	if v, _ := p.InstanceName(t.Context()); v != "Sonarr 4K" {
		t.Errorf("InstanceName = %q", v)
	}
	if v, _ := p.LogLevel(t.Context()); v != "info" {
		t.Errorf("LogLevel = %q", v)
	}
	if v, _ := p.EnableSsl(t.Context()); v {
		t.Errorf("EnableSsl = true")
	}
}

func TestProvider_EnsureDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.xml")
	l := configfile.NewLoader(path)
	mustLoad(t, l) // missing file is fine
	p := configfile.NewProvider(l, configfile.Overrides{}, nil, nil)

	defaults := map[string]string{
		"BindAddress": "*",
		"Port":        "8989",
		"LogLevel":    "debug",
	}
	if err := p.EnsureDefault(t.Context(), defaults); err != nil {
		t.Fatalf("EnsureDefault: %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, want := range []string{"<BindAddress>*</BindAddress>", "<Port>8989</Port>", "<LogLevel>debug</LogLevel>"} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("EnsureDefault output missing %q:\n%s", want, contents)
		}
	}

	// Calling again with different defaults should NOT overwrite — the
	// file already exists.
	other := map[string]string{"BindAddress": "127.0.0.1"}
	if err := p.EnsureDefault(t.Context(), other); err != nil {
		t.Fatalf("EnsureDefault 2: %v", err)
	}
	contents2, _ := os.ReadFile(path)
	if !strings.Contains(string(contents2), "<BindAddress>*</BindAddress>") {
		t.Errorf("EnsureDefault overwrote existing file:\n%s", contents2)
	}
}
