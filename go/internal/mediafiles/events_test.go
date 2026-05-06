package mediafiles

import (
	"reflect"
	"testing"

	"sonarr.tv/sonarr/internal/series"
)

// TestSeriesScannedEventShape pins the exposed field set — Series
// pointer + PossibleExtraFiles slice. Drift here is the kind of
// silent behavioural change downstream listeners (extras-import,
// metadata-import) would miss until runtime.
func TestSeriesScannedEventShape(t *testing.T) {
	s := &series.Series{ID: 7}
	files := []string{"/x/poster.jpg", "/x/episode.nfo"}
	ev := SeriesScannedEvent{Series: s, PossibleExtraFiles: files}
	if ev.Series != s {
		t.Fatalf("Series: got %v want %v", ev.Series, s)
	}
	if !reflect.DeepEqual(ev.PossibleExtraFiles, files) {
		t.Fatalf("PossibleExtraFiles: got %v want %v", ev.PossibleExtraFiles, files)
	}

	typ := reflect.TypeOf(SeriesScannedEvent{})
	if got := typ.NumField(); got != 2 {
		t.Fatalf("SeriesScannedEvent: got %d fields, want 2", got)
	}
}

// TestSeriesScanSkippedEventShape pins the exposed field set —
// Series pointer + Reason. Same drift-guard motivation as above.
func TestSeriesScanSkippedEventShape(t *testing.T) {
	s := &series.Series{ID: 11}
	ev := SeriesScanSkippedEvent{Series: s, Reason: SeriesScanSkippedReasonRootFolderIsEmpty}
	if ev.Series != s {
		t.Fatalf("Series: got %v want %v", ev.Series, s)
	}
	if ev.Reason != SeriesScanSkippedReasonRootFolderIsEmpty {
		t.Fatalf("Reason: got %v want RootFolderIsEmpty", ev.Reason)
	}

	typ := reflect.TypeOf(SeriesScanSkippedEvent{})
	if got := typ.NumField(); got != 2 {
		t.Fatalf("SeriesScanSkippedEvent: got %d fields, want 2", got)
	}
}

// TestSeriesScanSkippedReasonOrder pins the numeric order of the four
// reason entries — same order as the C# enum at
// `SeriesScanSkippedEvent.cs:18-24`. A reorder would silently change
// any persisted-int representation.
func TestSeriesScanSkippedReasonOrder(t *testing.T) {
	cases := []struct {
		got  SeriesScanSkippedReason
		want int
		name string
	}{
		{SeriesScanSkippedReasonRootFolderDoesNotExist, 0, "RootFolderDoesNotExist"},
		{SeriesScanSkippedReasonRootFolderIsEmpty, 1, "RootFolderIsEmpty"},
		{SeriesScanSkippedReasonNeverRescanAfterRefresh, 2, "NeverRescanAfterRefresh"},
		{SeriesScanSkippedReasonRescanAfterManualRefreshOnly, 3, "RescanAfterManualRefreshOnly"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if int(tc.got) != tc.want {
				t.Fatalf("ordinal: got %d want %d", int(tc.got), tc.want)
			}
			if tc.got.String() != tc.name {
				t.Fatalf("String: got %q want %q", tc.got.String(), tc.name)
			}
		})
	}
}

// TestSeriesScanSkippedReasonStringUnknown pins the fallback for an
// out-of-range value — keeps log messages parseable even if a
// future contributor adds an enum entry without updating String.
func TestSeriesScanSkippedReasonStringUnknown(t *testing.T) {
	r := SeriesScanSkippedReason(99)
	if got := r.String(); got != "Unknown" {
		t.Fatalf("String for out-of-range: got %q want Unknown", got)
	}
}
