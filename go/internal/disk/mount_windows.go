//go:build windows

package disk

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// platformGetMount reads volume info via GetVolumeInformationW.
//
// The C# tree uses `DriveInfo` which wraps `GetVolumeInformationW`
// + `GetVolumeMountPointW`. We narrow to GetVolumeInformationW for
// the read-only flag (FILE_READ_ONLY_VOLUME = 0x80000) + the volume
// label as the mount Name.
//
// Mount root resolution: for `C:\Some\Path` the root is `C:\` (the
// volume root). `filepath.VolumeName` extracts the drive letter +
// colon; we suffix the backslash to match the C# `DriveInfo.Root`
// shape.
func platformGetMount(path string) (*Mount, error) {
	volumeRoot := filepath.VolumeName(path)
	if volumeRoot == "" {
		return nil, fmt.Errorf("disk: GetMount(%q): no volume in path", path)
	}
	if volumeRoot[len(volumeRoot)-1] != filepath.Separator {
		volumeRoot += string(filepath.Separator)
	}

	rootPtr, err := windows.UTF16PtrFromString(volumeRoot)
	if err != nil {
		return nil, fmt.Errorf("disk: GetMount: encode root %q: %w", volumeRoot, err)
	}
	var (
		volumeNameBuf [windows.MAX_PATH + 1]uint16
		fsNameBuf     [windows.MAX_PATH + 1]uint16
		volumeSerial  uint32
		maxComponent  uint32
		fsFlags       uint32
	)
	if err := windows.GetVolumeInformation(
		rootPtr,
		&volumeNameBuf[0], uint32(len(volumeNameBuf)),
		&volumeSerial,
		&maxComponent,
		&fsFlags,
		&fsNameBuf[0], uint32(len(fsNameBuf)),
	); err != nil {
		return nil, fmt.Errorf("disk: GetVolumeInformation(%q): %w", volumeRoot, err)
	}

	// FILE_READ_ONLY_VOLUME = 0x00080000 — winnt.h.
	const fileReadOnlyVolume = 0x00080000
	readOnly := fsFlags&fileReadOnlyVolume != 0

	name := windows.UTF16PtrToString(&volumeNameBuf[0])
	if name == "" {
		// Fallback to the volume root path so the healthcheck message
		// has a non-empty identifier.
		name = volumeRoot
	}

	return &Mount{
		Name:          name,
		RootDirectory: volumeRoot,
		IsReadOnly:    readOnly,
	}, nil
}
