package appenv_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/appenv"
)

func TestCurrentOs_MatchesGOOS(t *testing.T) {
	t.Parallel()
	want := map[string]appenv.Os{
		"linux":   appenv.OsLinux,
		"darwin":  appenv.OsDarwin,
		"windows": appenv.OsWindows,
		"freebsd": appenv.OsBSD,
		"netbsd":  appenv.OsBSD,
		"openbsd": appenv.OsBSD,
	}
	if got, ok := want[runtime.GOOS]; ok {
		if appenv.CurrentOs != got {
			t.Errorf("CurrentOs (GOOS=%s) = %v, want %v", runtime.GOOS, appenv.CurrentOs, got)
		}
	}
	// IsLinux semantic: bundles BSD with Linux, mirroring C#.
	bundled := appenv.CurrentOs == appenv.OsLinux || appenv.CurrentOs == appenv.OsBSD
	if appenv.IsLinux() != bundled {
		t.Errorf("IsLinux semantic broken on %s", runtime.GOOS)
	}
	// Mutually exclusive: at most one of IsDarwin/IsWindows/IsLinux.
	on := 0
	for _, b := range []bool{appenv.IsDarwin(), appenv.IsWindows(), appenv.IsLinux()} {
		if b {
			on++
		}
	}
	if on > 1 {
		t.Errorf("multiple Is*() returned true on %s", runtime.GOOS)
	}
}

func TestDetectOs_BasicShape(t *testing.T) {
	t.Parallel()
	info := appenv.DetectOs()
	if info.Os != appenv.CurrentOs {
		t.Errorf("DetectOs.Os = %v, want %v", info.Os, appenv.CurrentOs)
	}
	// Container fields can only be true on Linux. On macOS/Windows
	// they must be false (matches the C# OsInfo constructor that
	// only walks the markers when IsLinux).
	if !appenv.IsLinux() {
		if info.IsDocker || info.IsPodman || info.IsContainerized {
			t.Errorf("non-Linux container detection fired: %+v", info)
		}
	}
}

func TestParseStartupArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		argv []string
		args map[string]string
		flag []string
	}{
		{
			name: "key=value with slash prefix",
			argv: []string{"/data=/srv/sonarr"},
			args: map[string]string{"data": "/srv/sonarr"},
		},
		{
			name: "key=value with double-dash prefix",
			argv: []string{"--data=/srv/sonarr"},
			args: map[string]string{"data": "/srv/sonarr"},
		},
		{
			name: "key=value bare",
			argv: []string{"DATA=/srv/sonarr"}, // case insensitive
			args: map[string]string{"data": "/srv/sonarr"},
		},
		{
			name: "quoted value",
			argv: []string{`/data="/path with spaces"`},
			args: map[string]string{"data": "/path with spaces"},
		},
		{
			name: "boolean flag",
			argv: []string{"/nobrowser"},
			flag: []string{"nobrowser"},
		},
		{
			name: "mixed",
			argv: []string{"/data=/srv", "--nobrowser", "-?"},
			args: map[string]string{"data": "/srv"},
			flag: []string{"nobrowser", "?"},
		},
		{
			name: "empty / whitespace tolerated",
			argv: []string{"", "  ", "/--"},
			args: map[string]string{},
			flag: []string{},
		},
		{
			// Slice 4.FD's new --frontenddir flag — same parser
			// path as `data`; the slot is exercised here to lock in
			// the constant's lowercase name.
			name: "frontenddir",
			argv: []string{"--frontenddir=/srv/sonarr/frontend/build"},
			args: map[string]string{appenv.StartupArgFrontendDir: "/srv/sonarr/frontend/build"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := appenv.ParseStartupArgs(c.argv)
			for k, want := range c.args {
				got, ok := ctx.Arg(k)
				if !ok {
					t.Errorf("Arg(%q) missing", k)
					continue
				}
				if got != want {
					t.Errorf("Arg(%q) = %q, want %q", k, got, want)
				}
			}
			if len(ctx.Args) != len(c.args) {
				t.Errorf("Args count = %d, want %d (%+v)", len(ctx.Args), len(c.args), ctx.Args)
			}
			for _, f := range c.flag {
				if !ctx.HasFlag(f) {
					t.Errorf("HasFlag(%q) = false", f)
				}
			}
			if len(ctx.Flags) != len(c.flag) {
				t.Errorf("Flags count = %d, want %d (%+v)", len(ctx.Flags), len(c.flag), ctx.Flags)
			}
		})
	}
}

func TestDetectAppFolder_DataOverride(t *testing.T) {
	t.Parallel()
	override := filepath.Join(t.TempDir(), "sonarr-test")
	ctx := appenv.ParseStartupArgs([]string{"/data=" + override})
	info, err := appenv.DetectAppFolder(ctx)
	if err != nil {
		t.Fatalf("DetectAppFolder: %v", err)
	}
	if info.AppData != override {
		t.Errorf("AppData = %q, want %q", info.AppData, override)
	}
	if info.Temp == "" {
		t.Errorf("Temp empty")
	}
	if info.Startup == "" || !filepath.IsAbs(info.Startup) {
		t.Errorf("Startup not absolute: %q", info.Startup)
	}
}

func TestDetectAppFolder_NoOverride(t *testing.T) {
	t.Parallel()
	ctx := appenv.ParseStartupArgs(nil)
	info, err := appenv.DetectAppFolder(ctx)
	if err != nil {
		t.Fatalf("DetectAppFolder: %v", err)
	}
	if !strings.HasSuffix(info.AppData, "Sonarr") {
		t.Errorf("AppData (%q) does not end with Sonarr", info.AppData)
	}
	if !filepath.IsAbs(info.AppData) {
		t.Errorf("AppData not absolute: %q", info.AppData)
	}
}

func TestAppFolderInfo_DerivedPaths(t *testing.T) {
	t.Parallel()
	info := appenv.AppFolderInfo{AppData: "/data/sonarr"}
	if info.ConfigPath() != filepath.Join("/data/sonarr", "config.xml") {
		t.Errorf("ConfigPath: %q", info.ConfigPath())
	}
	if info.MainDBPath("Sonarr") != filepath.Join("/data/sonarr", "Sonarr.db") {
		t.Errorf("MainDBPath: %q", info.MainDBPath("Sonarr"))
	}
	if info.LogDBPath("Sonarr") != filepath.Join("/data/sonarr", "Sonarr-log.db") {
		t.Errorf("LogDBPath: %q", info.LogDBPath("Sonarr"))
	}
}
