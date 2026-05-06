package episodefiles

// DeletedFile is the value-type pair that the EpisodeImport writer
// returns for each pre-existing EpisodeFile it relocated to the
// recycle bin during an upgrade. Mirrors
// `src/NzbDrone.Core/MediaFiles/DeletedEpisodeFile.cs:1-15`.
//
// Renamed from C# `DeletedEpisodeFile` to `DeletedFile` because the
// package qualifier already says `episodefiles.` — `episodefiles.DeletedFile`
// reads better than `episodefiles.DeletedEpisodeFile` at every call
// site. The wider event-payload field (LocalEpisode.OldFiles
// []DeletedFile) keeps the same shape regardless.
//
// Pointer to *File: the writer returns a list of these alongside the
// new replacement file; downstream consumers (history writer, the
// EpisodeImported event emitter) need access to the old file's
// metadata for log lines and the IsRevisionUpgrade comparison.
// RecycleBinPath is empty when the recycling-bin feature is disabled
// or the row was hard-deleted.
type DeletedFile struct {
	File           *File
	RecycleBinPath string
}
