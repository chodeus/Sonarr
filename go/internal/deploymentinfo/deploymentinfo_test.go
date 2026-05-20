package deploymentinfo_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"sonarr.tv/sonarr/internal/deploymentinfo"
)

// fakeFS implements deploymentinfo.FileSystem with an in-memory
// path → content map. Missing paths report not-exists.
type fakeFS struct {
	contents map[string]string
}

func (f fakeFS) FileExists(path string) bool {
	_, ok := f.contents[path]
	return ok
}

func (f fakeFS) ReadAllText(path string) (string, error) {
	if c, ok := f.contents[path]; ok {
		return c, nil
	}
	return "", errors.New("not found")
}

// pkg builds the expected absolute path for package_info given a
// bin folder. Used by tests to populate fakeFS.
func pkgPath(bin string) string { return filepath.Join(bin, "..", "package_info") }

// rel builds the expected absolute path for release_info given a
// bin folder.
func relPath(bin string) string { return filepath.Join(bin, "release_info") }

func TestNew_EmptyForNonPackagedInstall(t *testing.T) {
	t.Parallel()
	// Bin folder not named "bin" → package_info gate fails.
	p := deploymentinfo.New("/opt/sonarr", fakeFS{})
	if p.PackageVersion() != "" {
		t.Errorf("PackageVersion: got %q, want empty", p.PackageVersion())
	}
	if p.PackageUpdateMechanism() != deploymentinfo.MechanismBuiltIn {
		t.Errorf("PackageUpdateMechanism: got %v, want BuiltIn (default)", p.PackageUpdateMechanism())
	}
	if p.DefaultBranch() != "main" {
		t.Errorf("DefaultBranch: got %q, want main", p.DefaultBranch())
	}
	if p.IsExternalUpdateMechanism() {
		t.Errorf("IsExternalUpdateMechanism: got true, want false (BuiltIn default)")
	}
}

func TestNew_FileNameGate_NotBin(t *testing.T) {
	t.Parallel()
	// Even with a package_info file present, if bin folder name
	// isn't "bin" the C# gate skips the parse.
	bin := "/opt/sonarr/notbin"
	fs := fakeFS{contents: map[string]string{
		pkgPath(bin): "PackageVersion=1.2.3\n",
	}}
	p := deploymentinfo.New(bin, fs)
	if p.PackageVersion() != "" {
		t.Errorf("PackageVersion: got %q, want empty (bin gate should skip non-'bin' dirs)", p.PackageVersion())
	}
}

func TestNew_PackageInfoParse(t *testing.T) {
	t.Parallel()
	bin := "/usr/lib/sonarr/bin"
	fs := fakeFS{contents: map[string]string{
		pkgPath(bin): `PackageVersion=4.0.1.929-1
PackageAuthor=Servarr
PackageGlobalMessage=Important: backup before upgrade
UpdateMethod=Apt
UpdateMethodMessage=Use apt-get to upgrade
Branch=main
ReleaseVersion=4.0.1.929
`,
	}}
	p := deploymentinfo.New(bin, fs)
	if got := p.PackageVersion(); got != "4.0.1.929-1" {
		t.Errorf("PackageVersion: got %q", got)
	}
	if got := p.PackageAuthor(); got != "Servarr" {
		t.Errorf("PackageAuthor: got %q", got)
	}
	if got := p.PackageGlobalMessage(); got != "Important: backup before upgrade" {
		t.Errorf("PackageGlobalMessage: got %q", got)
	}
	if got := p.PackageUpdateMechanism(); got != deploymentinfo.MechanismApt {
		t.Errorf("PackageUpdateMechanism: got %v, want Apt", got)
	}
	if got := p.PackageUpdateMechanismMessage(); got != "Use apt-get to upgrade" {
		t.Errorf("PackageUpdateMechanismMessage: got %q", got)
	}
	if got := p.PackageBranch(); got != "main" {
		t.Errorf("PackageBranch: got %q", got)
	}
	if got := p.ReleaseVersion(); got != "4.0.1.929" {
		t.Errorf("ReleaseVersion: got %q", got)
	}
	if !p.IsExternalUpdateMechanism() {
		t.Errorf("IsExternalUpdateMechanism: got false (Apt = 11 >= External = 10 → want true)")
	}
	if p.DefaultUpdateMechanism() != deploymentinfo.MechanismApt {
		t.Errorf("DefaultUpdateMechanism: got %v, want Apt", p.DefaultUpdateMechanism())
	}
	if p.DefaultBranch() != "main" {
		t.Errorf("DefaultBranch: got %q, want main", p.DefaultBranch())
	}
}

