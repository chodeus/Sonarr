package healthcheck

import (
	"context"
	"errors"
	"testing"
)

type fakeRssLister struct {
	enabled int
	active  int
	err     error
}

func (f *fakeRssLister) RssEnabledCount(_ context.Context, requireActive bool) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	if requireActive {
		return f.active, nil
	}
	return f.enabled, nil
}

func TestIndexerRssCheck_NoEnabled(t *testing.T) {
	t.Parallel()
	c, err := NewIndexerRssCheck(&fakeRssLister{})
	if err != nil {
		t.Fatalf("NewIndexerRssCheck: %v", err)
	}
	got := c.Run(context.Background())
	if got.Type != ResultError || got.Reason != ReasonIndexerRssNoIndexersEnabled {
		t.Errorf("no-enabled branch: got %+v", got)
	}
}

func TestIndexerRssCheck_EnabledButNoneActive(t *testing.T) {
	t.Parallel()
	c, _ := NewIndexerRssCheck(&fakeRssLister{enabled: 2, active: 0})
	got := c.Run(context.Background())
	if got.Type != ResultWarning || got.Reason != ReasonIndexerRssNoIndexersAvailable {
		t.Errorf("none-active branch: got %+v", got)
	}
}

func TestIndexerRssCheck_OK(t *testing.T) {
	t.Parallel()
	c, _ := NewIndexerRssCheck(&fakeRssLister{enabled: 2, active: 2})
	got := c.Run(context.Background())
	if got.Type != ResultOK {
		t.Errorf("ok branch: got %+v", got)
	}
}

func TestIndexerRssCheck_ListerError(t *testing.T) {
	t.Parallel()
	c, _ := NewIndexerRssCheck(&fakeRssLister{err: errors.New("boom")})
	got := c.Run(context.Background())
	if got.Type != ResultWarning {
		t.Errorf("error path should warn: got %+v", got)
	}
}

func TestNewIndexerRssCheck_NilLister(t *testing.T) {
	t.Parallel()
	if _, err := NewIndexerRssCheck(nil); err == nil {
		t.Errorf("nil lister: expected error")
	}
}
