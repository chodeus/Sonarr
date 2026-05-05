package episodefiles_test

import (
	"reflect"
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/datastore"
	"sonarr.tv/sonarr/internal/datastore/datastoretest"
	"sonarr.tv/sonarr/internal/episodefiles"
	"sonarr.tv/sonarr/internal/mediainfo"
)

// TestMediaInfo_TypedRoundTrip exercises the MediaInfo column at the
// three boundaries the mapper has to handle: NULL ↔ nil, empty
// struct ↔ "{}", and a fully-populated struct (every field exercised
// at least once) ↔ embedded JSON.
//
// Phase 3.14 changed File.MediaInfo from `string` to `*mediainfo.Model`;
// this test pins the round-trip so a future drift in either the
// mediainfo struct shape or the MarshalEmbedded helper is caught at
// the persistence layer.
func TestMediaInfo_TypedRoundTrip(t *testing.T) {
	t.Parallel()
	runMediaInfoRoundTrip(t, datastoretest.OpenSQLite(t))
}

func TestMediaInfo_TypedRoundTrip_Postgres(t *testing.T) {
	runMediaInfoRoundTrip(t, datastoretest.OpenPostgres(t))
}

func runMediaInfoRoundTrip(t *testing.T, db *datastore.DB) {
	t.Helper()
	r, err := episodefiles.NewRepo(db)
	if err != nil {
		t.Fatalf("NewRepo: %v", err)
	}
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	// 1. Nil pointer round-trips as SQL NULL — and the row participates
	//    in AllWithoutMediaInfo (the C# `MediaInfo IS NULL` filter).
	nilRow := &episodefiles.File{
		SeriesID: 1, SeasonNumber: 1, RelativePath: "n.mkv", Quality: "{}",
		Size: 1, DateAdded: now, Languages: []int{},
	}
	if _, err := r.Insert(t.Context(), nilRow); err != nil {
		t.Fatalf("Insert nil-MediaInfo: %v", err)
	}
	gotNil, err := r.Get(t.Context(), nilRow.ID)
	if err != nil {
		t.Fatalf("Get nil-MediaInfo: %v", err)
	}
	if gotNil.MediaInfo != nil {
		t.Errorf("nil round-trip: got %+v, want nil", gotNil.MediaInfo)
	}
	missing, err := r.AllWithoutMediaInfo(t.Context())
	if err != nil {
		t.Fatalf("AllWithoutMediaInfo: %v", err)
	}
	var sawNil bool
	for _, m := range missing {
		if m.ID == nilRow.ID {
			sawNil = true
			break
		}
	}
	if !sawNil {
		t.Errorf("AllWithoutMediaInfo: nil row not surfaced")
	}

	// 2. Empty struct round-trips. Insert {}, expect non-nil pointer to
	//    a zero-value Model on read. (Distinguishes "we have a record,
	//    it's just empty" from "no record yet".)
	emptyRow := &episodefiles.File{
		SeriesID: 1, SeasonNumber: 2, RelativePath: "e.mkv", Quality: "{}",
		Size: 1, DateAdded: now, Languages: []int{},
		MediaInfo: &mediainfo.Model{},
	}
	if _, err := r.Insert(t.Context(), emptyRow); err != nil {
		t.Fatalf("Insert empty-MediaInfo: %v", err)
	}
	gotEmpty, err := r.Get(t.Context(), emptyRow.ID)
	if err != nil {
		t.Fatalf("Get empty-MediaInfo: %v", err)
	}
	if gotEmpty.MediaInfo == nil {
		t.Errorf("empty round-trip: got nil, want zero-value pointer")
	} else if !reflect.DeepEqual(gotEmpty.MediaInfo, &mediainfo.Model{}) {
		t.Errorf("empty round-trip: got %+v, want zero", gotEmpty.MediaInfo)
	}

	// 3. Fully-populated struct round-trips byte-for-byte, including
	//    the camelCase wire shape the embedded-doc helpers produce
	//    and the EDOC-Q16 `videoCodecID` casing pin.
	full := &mediainfo.Model{
		SchemaRevision:               7,
		ContainerFormat:              "Matroska",
		VideoFormat:                  "AVC",
		VideoCodecID:                 "V_MPEG4/ISO/AVC",
		VideoProfile:                 "High@L4.1",
		VideoBitrate:                 8_500_000,
		VideoBitDepth:                10,
		VideoColourPrimaries:         "BT.709",
		VideoTransferCharacteristics: "BT.709",
		VideoHdrFormat:               mediainfo.HdrDolbyVisionHdr10,
		Height:                       1080,
		Width:                        1920,
		RunTime:                      datastore.STDuration(45 * time.Minute),
		VideoFps:                     23.976,
		ScanType:                     "Progressive",
		AudioStreams: []mediainfo.AudioStream{
			{Format: "AAC", CodecID: "A_AAC-2", Channels: 6, ChannelPositions: "5/0/1", Language: "eng", Bitrate: 384_000, Title: "Surround"},
			{Format: "AC-3", Channels: 2, Language: "deu"},
		},
		SubtitleStreams: []mediainfo.SubtitleStream{
			{Language: "eng", Format: "SRT"},
			{Language: "fra", Format: "PGS", Forced: ptr(true)},
		},
	}
	fullRow := &episodefiles.File{
		SeriesID: 2, SeasonNumber: 1, RelativePath: "f.mkv", Quality: "{}",
		Size: 1, DateAdded: now, Languages: []int{1, 2},
		MediaInfo: full,
	}
	if _, err := r.Insert(t.Context(), fullRow); err != nil {
		t.Fatalf("Insert full-MediaInfo: %v", err)
	}
	gotFull, err := r.Get(t.Context(), fullRow.ID)
	if err != nil {
		t.Fatalf("Get full-MediaInfo: %v", err)
	}
	if !reflect.DeepEqual(gotFull.MediaInfo, full) {
		t.Errorf("full round-trip mismatch:\n got: %+v\nwant: %+v", gotFull.MediaInfo, full)
	}

	// 4. Update path: flip nil → struct via Update.
	emptyRow.MediaInfo = full
	if err := r.Update(t.Context(), emptyRow); err != nil {
		t.Fatalf("Update populate: %v", err)
	}
	gotUpdated, err := r.Get(t.Context(), emptyRow.ID)
	if err != nil {
		t.Fatalf("Get updated: %v", err)
	}
	if !reflect.DeepEqual(gotUpdated.MediaInfo, full) {
		t.Errorf("update round-trip mismatch")
	}

	// 5. Update path: flip struct → nil via Update.
	emptyRow.MediaInfo = nil
	if err := r.Update(t.Context(), emptyRow); err != nil {
		t.Fatalf("Update clear: %v", err)
	}
	gotCleared, err := r.Get(t.Context(), emptyRow.ID)
	if err != nil {
		t.Fatalf("Get cleared: %v", err)
	}
	if gotCleared.MediaInfo != nil {
		t.Errorf("cleared round-trip: got %+v, want nil", gotCleared.MediaInfo)
	}
}

func ptr[T any](v T) *T { return &v }
