package main

import (
	"context"
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/configfile"
)

// fakeInstanceNamer stands in for *configfile.Provider's InstanceName
// accessor so the adapter can be exercised without a real config file.
type fakeInstanceNamer struct {
	name string
	err  error
}

func (f fakeInstanceNamer) InstanceName(_ context.Context) (string, error) {
	return f.name, f.err
}

// TestNotificationConfigAdapter pins the J460 wiring: the adapter must
// expose the configured InstanceName as the `InstanceName() string`
// surface the Discord/Telegram notification providers narrow off
// deps.ConfigProvider, and fall back to the product name when the
// configfile accessor errors or returns empty.
func TestNotificationConfigAdapter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  fakeInstanceNamer
		want string
	}{
		{"configured", fakeInstanceNamer{name: "MyBox"}, "MyBox"},
		{"default product name", fakeInstanceNamer{name: configfile.AppName}, configfile.AppName},
		{"error falls back", fakeInstanceNamer{err: errors.New("boom")}, configfile.AppName},
		{"empty falls back", fakeInstanceNamer{name: ""}, configfile.AppName},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := notificationConfigAdapter{src: c.src}
			if got := a.InstanceName(); got != c.want {
				t.Errorf("InstanceName(): got %q, want %q", got, c.want)
			}
		})
	}
}

// TestNotificationConfigAdapter_SatisfiesProviderSurface is a
// compile-time guard that the adapter satisfies the exact narrow
// interface the Discord and Telegram providers assert off
// deps.ConfigProvider (interface{ InstanceName() string }). If the
// providers' asserted shape ever drifts, this stops compiling.
func TestNotificationConfigAdapter_SatisfiesProviderSurface(t *testing.T) {
	t.Parallel()
	var _ interface{ InstanceName() string } = notificationConfigAdapter{src: fakeInstanceNamer{}}
}
