package providerstatus_test

import (
	"context"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/downloadclientstatus"
	"sonarr.tv/sonarr/internal/importliststatus"
	"sonarr.tv/sonarr/internal/indexerstatus"
	"sonarr.tv/sonarr/internal/notificationstatus"
	"sonarr.tv/sonarr/internal/providerstatus"
)

// recorderFactory builds a per-family Service that satisfies both
// providerstatus.Recorder and providerstatus.BlockedView. The four
// factories below each construct one of the family services and return
// it as the shared interface pair, exercising the compile-time + runtime
// behaviour at once.
type recorderFactory struct {
	name  string
	build func(t *testing.T) (providerstatus.Recorder, providerstatus.BlockedView)
}

const refClockYear = 2026

var refClock = time.Date(refClockYear, 5, 10, 12, 0, 0, 0, time.UTC)

func indexerFactory(t *testing.T) (providerstatus.Recorder, providerstatus.BlockedView) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := indexerstatus.NewRepo(db)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := indexerstatus.NewService(r, nil, refClock.Add(-1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return refClock })
	return svc, svc
}

func downloadClientFactory(t *testing.T) (providerstatus.Recorder, providerstatus.BlockedView) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := downloadclientstatus.NewRepo(db)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := downloadclientstatus.NewService(r, nil, refClock.Add(-1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return refClock })
	return svc, svc
}

func notificationFactory(t *testing.T) (providerstatus.Recorder, providerstatus.BlockedView) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := notificationstatus.NewRepo(db)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := notificationstatus.NewService(r, nil, refClock.Add(-1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return refClock })
	return svc, svc
}

func importListFactory(t *testing.T) (providerstatus.Recorder, providerstatus.BlockedView) {
	t.Helper()
	db := datastoretest.OpenSQLite(t)
	r, err := importliststatus.NewRepo(db)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := importliststatus.NewService(r, nil, refClock.Add(-1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	svc.SetClock(func() time.Time { return refClock })
	return svc, svc
}

func TestAllFamiliesSatisfyRecorderAndBlockedView(t *testing.T) {
	factories := []recorderFactory{
		{name: "indexer", build: indexerFactory},
		{name: "downloadclient", build: downloadClientFactory},
		{name: "notification", build: notificationFactory},
		{name: "importlist", build: importListFactory},
	}

	ctx := context.Background()
	for _, f := range factories {
		t.Run(f.name, func(t *testing.T) {
			rec, view := f.build(t)

			// Initially no blocked providers.
			blocked, err := view.GetBlocked(ctx)
			if err != nil {
				t.Fatalf("GetBlocked initial: %v", err)
			}
			if len(blocked) != 0 {
				t.Fatalf("expected empty blocked list, got %d", len(blocked))
			}

			// RecordFailure with a 30-minute minimum back-off → provider 7
			// must show up in the blocked view.
			if err := rec.RecordFailure(ctx, 7, 30*time.Minute); err != nil {
				t.Fatalf("RecordFailure: %v", err)
			}
			blocked, err = view.GetBlocked(ctx)
			if err != nil {
				t.Fatalf("GetBlocked after failure: %v", err)
			}
			if len(blocked) != 1 {
				t.Fatalf("expected 1 blocked, got %d", len(blocked))
			}
			b := blocked[0]
			if b.ProviderID != 7 {
				t.Errorf("ProviderID = %d", b.ProviderID)
			}
			if b.DisabledTill == nil {
				t.Errorf("DisabledTill nil")
			}
			if b.EscalationLevel < 1 {
				t.Errorf("EscalationLevel = %d, want >=1", b.EscalationLevel)
			}

			// RecordSuccess decrements level + clears DisabledTill.
			if err := rec.RecordSuccess(ctx, 7); err != nil {
				t.Fatalf("RecordSuccess: %v", err)
			}
			blocked, _ = view.GetBlocked(ctx)
			if len(blocked) != 0 {
				t.Fatalf("expected empty blocked after RecordSuccess, got %d", len(blocked))
			}

			// RecordConnectionFailure exercises the escalate=false path.
			if err := rec.RecordConnectionFailure(ctx, 99); err != nil {
				t.Fatalf("RecordConnectionFailure: %v", err)
			}

			// OnProviderDeleted clears the row.
			if err := rec.OnProviderDeleted(ctx, 99); err != nil {
				t.Fatalf("OnProviderDeleted: %v", err)
			}
			blocked, _ = view.GetBlocked(ctx)
			for _, e := range blocked {
				if e.ProviderID == 99 {
					t.Errorf("provider 99 still in blocked list after delete")
				}
			}
		})
	}
}

// Ensure each family's compile-time assertion in *_status/recorder.go
// holds at runtime as well.
func TestRecorderInterfacesSatisfied(t *testing.T) {
	_, _ = indexerFactory(t)
	_, _ = downloadClientFactory(t)
	_, _ = notificationFactory(t)
	_, _ = importListFactory(t)
}
