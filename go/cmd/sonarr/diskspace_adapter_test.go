package main

import "testing"

// specialDriveRegex must match the C# _regexSpecialDrive denylist
// (DiskSpaceService.cs:26) — container/system mounts excluded from the
// optional disk list, real data mounts kept.
func TestSpecialDriveRegex_J412(t *testing.T) {
	t.Parallel()
	excluded := []string{
		"/var/lib/docker",
		"/var/lib/docker/overlay2",
		"/var/lib/rancher",
		"/var/lib/kubelet/pods",
		"/boot",
		"/boot/efi",
		"/etc",
		"/etc/hostname",
		"/docker/aufs",
		"/docker/var/aufs/mnt",
		"/some/.timemachine/backup",
	}
	for _, p := range excluded {
		if !specialDriveRegex.MatchString(p) {
			t.Errorf("expected %q to be excluded (special drive)", p)
		}
	}

	kept := []string{
		"/",
		"/mnt/media",
		"/data",
		"/var/lib/sonarr", // not docker/rancher/kubelet
		"/etcetera",       // not /etc boundary
		"/home/user",
		"/boots", // not /boot boundary
	}
	for _, p := range kept {
		if specialDriveRegex.MatchString(p) {
			t.Errorf("expected %q to be kept (not a special drive)", p)
		}
	}
}
