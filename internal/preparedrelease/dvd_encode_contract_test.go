// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareRecollectsDVDEncodePresentationAfterRestart(t *testing.T) {
	path := writePreparedTestFile(t, "Example.Film.2026.DVD.mkv", "synthetic media")
	store := newMemoryStore()
	input := api.PrepareInput{SourcePath: path}
	oldFacts := dvdEncodeContractFacts("DVD", true)
	previous, err := newTestModule(t, store, &recordingCollector{facts: &oldFacts}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	stale := previous.Release
	stale.Compatibility.ContractVersion = "prepared-release-v39"
	store.mu.Lock()
	store.current[canonicalSourceKey(path)] = stale
	store.mu.Unlock()

	newFacts := dvdEncodeContractFacts("DVDRip", false)
	collector := &clientEvidenceTestCollector{base: recordingCollector{facts: &newFacts}}
	restarted := newTestModule(t, store, collector)
	current, err := restarted.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if collector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 ||
		current.Release.Compatibility.ContractVersion != ContractVersion {
		t.Fatalf("cached DVD encode reused: generation=%d calls=%d contract=%q", current.Release.Generation,
			collector.collectCount(), current.Release.Compatibility.ContractVersion)
	}
	component, found := current.Release.Naming.GeneratedName.Component(api.NameRoleSource)
	if !found || component.Value != "DVDRip" || component.Manual || current.Release.Naming.ReleaseName != "Example Film 2026 DVDRip" ||
		current.Release.Media.Type != "ENCODE" || current.Release.Media.Source != "DVD" || current.Release.Naming.Source != "DVD" {
		t.Fatalf("DVD encode recollection changed facts or retained old presentation: source=%+v naming=%+v media=%+v",
			component, current.Release.Naming, current.Release.Media)
	}
	reused, err := restarted.Prepare(t.Context(), input)
	if err != nil || collector.collectCount() != 1 || reused.Release.Generation != current.Release.Generation {
		t.Fatalf("current DVD facts not reused: generation=%d calls=%d err=%v", reused.Release.Generation, collector.collectCount(), err)
	}
}

func dvdEncodeContractFacts(label string, manual bool) CollectedFacts {
	return CollectedFacts{
		Media: api.MediaFacts{Type: "ENCODE", Source: "DVD"},
		Naming: api.NamingFacts{
			Type:        "ENCODE",
			Source:      "DVD",
			ReleaseName: "Example Film 2026 " + label,
			GeneratedName: &api.ReleaseNameDocument{
				Version: api.ReleaseNameDocumentVersionV1,
				Components: []api.ReleaseNameComponent{
					{
						Role:           api.NameRoleTitle,
						Value:          "Example Film",
						AvailableValue: "Example Film",
						Present:        true,
						Join:           " ",
					},
					{
						Role:           api.NameRoleYear,
						Value:          "2026",
						AvailableValue: "2026",
						Present:        true,
						Join:           " ",
					},
					{
						Role:           api.NameRoleSource,
						Value:          label,
						AvailableValue: label,
						Present:        true,
						Manual:         manual,
						Join:           " ",
					},
				},
			},
		},
	}
}
