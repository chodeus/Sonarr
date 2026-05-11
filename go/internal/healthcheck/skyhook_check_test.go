package healthcheck

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/metadatasource"
)

// fakeSkyHookProber returns the configured err on every probe call
// and records the call count. Each test scopes its own instance.
type fakeSkyHookProber struct {
	err   error
	calls int
}

func (f *fakeSkyHookProber) ProbeKnownSeries(_ context.Context) error {
	f.calls++
	return f.err
}

// TestSkyHookCheck_Run covers the three classification outcomes
// documented at [SkyHookCheck]: success → OK; ErrServiceUnavailable
// → Error/ReasonSkyHookUnavailable; other error → Warning/
// ReasonSkyHookProbeFailed.
func TestSkyHookCheck_Run(t *testing.T) {
	cases := []struct {
		name       string
		proberErr  error
		wantType   Result
		wantReason Reason
		wantMsg    string // substring
	}{
		{
			name:      "probe_success_yields_OK",
			proberErr: nil,
			wantType:  ResultOK,
		},
		{
			name:       "service_unavailable_yields_Error",
			proberErr:  metadatasource.NewServiceUnavailableError("skyhook 503", nil),
			wantType:   ResultError,
			wantReason: ReasonSkyHookUnavailable,
			wantMsg:    "unavailable",
		},
		{
			name: "wrapped_service_unavailable_still_classifies_as_Error",
			// Wrap to exercise the errors.Is chain — production
			// callers may wrap the sentinel.
			proberErr:  &wrappedErr{inner: metadatasource.NewServiceUnavailableError("downstream", nil)},
			wantType:   ResultError,
			wantReason: ReasonSkyHookUnavailable,
		},
		{
			name:       "other_error_yields_Warning",
			proberErr:  errors.New("decode failure: unexpected EOF"),
			wantType:   ResultWarning,
			wantReason: ReasonSkyHookProbeFailed,
			wantMsg:    "decode failure",
		},
		{
			name:       "context_canceled_yields_Warning",
			proberErr:  context.Canceled,
			wantType:   ResultWarning,
			wantReason: ReasonSkyHookProbeFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prober := &fakeSkyHookProber{err: tc.proberErr}
			check, err := NewSkyHookCheck(prober, nil)
			if err != nil {
				t.Fatalf("NewSkyHookCheck: %v", err)
			}
			got := check.Run(context.Background())
			if prober.calls != 1 {
				t.Errorf("prober called %d times, want 1", prober.calls)
			}
			if got.Source != SkyHookCheckName {
				t.Errorf("Source = %q, want %q", got.Source, SkyHookCheckName)
			}
			if got.Type != tc.wantType {
				t.Errorf("Type = %v, want %v", got.Type, tc.wantType)
			}
			if tc.wantType != ResultOK {
				if got.Reason != tc.wantReason {
					t.Errorf("Reason = %v, want %v", got.Reason, tc.wantReason)
				}
				if got.WikiURL == "" {
					t.Errorf("WikiURL empty on non-OK result")
				}
			}
			if tc.wantMsg != "" && !strings.Contains(got.Message, tc.wantMsg) {
				t.Errorf("Message = %q, want substring %q", got.Message, tc.wantMsg)
			}
		})
	}
}

// TestNewSkyHookCheck_RejectsNilProber covers the constructor guard.
func TestNewSkyHookCheck_RejectsNilProber(t *testing.T) {
	if _, err := NewSkyHookCheck(nil, nil); err == nil {
		t.Errorf("NewSkyHookCheck(nil, nil) = no err, want guard rejection")
	}
}

// TestSkyHookCheck_Metadata covers the framework-required methods
// (Name / CheckOnStartup / CheckOnSchedule / EventTriggers).
func TestSkyHookCheck_Metadata(t *testing.T) {
	check, err := NewSkyHookCheck(&fakeSkyHookProber{}, nil)
	if err != nil {
		t.Fatalf("NewSkyHookCheck: %v", err)
	}
	if got := check.Name(); got != SkyHookCheckName {
		t.Errorf("Name() = %q, want %q", got, SkyHookCheckName)
	}
	if !check.CheckOnStartup() {
		t.Errorf("CheckOnStartup() = false, want true")
	}
	if !check.CheckOnSchedule() {
		t.Errorf("CheckOnSchedule() = false, want true")
	}
	if triggers := check.EventTriggers(); triggers != nil {
		t.Errorf("EventTriggers() = %+v, want nil", triggers)
	}
}

// wrappedErr is a minimal error wrapper used to verify errors.Is
// traversal works against the metadatasource sentinel chain.
type wrappedErr struct {
	inner error
}

func (e *wrappedErr) Error() string { return "wrapped: " + e.inner.Error() }
func (e *wrappedErr) Unwrap() error { return e.inner }
