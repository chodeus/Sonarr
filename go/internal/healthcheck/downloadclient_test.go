package healthcheck

import (
	"context"
	"errors"
	"testing"
)

type fakeDownloadClientLister struct {
	count int
	err   error
}

func (f *fakeDownloadClientLister) EnabledDownloadClientCount(context.Context) (int, error) {
	return f.count, f.err
}

func TestDownloadClientCheck_None(t *testing.T) {
	t.Parallel()
	c, err := NewDownloadClientCheck(&fakeDownloadClientLister{})
	if err != nil {
		t.Fatalf("NewDownloadClientCheck: %v", err)
	}
	got := c.Run(context.Background())
	if got.Type != ResultWarning || got.Reason != ReasonDownloadClientCheckNoneAvailable {
		t.Errorf("none branch: got %+v", got)
	}
}

func TestDownloadClientCheck_OK(t *testing.T) {
	t.Parallel()
	c, _ := NewDownloadClientCheck(&fakeDownloadClientLister{count: 1})
	got := c.Run(context.Background())
	if got.Type != ResultOK {
		t.Errorf("ok branch: got %+v", got)
	}
}

func TestNewDownloadClientCheck_NilLister(t *testing.T) {
	t.Parallel()
	if _, err := NewDownloadClientCheck(nil); err == nil {
		t.Errorf("nil lister: expected error")
	}
}

func TestDownloadClientCheck_ListerError(t *testing.T) {
	t.Parallel()
	c, _ := NewDownloadClientCheck(&fakeDownloadClientLister{err: errors.New("boom")})
	got := c.Run(context.Background())
	if got.Type != ResultWarning {
		t.Errorf("error path: got %+v", got)
	}
}