func TestNew_ReleaseInfoOverridesReleaseVersionAndBranch(t *testing.T) {
	t.Parallel()
	bin := "/usr/lib/sonarr/bin"
	fs := fakeFS{contents: map[string]string{
		pkgPath(bin): `PackageVersion=4.0.0.1
ReleaseVersion=4.0.0.1
Branch=develop
`,
		relPath(bin): `ReleaseVersion=4.0.0.42
Branch=release
`,
	}}
	p := deploymentinfo.New(bin, fs)
	if got := p.ReleaseVersion(); got != "4.0.0.42" {
		t.Errorf("ReleaseVersion: got %q (release_info should override package_info)", got)
	}
	if got := p.ReleaseBranch(); got != "release" {
		t.Errorf("ReleaseBranch: got %q", got)
	}
	if got := p.PackageBranch(); got != "develop" {
		t.Errorf("PackageBranch: got %q (release_info shouldn't clobber)", got)
	}
	if got := p.DefaultBranch(); got != "release" {
		t.Errorf("DefaultBranch: got %q, want release (release_info wins)", got)
	}
}

func TestNew_ReleaseInfoOnly(t *testing.T) {
	t.Parallel()
	// No package_info, only release_info — tarball install shape.
	bin := "/opt/sonarr/bin"
	fs := fakeFS{contents: map[string]string{
		relPath(bin): "ReleaseVersion=4.0.0.500\nBranch=develop\n",
	}}
	p := deploymentinfo.New(bin, fs)
	if got := p.ReleaseVersion(); got != "4.0.0.500" {
		t.Errorf("ReleaseVersion: got %q", got)
	}
	if got := p.DefaultBranch(); got != "develop" {
		t.Errorf("DefaultBranch: got %q, want develop", got)
	}
	if p.IsExternalUpdateMechanism() {
		t.Errorf("IsExternalUpdateMechanism: tarball install should default to BuiltIn (not external)")
	}
}

func TestNew_UnknownUpdateMethodFallsBack(t *testing.T) {
	t.Parallel()
	bin := "/usr/lib/sonarr/bin"
	fs := fakeFS{contents: map[string]string{
		pkgPath(bin): "UpdateMethod=Flatpak\n",
	}}
	p := deploymentinfo.New(bin, fs)
	if p.PackageUpdateMechanism() != deploymentinfo.MechanismBuiltIn {
		t.Errorf("unknown UpdateMethod should fall back to BuiltIn; got %v", p.PackageUpdateMechanism())
	}
}

func TestNew_CaseInsensitiveUpdateMethod(t *testing.T) {
	t.Parallel()
	// C# uses Enum.TryParse(value, true, ...) — case-insensitive.
	bin := "/usr/lib/sonarr/bin"
	for _, in := range []string{"docker", "DOCKER", "Docker", " docker "} {
		in := in
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			fs := fakeFS{contents: map[string]string{
				pkgPath(bin): "UpdateMethod=" + in + "\n",
			}}
			p := deploymentinfo.New(bin, fs)
			if got := p.PackageUpdateMechanism(); got != deploymentinfo.MechanismDocker {
				t.Errorf("UpdateMethod=%q: got %v, want Docker", in, got)
			}
		})
	}
}

func TestNew_EmptyBranchKeepsDefault(t *testing.T) {
	t.Parallel()
	bin := "/usr/lib/sonarr/bin"
	fs := fakeFS{contents: map[string]string{
		pkgPath(bin): "PackageVersion=1.0\nBranch=\n",
	}}
	p := deploymentinfo.New(bin, fs)
	if got := p.PackageBranch(); got != "" {
		t.Errorf("PackageBranch: got %q, want empty", got)
	}
	if got := p.DefaultBranch(); got != "main" {
		t.Errorf("DefaultBranch: got %q, want main (whitespace-only Branch should not override)", got)
	}
}

func TestUpdateMechanism_StringRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		m    deploymentinfo.UpdateMechanism
		want string
	}{
		{deploymentinfo.MechanismBuiltIn, "BuiltIn"},
		{deploymentinfo.MechanismScript, "Script"},
		{deploymentinfo.MechanismExternal, "External"},
		{deploymentinfo.MechanismApt, "Apt"},
		{deploymentinfo.MechanismDocker, "Docker"},
	}
	for _, tc := range cases {
		if got := tc.m.String(); got != tc.want {
			t.Errorf("(%d).String() = %q, want %q", tc.m, got, tc.want)
		}
	}
}

func TestOSFileSystem_RoundTrip(t *testing.T) {
	t.Parallel()
	// Smoke-test the real-disk path against a t.TempDir fixture so
	// the wiring path used at boot time is covered.
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	pkgFile := filepath.Join(dir, "package_info")
	if err := os.WriteFile(pkgFile, []byte("PackageVersion=1.2.3\nBranch=main\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := deploymentinfo.New(binDir, deploymentinfo.OSFileSystem{})
	if got := p.PackageVersion(); got != "1.2.3" {
		t.Errorf("PackageVersion: got %q, want 1.2.3", got)
	}
	if got := p.PackageBranch(); got != "main" {
		t.Errorf("PackageBranch: got %q, want main", got)
	}
}
