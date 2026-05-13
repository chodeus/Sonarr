package healthcheck

import (
	"context"
	"errors"
	"testing"
)

type fakeSearchLister struct {
	autoAll     int
	autoActive  int
	interactive int
	err         error
}

func (f *fakeSearchLister) AutomaticSearchEnabledCount(_ context.Context, requireActive bool) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	if requireActive {
		return f.autoActive, nil
	}
	return f.autoAll, nil
}

func (f *fakeSearchLister) InteractiveSearchEnabledCount(context.Context) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.interactive, nil
}

func TestIndexerSearchCheck_NoAutomatic(t *testing.T) {
	t.Parallel()
	c, err := NewIndexerSearchCheck(&fakeSearchLister{})
	if err != nil {
		t.Fatalf("NewIndexerSearchCheck: %v", err)
	}
	got := c.Run(context.Background())
	if got.Type != ResultWarning || got.Reason != ReasonIndexerSearchNoAutomatic {
		t.Errorf("no-automatic: got %+v", got)
	}
}

func TestIndexerSearchCheck_NoInteractive(t *testing.T) {
	t.Parallel()
	c, _ := NewIndexerSearchCheck(&fakeSearchLister{autoAll: 1, autoActive: 1, interactive: 0})
	got := c.Run(context.Background())
	if got.Type != ResultWarning || got.Reason != ReasonIndexerSearchNoInteractive {
		t.Errorf("no-interactive: got %+v", got)
	}
}

func TestIndexerSearchCheck_NoActive(t *testing.T) {
	t.Parallel()
	c, _ := NewIndexerSearchCheck(&fakeSearchLister{autoAll: 1, autoActive: 0, interactive: 1})
	got := c.Run(context.Background())
	if got.Type != ResultWarning || got.Reason != ReasonIndexerSearchNoAvailableIndexers {
		t.Errorf("no-active: got %+v", got)
	}
}

func TestIndexerSearchCheck_OK(t *testing.T) {
	t.Parallel()
	c, _ := NewIndexerSearchCheck(&fakeSearchLister{autoAll: 1, autoActive: 1, interactive: 1})
	got := c.Run(context.Background())
	if got.Type != ResultOK {
		t.Errorf("ok: got %+v", got)
	}
}

func TestNewIndexerSearchCheck_NilLister(t *testing.T) {
	t.Parallel()
	if _, err := NewIndexerSearchCheck(nil); err == nil {
		t.Errorf("nil lister: expected error")
	}
}

func TestIndexerSearchCheck_ListerError(t *testing.T) {
	t.Parallel()
	c, _ := NewIndexerSearchCheck(&fakeSearchLister{err: errors.New("boom")})
	got := c.Run(context.Background())
	if got.Type != ResultWarning {
		t.Errorf("error path: got %+v", got)
	}
}
