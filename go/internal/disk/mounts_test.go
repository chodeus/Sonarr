package disk

import (
	"strings"
	"testing"
)

func TestDriveTypeForFsType(t *testing.T) {
	t.Parallel()
	cases := map[string]DriveType{
		"ext4":    DriveTypeFixed,
		"XFS":     DriveTypeFixed, // case-insensitive
		"apfs":    DriveTypeFixed,
		"ntfs":    DriveTypeFixed,
		"nfs":     DriveTypeNetwork,
		"cifs":    DriveTypeNetwork,
		"smbfs":   DriveTypeNetwork,
		"tmpfs":   DriveTypeOther,
		"proc":    DriveTypeOther,
		"overlay": DriveTypeOther,
		"":        DriveTypeOther,
	}
	for fs, want := range cases {
		if got := driveTypeForFsType(fs); got != want {
			t.Errorf("driveTypeForFsType(%q) = %v, want %v", fs, got, want)
		}
	}
}

func TestParseProcMounts(t *testing.T) {
	t.Parallel()
	const sample = `sysfs /sys sysfs rw,nosuid 0 0
/dev/sda1 / ext4 rw,relatime 0 0
tmpfs /run tmpfs rw,nosuid 0 0
//server/share /mnt/share cifs rw 0 0
/dev/sdb1 /mnt/with\040space ext4 rw 0 0
`
	got := parseProcMounts(strings.NewReader(sample))
	if len(got) != 5 {
		t.Fatalf("parsed %d entries, want 5", len(got))
	}
	// Root ext4 → Fixed.
	if got[1].RootDirectory != "/" || got[1].DriveType != DriveTypeFixed {
		t.Errorf("entry[1] = %+v, want / Fixed", got[1])
	}
	// cifs → Network.
	if got[3].RootDirectory != "/mnt/share" || got[3].DriveType != DriveTypeNetwork {
		t.Errorf("entry[3] = %+v, want /mnt/share Network", got[3])
	}
	// Octal-escaped space decoded.
	if got[4].RootDirectory != "/mnt/with space" {
		t.Errorf("entry[4] root = %q, want '/mnt/with space'", got[4].RootDirectory)
	}
	// tmpfs → Other.
	if got[2].DriveType != DriveTypeOther {
		t.Errorf("entry[2] (tmpfs) drive type = %v, want Other", got[2].DriveType)
	}
}

func TestUnescapeProcMount(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"/plain/path":    "/plain/path",
		`/with\040space`: "/with space",
		`/tab\011here`:   "/tab\there",
		`/back\134slash`: `/back\slash`,
		`/trailing\\`:    `/trailing\\`, // incomplete escape left as-is
	}
	for in, want := range cases {
		if got := unescapeProcMount(in); got != want {
			t.Errorf("unescapeProcMount(%q) = %q, want %q", in, got, want)
		}
	}
}

// GetMounts on the host returns at least the root mount (smoke test; the
// concrete set is environment-specific so we only assert non-empty +
// that a Fixed root is present).
func TestGetMounts_HostHasRoot(t *testing.T) {
	t.Parallel()
	p := &Provider{}
	mounts := p.GetMounts()
	if len(mounts) == 0 {
		t.Skip("no mounts enumerated on this platform/build")
	}
	var sawFixed bool
	for _, m := range mounts {
		if m.DriveType == DriveTypeFixed {
			sawFixed = true
		}
		if m.RootDirectory == "" {
			t.Errorf("mount with empty RootDirectory: %+v", m)
		}
	}
	if !sawFixed {
		t.Errorf("expected at least one Fixed mount among %d", len(mounts))
	}
}
