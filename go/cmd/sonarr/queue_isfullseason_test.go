package main

import (
	"testing"

	"sonarr.tv/sonarr/internal/decisionengine"
	"sonarr.tv/sonarr/internal/parser"
	"sonarr.tv/sonarr/internal/queue"
)

// J415: projectQueueResource maps RemoteEpisode.ParsedEpisodeInfo.FullSeason
// onto QueueResource.IsFullSeason (C# QueueResource.cs:82).
func TestProjectQueueResource_IsFullSeason_J415(t *testing.T) {
	t.Parallel()

	fullSeason := queue.Queue{
		ID:            1,
		RemoteEpisode: &decisionengine.Subject{ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{FullSeason: true}},
	}
	if got := projectQueueResource(fullSeason); !got.IsFullSeason {
		t.Errorf("IsFullSeason = false, want true for a full-season release")
	}

	singleEp := queue.Queue{
		ID:            2,
		RemoteEpisode: &decisionengine.Subject{ParsedEpisodeInfo: &parser.ParsedEpisodeInfo{FullSeason: false}},
	}
	if got := projectQueueResource(singleEp); got.IsFullSeason {
		t.Errorf("IsFullSeason = true, want false for a single-episode release")
	}

	// nil RemoteEpisode / ParsedEpisodeInfo → false, no panic.
	if got := projectQueueResource(queue.Queue{ID: 3}); got.IsFullSeason {
		t.Errorf("IsFullSeason = true for nil RemoteEpisode, want false")
	}
	noInfo := queue.Queue{ID: 4, RemoteEpisode: &decisionengine.Subject{}}
	if got := projectQueueResource(noInfo); got.IsFullSeason {
		t.Errorf("IsFullSeason = true for nil ParsedEpisodeInfo, want false")
	}
}
