package v3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"sonarr.tv/sonarr/internal/notifications"
)

type fakeNotifLister struct {
	rows []notifications.Notification
	err  error
}

func (f *fakeNotifLister) All(context.Context) ([]notifications.Notification, error) {
	return f.rows, f.err
}

func TestNotificationHandler_Empty(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationHandler(&fakeNotifLister{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/notification", nil))
	if got := rr.Body.String(); got != "[]" {
		t.Errorf("empty body: got %q want []", got)
	}
}

func TestNotificationHandler_Error(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	NotificationHandler(&fakeNotifLister{err: errors.New("boom")}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v3/notification", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d want 500", rr.Code)
	}
}
