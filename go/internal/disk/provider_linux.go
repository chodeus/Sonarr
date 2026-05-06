//go:build linux

package disk

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// parseOctalMode parses a 3- or 4-digit octal permission string into
// an os.FileMode capped at 0o7777 (the Unix sticky/setuid/setgid +
// access-permission range). Empty / non-octal / over-range values
// surface as named errors. Linux-only because non-Linux builds skip
// the chmod path entirely.
func parseOctalMode(s string) (os.FileMode, error) {
	if s == "" {
		return 0, errors.New("empty permission mask")
	}
	n, err := strconv.ParseUint(s, 8, 16)
	if err != nil {
		return 0, fmt.Errorf("not an octal string: %s", s)
	}
	if n > 0o7777 {
		return 0, fmt.Errorf("mask too large: 0o%o", n)
	}
	return os.FileMode(n), nil
}

// setPermissions is the Linux concrete impl of `Provider.SetPermissions`
// — chmod(2) by parsed octal mask, then chown(2) by group name when
// non-empty. Ports `Mono/Disk/DiskProvider.cs:69-114`.
//
// Behaviour matches the C# body branch-for-branch:
//
//   - Mask < 4 chars preserves setuid/setgid/sticky bits from the
//     existing inode (`if (mask.Length < 4) { permissions |= curStat.st_mode & ~ACCESSPERMS; }`).
//   - chmod failure surfaces a wrapped error (C# throws
//     LinuxPermissionsException; Go port wraps the syscall error
//     verbatim so callers can `errors.Is(err, fs.ErrPermission)` etc.).
//   - chown is best-effort by group only (uid=-1 means "don't
//     change") — matches `Syscall.chown(path, -1, groupId)` at `:108`.
//   - Empty group skips the chown call entirely; matches the C#
//     `GetGroupId(group)` early-zero behaviour for empty input
//     (the C# helper returns 0 for unset, which chown rejects with
//     EPERM for non-root, masking the parity issue).
func (p *Provider) setPermissions(path, mask, group string) error {
	mode, err := parseOctalMode(mask)
	if err != nil {
		return fmt.Errorf("disk: SetPermissions(%q, %q): %w", path, mask, err)
	}

	if len(mask) < 4 {
		var st syscall.Stat_t
		if err := syscall.Stat(path, &st); err != nil {
			return fmt.Errorf("disk: SetPermissions(%q): stat: %w", path, err)
		}
		// Preserve setuid/setgid/sticky from the existing inode.
		// Lower 12 bits cover the full Unix permission range; 0o7777
		// AND with 0o7000 isolates the high three.
		preserve := os.FileMode(st.Mode) & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
		mode |= preserve
	}

	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("disk: SetPermissions(%q) chmod: %w", path, err)
	}

	if group == "" {
		return nil
	}
	g, err := user.LookupGroup(group)
	if err != nil {
		return fmt.Errorf("disk: SetPermissions(%q) lookup group %q: %w", path, group, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return fmt.Errorf("disk: SetPermissions(%q) parse gid %q: %w", path, g.Gid, err)
	}
	if err := os.Chown(path, -1, gid); err != nil {
		return fmt.Errorf("disk: SetPermissions(%q) chown: %w", path, err)
	}
	return nil
}
