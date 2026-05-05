package downloadclients_test

import (
	"testing"
	"time"

	"sonarr.tv/sonarr/internal/downloadclients"
)

func TestDefaultTorrentSeedConfiguration_BothFieldsNil(t *testing.T) {
	t.Parallel()
	cfg := downloadclients.DefaultTorrentSeedConfiguration
	if cfg.Ratio != nil {
		t.Errorf("Default Ratio = %v, want nil", cfg.Ratio)
	}
	if cfg.SeedTime != nil {
		t.Errorf("Default SeedTime = %v, want nil", cfg.SeedTime)
	}
}

func TestTorrentSeedConfiguration_FieldRoundtrip(t *testing.T) {
	t.Parallel()
	ratio := 1.5
	seedTime := 6 * time.Hour
	cfg := downloadclients.TorrentSeedConfiguration{
		Ratio:    &ratio,
		SeedTime: &seedTime,
	}
	if cfg.Ratio == nil || *cfg.Ratio != 1.5 {
		t.Errorf("Ratio: %v", cfg.Ratio)
	}
	if cfg.SeedTime == nil || *cfg.SeedTime != 6*time.Hour {
		t.Errorf("SeedTime: %v", cfg.SeedTime)
	}
}
