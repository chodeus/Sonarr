package parser_test

import (
	"errors"
	"testing"

	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/series"
)

func TestValidateForSeriesType(t *testing.T) {
	t.Run("daily parse against Standard series → false", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.2024.01.15.Episode.Name.HDTV.x264-LOL")
		if got == nil || !got.IsDaily() {
			t.Fatalf("ParseTitle did not return a daily parse: %+v", got)
		}
		if parser.ValidateForSeriesType(got, series.SeriesTypeStandard) {
			t.Errorf("daily parse on Standard series should fail validation")
		}
	})

	t.Run("daily parse against Daily series → true", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.2024.01.15.Episode.Name.HDTV.x264-LOL")
		if got == nil || !got.IsDaily() {
			t.Fatalf("ParseTitle did not return a daily parse: %+v", got)
		}
		if !parser.ValidateForSeriesType(got, series.SeriesTypeDaily) {
			t.Errorf("daily parse on Daily series should pass validation")
		}
	})

	t.Run("non-daily parse against Standard series → true", func(t *testing.T) {
		got := parser.ParseTitle("Series.Title.S01E05.HDTV.XviD-LOL")
		if got == nil {
			t.Fatal("ParseTitle returned nil")
		}
		if got.IsDaily() {
			t.Fatal("expected non-daily parse")
		}
		if !parser.ValidateForSeriesType(got, series.SeriesTypeStandard) {
			t.Errorf("non-daily parse on Standard series should pass validation")
		}
	})

	t.Run("nil parsed → false", func(t *testing.T) {
		if parser.ValidateForSeriesType(nil, series.SeriesTypeStandard) {
			t.Errorf("nil ParsedEpisodeInfo should fail validation")
		}
	})
}

// TestErrSentinels confirms the sentinel error types are stable
// (errors.Is works) — defensive test for the reserved-for-future
// daily-validation slice.
func TestErrSentinels(t *testing.T) {
	if !errors.Is(parser.ErrInvalidDate, parser.ErrInvalidDate) {
		t.Error("ErrInvalidDate fails errors.Is identity check")
	}
	if !errors.Is(parser.ErrInvalidSeason, parser.ErrInvalidSeason) {
		t.Error("ErrInvalidSeason fails errors.Is identity check")
	}
	if errors.Is(parser.ErrInvalidDate, parser.ErrInvalidSeason) {
		t.Error("distinct sentinels should not be Is-equal")
	}
}
