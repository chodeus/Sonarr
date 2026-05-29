// Boot-time adapter wiring the ManualImport output-folder cleanup arm
// (J451) to the disk provider + downloadedimport service. Satisfies
// handlers.ManualImportFolderCleaner by combining the disk provider's
// FolderExists / DeleteFolder with downloadedimport.Service's
// ShouldDeleteFolder — the same collaborators C# ManualImportService
// uses at `:606-615`.
package main

import (
	"sonarr.tv/sonarr/internal/disk"
	"sonarr.tv/sonarr/internal/downloadedimport"
	"sonarr.tv/sonarr/internal/series"
)

// manualImportFolderCleaner adapts the disk provider + downloadedimport
// service to the narrow handlers.ManualImportFolderCleaner interface.
type manualImportFolderCleaner struct {
	disk *disk.Provider
	dimp *downloadedimport.Service
}

func (c manualImportFolderCleaner) FolderExists(path string) bool {
	return c.disk.FolderExists(path)
}

func (c manualImportFolderCleaner) ShouldDeleteFolder(path string, ser *series.Series) bool {
	return c.dimp.ShouldDeleteFolder(path, ser)
}

func (c manualImportFolderCleaner) DeleteFolder(path string, recursive bool) error {
	return c.disk.DeleteFolder(path, recursive)
}
