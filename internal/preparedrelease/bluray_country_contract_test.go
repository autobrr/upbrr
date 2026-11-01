// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareRejectsPreCountryMatchGenerationUntilReprepared(t *testing.T) {
	for _, version := range []string{"prepared-release-v61", "prepared-release-v63", "prepared-release-v63-package-inventory"} {
		t.Run(version, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "Example.Movie.2026.GER.BluRay-GRP")
			for _, directory := range []string{"PLAYLIST", "STREAM"} {
				if err := os.MkdirAll(filepath.Join(source, "BDMV", directory), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(source, "BDMV", "PLAYLIST", "00001.MPLS"), []byte("synthetic playlist"), 0o600); err != nil {
				t.Fatal(err)
			}
			store := newMemoryStore()
			input := api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{Playlist: api.PlaylistInstruction{Set: true, Selected: []string{"00001.MPLS"}}}}
			previous, err := newTestModule(t, store, &recordingCollector{}).Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			stale := previous.Release
			stale.Compatibility.ContractVersion = version
			stale.Media.Region, stale.Naming.Region = "USA", "USA"
			store.mu.Lock()
			store.current[canonicalSourceKey(source)] = stale
			store.mu.Unlock()
			newFacts := CollectedFacts{
				Media:       stale.Media,
				Naming:      stale.Naming,
				Disc:        stale.Disc,
				Assessments: stale.Assessments,
			}
			newFacts.Media.Region, newFacts.Naming.Region = "GER", "GER"
			collector := &clientEvidenceTestCollector{base: recordingCollector{facts: &newFacts}}
			restarted := newTestModule(t, store, collector)
			required := input
			required.RequirePrepared = true
			required.ExternalFreshness = api.ExternalFreshnessLoad
			if reused, err := restarted.Prepare(t.Context(), required); err == nil {
				t.Fatalf("old automatic country remained uploadable: region=%s", reused.Release.Media.Region)
			}
			if collector.collectCount() != 0 {
				t.Fatal("required reuse unexpectedly recollected metadata")
			}
			current, err := restarted.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if collector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 || current.Release.Media.Region != "GER" || current.Release.Naming.Region != "GER" {
				t.Fatalf("old country facts reused: generation=%d calls=%d region=%s naming=%s", current.Release.Generation, collector.collectCount(), current.Release.Media.Region, current.Release.Naming.Region)
			}
			reused, err := restarted.Prepare(t.Context(), required)
			if err != nil || reused.Release.Generation != current.Release.Generation || collector.collectCount() != 1 {
				t.Fatalf("current prepared country not reusable: generation=%d calls=%d error=%v", reused.Release.Generation, collector.collectCount(), err)
			}
		})
	}
}
