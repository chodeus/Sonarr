package xbmc_test

import (
	"errors"
	"strings"
	"testing"

	"sonarr.tv/sonarr/internal/metadata"
	"sonarr.tv/sonarr/internal/metadata/xbmc"
)

// TestRegistration_DefaultRegistry verifies the init() side effect:
// metadata.Default() has a Constructor entry keyed on
// (ImplementationName, ConfigContractName).
//
// Acts as a defense against accidentally removing the init() block.
func TestRegistration_DefaultRegistry(t *testing.T) {
	t.Parallel()
	if !metadata.Default().Has(xbmc.ImplementationName, xbmc.ConfigContractName) {
		t.Errorf("metadata.Default() missing entry for (%q, %q)", xbmc.ImplementationName, xbmc.ConfigContractName)
	}
}

// TestRegistration_Construct exercises the constructor invoked via
// metadata.Default().Construct — the end-to-end production path.
func TestRegistration_Construct(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             7,
		Implementation: xbmc.ImplementationName,
		ConfigContract: xbmc.ConfigContractName,
		Settings:       `{"seriesMetadata":true,"episodeMetadata":false,"seriesImages":true,"seasonImages":true,"episodeImages":true}`,
	}
	deps := metadata.Dependencies{
		DiskProvider:     &fakeNfoFS{files: map[string]string{}},
		MapCoversToLocal: fakeCoverPath{},
	}
	p, err := metadata.Default().Construct(def, deps)
	if err != nil {
		t.Fatalf("Construct: %v", err)
	}
	if p.Name() != "Kodi (XBMC) / Emby" {
		t.Errorf("Name = %q", p.Name())
	}
	if p.Definition() == nil || p.Definition().ID != 7 {
		t.Errorf("Definition wrong: %+v", p.Definition())
	}
	// Confirm Settings.episodeMetadata=false survived the JSON
	// round-trip into the constructor.
	xm, ok := p.(*xbmc.XbmcMetadata)
	if !ok {
		t.Fatalf("Construct returned %T, want *xbmc.XbmcMetadata", p)
	}
	if xm.SettingsConfig().EpisodeMetadata {
		t.Errorf("Settings did not deserialise — EpisodeMetadata should be false")
	}
}

func TestRegistration_Construct_NilDiskProviderRejected(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             1,
		Implementation: xbmc.ImplementationName,
		ConfigContract: xbmc.ConfigContractName,
	}
	// No DiskProvider in deps — the Detector dep is REQUIRED.
	_, err := metadata.Default().Construct(def, metadata.Dependencies{})
	if err == nil {
		t.Errorf("expected error when deps.DiskProvider is nil")
	}
}

func TestRegistration_Construct_WrongDiskProviderTypeRejected(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             1,
		Implementation: xbmc.ImplementationName,
		ConfigContract: xbmc.ConfigContractName,
	}
	// DiskProvider doesn't satisfy NfoFilesystem (missing
	// GetFileSize + ReadAllText).
	_, err := metadata.Default().Construct(def, metadata.Dependencies{DiskProvider: 42})
	if err == nil {
		t.Errorf("expected error when DiskProvider doesn't satisfy NfoFilesystem")
	}
}

func TestRegistration_Construct_MalformedSettings(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             1,
		Implementation: xbmc.ImplementationName,
		ConfigContract: xbmc.ConfigContractName,
		Settings:       `{not json`,
	}
	deps := metadata.Dependencies{DiskProvider: &fakeNfoFS{files: map[string]string{}}}
	_, err := metadata.Default().Construct(def, deps)
	if err == nil {
		t.Errorf("expected error on malformed Settings JSON")
	}
}

func TestRegistration_Construct_EmptySettingsTakesDefaults(t *testing.T) {
	t.Parallel()
	for _, blob := range []string{"", "{}", "  "} {
		def := &metadata.Metadata{
			ID:             1,
			Implementation: xbmc.ImplementationName,
			ConfigContract: xbmc.ConfigContractName,
			Settings:       blob,
		}
		deps := metadata.Dependencies{DiskProvider: &fakeNfoFS{files: map[string]string{}}}
		p, err := metadata.Default().Construct(def, deps)
		if err != nil {
			t.Fatalf("blob=%q: %v", blob, err)
		}
		xm := p.(*xbmc.XbmcMetadata)
		if !xm.SettingsConfig().SeriesMetadata {
			t.Errorf("blob=%q: defaults not applied", blob)
		}
	}
}

// TestRegistration_Construct_NilCoverPathTolerated verifies that the
// optional MapCoversToLocal dep being unset doesn't fail construction
// — SeriesImages just degrades to empty.
func TestRegistration_Construct_NilCoverPathTolerated(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             1,
		Implementation: xbmc.ImplementationName,
		ConfigContract: xbmc.ConfigContractName,
	}
	deps := metadata.Dependencies{DiskProvider: &fakeNfoFS{files: map[string]string{}}}
	// No MapCoversToLocal — should still construct successfully.
	if _, err := metadata.Default().Construct(def, deps); err != nil {
		t.Errorf("nil MapCoversToLocal should be tolerated: %v", err)
	}
}

// TestRegistration_ErrorMessagesIncludeContext smoke-tests that the
// error chain carries the package prefix so logs are readable.
func TestRegistration_ErrorMessagesIncludeContext(t *testing.T) {
	t.Parallel()
	def := &metadata.Metadata{
		ID:             1,
		Implementation: xbmc.ImplementationName,
		ConfigContract: xbmc.ConfigContractName,
		Settings:       `{not json`,
	}
	deps := metadata.Dependencies{DiskProvider: &fakeNfoFS{files: map[string]string{}}}
	_, err := metadata.Default().Construct(def, deps)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, err) || !strings.Contains(err.Error(), "xbmc") {
		t.Errorf("error chain missing xbmc prefix: %v", err)
	}
}
