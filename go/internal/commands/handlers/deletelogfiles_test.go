package handlers

import (
	"context"
	"errors"
	"testing"
)

type fakeFolderCleaner struct {
	emptied []string
	err     error
}

func (f *fakeFolderCleaner) EmptyFolder(path string) error {
	f.emptied = append(f.emptied, path)
	return f.err
}

func TestDeleteLogFiles_EmptiesConfiguredFolder(t *testing.T) {
	t.Parallel()
	c := &fakeFolderCleaner{}
	if err := NewDeleteLogFiles(c, "/var/log/sonarr", nil).Execute(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(c.emptied) != 1 || c.emptied[0] != "/var/log/sonarr" {
		t.Errorf("emptied = %v, want [/var/log/sonarr]", c.emptied)
	}
}

func TestDeleteUpdateLogFiles_EmptiesUpdateFolder(t *testing.T) {
	t.Parallel()
	c := &fakeFolderCleaner{}
	if err := NewDeleteUpdateLogFiles(c, "/var/log/sonarr/update", nil).Execute(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(c.emptied) != 1 || c.emptied[0] != "/var/log/sonarr/update" {
		t.Errorf("emptied = %v", c.emptied)
	}
}

func TestDeleteLogFiles_NilCleanerOrEmptyFolder_NoOp(t *testing.T) {
	t.Parallel()
	if err := NewDeleteLogFiles(nil, "/x", nil).Execute(context.Background(), ""); err != nil {
		t.Errorf("nil cleaner should no-op: %v", err)
	}
	c := &fakeFolderCleaner{}
	if err := NewDeleteLogFiles(c, "", nil).Execute(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if len(c.emptied) != 0 {
		t.Errorf("empty folder path → no EmptyFolder call, got %v", c.emptied)
	}
}

func TestDeleteLogFiles_ErrorPropagates(t *testing.T) {
	t.Parallel()
	c := &fakeFolderCleaner{err: errors.New("perm denied")}
	if err := NewDeleteLogFiles(c, "/x", nil).Execute(context.Background(), ""); err == nil {
		t.Error("EmptyFolder error should propagate")
	}
}
