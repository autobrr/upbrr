// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"testing"
)

func TestPrepareRecollectsMixedEditionAfterRestart(t *testing.T) {
	path := writePreparedTestFile(t, "Example.Movie.2026.mkv", "source")
	store := newMemoryStore()
	input := api.PrepareInput{SourcePath: path}
	oldFacts := CollectedFacts{Media: api.MediaFacts{Edition: "Hybrid"}}
	previous, err := newTestModule(t, store, &recordingCollector{facts: &oldFacts}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	stale := previous.Release
	stale.Compatibility.ContractVersion = "prepared-release-v40"
	store.mu.Lock()
	store.current[canonicalSourceKey(path)] = stale
	store.mu.Unlock()
	facts := CollectedFacts{Media: api.MediaFacts{ReleaseFeatures: []api.ReleaseFeature{api.ReleaseFeatureHybrid}}}
	collector := &clientEvidenceTestCollector{base: recordingCollector{facts: &facts}}
	restarted := newTestModule(t, store, collector)
	current, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if collector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 || current.Release.Media.Edition != "" || !slices.Equal(current.Release.Media.ReleaseFeatures, facts.Media.ReleaseFeatures) {
		t.Fatalf("mixed edition reused: generation=%d calls=%d media=%+v", current.Release.Generation, collector.collectCount(), current.Release.Media)
	}
}
