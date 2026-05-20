package downloadstation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients/downloadstation"
)

// queueFileStationGetinfo queues an API discovery + auth +
// FileStation getinfo response — the canonical 3-call sequence for
// a one-shot GetSharedFolderMapping fetch. SharedFolder is fixed at
// /data; physical path is hard-coded at /volume1/data which matches
// the canonical Synology SharedFolder→volume mapping convention.
func queueFileStationGetinfo(doer *scriptedDoer) {
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queue("/webapi/FileStation/file_share.cgi",
		`{"success":true,"data":{"files":[
			{"isdir":true,"name":"data","path":"/data","additional":{"real_path":"/volume1/data"}}
		]}}`)
}

func TestSharedFolderResolver_HappyPath(t *testing.T) {
	t.Parallel()
	doer := newScripted()
	queueFileStationGetinfo(doer)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	resolver := downloadstation.NewSharedFolderResolver(proxy)

	got, err := resolver.Resolve(context.Background(), newSettings(), "/data/tv/Show.S01E02")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if want := "/volume1/data/tv/Show.S01E02"; got.FullPath() != want {
		t.Errorf("Resolve = %q, want %q", got.FullPath(), want)
	}
}

func TestSharedFolderResolver_OnlySharedFolderNoSubpath(t *testing.T) {
	t.Parallel()
	doer := newScripted()
	queueFileStationGetinfo(doer)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	resolver := downloadstation.NewSharedFolderResolver(proxy)

	got, err := resolver.Resolve(context.Background(), newSettings(), "/data")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if want := "/volume1/data"; got.FullPath() != want {
		t.Errorf("Resolve = %q, want %q", got.FullPath(), want)
	}
}

func TestSharedFolderResolver_CachesAcrossCallsForSameSharedFolder(t *testing.T) {
	t.Parallel()
	doer := newScripted()
	queueFileStationGetinfo(doer)
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	resolver := downloadstation.NewSharedFolderResolver(proxy)

	for i := 0; i < 5; i++ {
		if _, err := resolver.Resolve(context.Background(), newSettings(), "/data/tv/E"+string(rune('0'+i))); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	// 1 round-trip total: discovery + auth + 1 file_share.cgi call.
	fsCalls := 0
	for _, c := range doer.calls {
		if c.path == "/webapi/FileStation/file_share.cgi" {
			fsCalls++
		}
	}
	if fsCalls != 1 {
		t.Errorf("FileStation called %d times, want 1 (cached)", fsCalls)
	}
}

func TestSharedFolderResolver_CacheExpiresAfterTTL(t *testing.T) {
	t.Parallel()
	doer := newScripted()
	queueFileStationGetinfo(doer)
	// Second probe after TTL expiry — re-queue the same fixture so
	// the second Resolve sees a valid response.
	queueFileStationGetinfo(doer)

	base := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	clock := base
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return clock })
	resolver := downloadstation.NewSharedFolderResolver(proxy,
		downloadstation.WithSharedFolderTTL(10*time.Minute),
		downloadstation.WithSharedFolderClock(func() time.Time { return clock }),
	)

	if _, err := resolver.Resolve(context.Background(), newSettings(), "/data/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), newSettings(), "/data/x"); err != nil {
		t.Fatal(err)
	}
	// 1 FileStation call so far (within TTL).
	clock = base.Add(11 * time.Minute)
	if _, err := resolver.Resolve(context.Background(), newSettings(), "/data/x"); err != nil {
		t.Fatal(err)
	}
	fsCalls := 0
	for _, c := range doer.calls {
		if c.path == "/webapi/FileStation/file_share.cgi" {
			fsCalls++
		}
	}
	if fsCalls != 2 {
		t.Errorf("FileStation called %d times, want 2 (1 within TTL + 1 after expiry)", fsCalls)
	}
}

func TestSharedFolderResolver_ProxyErrorNotCached(t *testing.T) {
	t.Parallel()
	// Both calls fail with a transport error. The error must NOT be
	// cached; each Resolve re-walks the proxy.
	doer := newScripted()
	queueAPIDiscoveryWithFileStation(doer)
	doer.queue("/webapi/auth.cgi", `{"success":true,"data":{"sid":"S"}}`)
	doer.queueErr("/webapi/FileStation/file_share.cgi", errors.New("transient"))
	doer.queueErr("/webapi/FileStation/file_share.cgi", errors.New("transient"))
	proxy := downloadstation.NewProxy(doer, nil, func() time.Time { return time.Unix(0, 0) })
	resolver := downloadstation.NewSharedFolderResolver(proxy)

	for i := 0; i < 2; i++ {
		if _, err := resolver.Resolve(context.Background(), newSettings(), "/data/x"); err == nil {
			t.Fatalf("call %d: want err", i)
		}
	}
	fsCalls := 0
	for _, c := range doer.calls {
		if c.path == "/webapi/FileStation/file_share.cgi" {
			fsCalls++
		}
	}
	if fsCalls != 2 {
		t.Errorf("FileStation called %d times, want 2 (errors must NOT be cached)", fsCalls)
	}
}

func TestSharedFolderResolver_EmptyPathErrors(t *testing.T) {
	t.Parallel()
	resolver := downloadstation.NewSharedFolderResolver(downloadstation.NewProxy(newScripted(), nil, nil))
	if _, err := resolver.Resolve(context.Background(), newSettings(), ""); err == nil {
		t.Errorf("Resolve(\"\"): want err")
	}
}

func TestSharedFolderResolver_RootOnlyPathErrors(t *testing.T) {
	t.Parallel()
	resolver := downloadstation.NewSharedFolderResolver(downloadstation.NewProxy(newScripted(), nil, nil))
	if _, err := resolver.Resolve(context.Background(), newSettings(), "/"); err == nil {
		t.Errorf("Resolve(\"/\"): want err")
	}
}

func TestSharedFolderResolver_NilProxyPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on nil proxy")
		}
	}()
	downloadstation.NewSharedFolderResolver(nil)
}
