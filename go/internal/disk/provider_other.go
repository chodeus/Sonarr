//go:build !linux

package disk

// setPermissions is the non-Linux build's `Provider.SetPermissions`
// implementation — a debug-logged no-op. Mirrors the C#
// `WindowsDiskProvider` SetPermissions override (an empty body;
// permissions are NTFS ACLs which the C# tree handles via a
// different code path that hasn't ported yet).
//
// macOS callers see the same shape: SetPermissions returns nil and
// the on-disk mode is unchanged. The mediafiles.Service layer
// already swallows SetPermissions errors per the C# `try/catch` at
// `DiskScanService.cs:281-285`, so this no-op is observable only in
// debug logs.
func (p *Provider) setPermissions(path, mask, group string) error {
	p.log.Debug("disk: SetPermissions skipped on non-Linux build",
		"path", path, "mask", mask, "group", group)
	return nil
}
