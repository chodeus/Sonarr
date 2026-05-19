package configfile_test

import (
	"testing"

	"sonarr.tv/sonarr/internal/configfile"
)

func TestParseOverridesFromEnv_Server(t *testing.T) {
	t.Parallel()
	o := configfile.ParseOverridesFromEnv([]string{
		"Sonarr__Server__Port=12345",
		"Sonarr__Server__SslPort=23456",
		"Sonarr__Server__BindAddress=0.0.0.0",
		"Sonarr__Server__UrlBase=/sonarr",
		"Sonarr__Server__EnableSsl=true",
	})
	if o.Port == nil || *o.Port != 12345 {
		t.Errorf("Port = %v, want 12345", deref(o.Port))
	}
	if o.SslPort == nil || *o.SslPort != 23456 {
		t.Errorf("SslPort = %v, want 23456", deref(o.SslPort))
	}
	if o.BindAddress == nil || *o.BindAddress != "0.0.0.0" {
		t.Errorf("BindAddress = %v, want 0.0.0.0", derefStr(o.BindAddress))
	}
	if o.URLBase == nil || *o.URLBase != "/sonarr" {
		t.Errorf("URLBase = %v", derefStr(o.URLBase))
	}
	if o.EnableSsl == nil || !*o.EnableSsl {
		t.Errorf("EnableSsl = %v, want true", derefBool(o.EnableSsl))
	}
}

func TestParseOverridesFromEnv_Auth(t *testing.T) {
	t.Parallel()
	o := configfile.ParseOverridesFromEnv([]string{
		"Sonarr__Auth__ApiKey=abc-123",
		"Sonarr__Auth__Method=Forms",
		"Sonarr__Auth__Required=DisabledForLocalAddresses",
	})
	if o.APIKey == nil || *o.APIKey != "abc-123" {
		t.Errorf("APIKey = %v", derefStr(o.APIKey))
	}
	if o.AuthMethod == nil || *o.AuthMethod != "Forms" {
		t.Errorf("AuthMethod = %v", derefStr(o.AuthMethod))
	}
	if o.AuthRequired == nil || *o.AuthRequired != "DisabledForLocalAddresses" {
		t.Errorf("AuthRequired = %v", derefStr(o.AuthRequired))
	}
}

func TestParseOverridesFromEnv_AppAndUpdate(t *testing.T) {
	t.Parallel()
	o := configfile.ParseOverridesFromEnv([]string{
		"Sonarr__App__InstanceName=Test Sonarr",
		"Sonarr__App__LaunchBrowser=false",
		"Sonarr__Update__Branch=develop",
	})
	if o.InstanceName == nil || *o.InstanceName != "Test Sonarr" {
		t.Errorf("InstanceName = %v", derefStr(o.InstanceName))
	}
	if o.LaunchBrowser == nil || *o.LaunchBrowser {
		t.Errorf("LaunchBrowser = %v, want false", derefBool(o.LaunchBrowser))
	}
	if o.BranchOverride == nil || *o.BranchOverride != "develop" {
		t.Errorf("BranchOverride = %v", derefStr(o.BranchOverride))
	}
}

func TestParseOverridesFromEnv_LogLevel(t *testing.T) {
	t.Parallel()
	// LogLevel sits at the top level (no Sonarr:Section path) per
	// ConfigFileProvider.cs:264. Accept the Sonarr__LogLevel form.
	o := configfile.ParseOverridesFromEnv([]string{"Sonarr__LogLevel=debug"})
	if o.LogLevel == nil || *o.LogLevel != "debug" {
		t.Errorf("LogLevel = %v", derefStr(o.LogLevel))
	}
}

func TestParseOverridesFromEnv_CaseInsensitive(t *testing.T) {
	t.Parallel()
	// ASP.NET config is case-insensitive — all-uppercase or mixed
	// should still resolve.
	o := configfile.ParseOverridesFromEnv([]string{
		"SONARR__SERVER__PORT=9999",
		"sonarr__auth__apikey=lower-case-key",
	})
	if o.Port == nil || *o.Port != 9999 {
		t.Errorf("Port (upper) = %v", deref(o.Port))
	}
	if o.APIKey == nil || *o.APIKey != "lower-case-key" {
		t.Errorf("APIKey (lower) = %v", derefStr(o.APIKey))
	}
}

func TestParseOverridesFromEnv_IgnoresUnrelated(t *testing.T) {
	t.Parallel()
	o := configfile.ParseOverridesFromEnv([]string{
		"PATH=/usr/bin:/bin",
		"HOME=/home/sonarr",
		"NOT_SONARR__FOO=bar",
		"Sonarr=value-without-section",      // no __<...> tail
		"Sonarr__App__Theme=carbon",         // unmapped App key
		"Sonarr__Server__Unknown=ignored",   // unmapped Server key
		"Sonarr__BogusSection__Key=ignored", // unknown section
	})
	// No fields should be set.
	if o.Port != nil || o.APIKey != nil || o.LogLevel != nil || o.InstanceName != nil {
		t.Errorf("expected zero overrides, got %#v", o)
	}
}

func TestParseOverridesFromEnv_InvalidIntsAreDropped(t *testing.T) {
	t.Parallel()
	// Mirrors C# `config.GetValue<int?>` semantics — unparseable
	// values yield null rather than panicking the boot.
	o := configfile.ParseOverridesFromEnv([]string{
		"Sonarr__Server__Port=not-a-number",
		"Sonarr__Server__EnableSsl=maybe",
	})
	if o.Port != nil {
		t.Errorf("Port should drop on parse error, got %d", *o.Port)
	}
	if o.EnableSsl != nil {
		t.Errorf("EnableSsl should drop on parse error, got %v", *o.EnableSsl)
	}
}

func TestParseOverridesFromEnv_MissingEqualsDropped(t *testing.T) {
	t.Parallel()
	// Each os.Environ() entry is `KEY=VALUE`; an entry with no `=`
	// (or `=` at position 0) is malformed and ignored.
	o := configfile.ParseOverridesFromEnv([]string{
		"Sonarr__Server__Port",
		"=orphan",
	})
	if o.Port != nil {
		t.Errorf("Port should not be set when entry is malformed")
	}
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func derefStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func derefBool(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}
