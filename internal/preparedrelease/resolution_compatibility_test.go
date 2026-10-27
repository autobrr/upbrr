// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareRecomputesPreHeightFloorResolution(t *testing.T) {
	path := writePreparedTestFile(t, "Example.Movie.2026.mkv", "source")
	store := newMemoryStore()
	input := api.PrepareInput{SourcePath: path}
	previous, err := newTestModule(t, store, &recordingCollector{
		facts: &CollectedFacts{Naming: api.NamingFacts{Resolution: "540p"}},
	}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	stale := previous.Release
	stale.Compatibility.ContractVersion = "prepared-release-v53"
	store.mu.Lock()
	store.current[canonicalSourceKey(path)] = stale
	store.mu.Unlock()

	collector := &recordingCollector{facts: &CollectedFacts{Naming: api.NamingFacts{Resolution: "720p"}}}
	restarted := newTestModule(t, store, collector)
	current, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if current.Release.Generation != previous.Release.Generation+1 || current.Release.Naming.Resolution != "720p" ||
		current.Release.Compatibility.ContractVersion != ContractVersion {
		t.Fatalf("stale resolution reused: generation=%d resolution=%q", current.Release.Generation, current.Release.Naming.Resolution)
	}
	reused, err := restarted.Prepare(t.Context(), input)
	if err != nil || reused.Release.Generation != current.Release.Generation || collector.callCount() != 1 {
		t.Fatalf("corrected generation not reused: generation=%d calls=%d err=%v", reused.Release.Generation, collector.callCount(), err)
	}
}
