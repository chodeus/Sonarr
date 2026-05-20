package healthcheck_test

import (
	"context"
	"testing"

	"sonarr.tv/sonarr/internal/healthcheck"
)

// stubGlobalMessageProvider implements the narrow
// PackageGlobalMessageProvider interface with a single configurable
// value.
type stubGlobalMessageProvider struct{ msg string }

func (s stubGlobalMessageProvider) PackageGlobalMessage() string { return s.msg }

func TestPackageGlobalMessageCheck_EmptyPasses(t *testing.T) {
	t.Parallel()
	cases := []string{"", "   ", "\n\t"}
	for _, msg := range cases {
		msg := msg
		t.Run("msg="+msg, func(t *testing.T) {
			t.Parallel()
			c := healthcheck.NewPackageGlobalMessageCheck(stubGlobalMessageProvider{msg: msg})
			got := c.Run(context.Background())
			if got.Type != healthcheck.ResultOK {
				t.Errorf("Type: got %v, want OK (empty message should pass)", got.Type)
			}
			if got.Message != "" {
				t.Errorf("Message: got %q, want empty", got.Message)
			}
		})
	}
}

func TestPackageGlobalMessageCheck_NoticeBare(t *testing.T) {
	t.Parallel()
	c := healthcheck.NewPackageGlobalMessageCheck(stubGlobalMessageProvider{msg: "Service migration window 2026-06-01"})
	got := c.Run(context.Background())
	if got.Type != healthcheck.ResultNotice {
		t.Errorf("Type: got %v, want Notice (bare message default)", got.Type)
	}
	if got.Message != "Service migration window 2026-06-01" {
		t.Errorf("Message: got %q, want passthrough", got.Message)
	}
	if got.Reason != healthcheck.ReasonPackage {
		t.Errorf("Reason: got %v, want ReasonPackage", got.Reason)
	}
}

func TestPackageGlobalMessageCheck_WarnPrefixStrips(t *testing.T) {
	t.Parallel()
	c := healthcheck.NewPackageGlobalMessageCheck(stubGlobalMessageProvider{msg: "Warn: backup before upgrade"})
	got := c.Run(context.Background())
	if got.Type != healthcheck.ResultWarning {
		t.Errorf("Type: got %v, want Warning", got.Type)
	}
	if got.Message != "backup before upgrade" {
		t.Errorf("Message: got %q, want %q (prefix should be stripped)", got.Message, "backup before upgrade")
	}
}

func TestPackageGlobalMessageCheck_ErrorPrefixStrips(t *testing.T) {
	t.Parallel()
	c := healthcheck.NewPackageGlobalMessageCheck(stubGlobalMessageProvider{msg: "Error: end-of-life package - upgrade required"})
	got := c.Run(context.Background())
	if got.Type != healthcheck.ResultError {
		t.Errorf("Type: got %v, want Error", got.Type)
	}
	if got.Message != "end-of-life package - upgrade required" {
		t.Errorf("Message: got %q, want %q", got.Message, "end-of-life package - upgrade required")
	}
}

func TestNewPackageGlobalMessageCheck_NilProvider(t *testing.T) {
	t.Parallel()
	if c := healthcheck.NewPackageGlobalMessageCheck(nil); c != nil {
		t.Errorf("NewPackageGlobalMessageCheck(nil): got %v, want nil", c)
	}
}
