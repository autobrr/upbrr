// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"testing"
)

func TestIndependentEditionControlsPersistResetAndInvalidate(t *testing.T) {
	source := writePreparedTestFile(t, "Example.Movie.2026.mkv", "media")
	store := newMemoryStore()
	module := newTestModule(t, store, &recordingCollector{})
	raw := api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{ReleaseName: api.ReleaseNameOverrides{
		Edition:         new("Custom wording"),
		Presentation:    new("Open Matte"),
		ReleaseFeatures: new([]api.ReleaseFeature{api.ReleaseFeatureHybrid, api.ReleaseFeature4KRestoration}),
	}}}
	first, err := module.ResolveInput(t.Context(), raw, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := newTestModule(t, store, &recordingCollector{}).ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		t.Fatal(err)
	}
	got := inherited.Input.Instructions.ReleaseName
	if got.Presentation == nil || *got.Presentation != "Open Matte" || got.ReleaseFeatures == nil || !slices.Equal(*got.ReleaseFeatures, *raw.Instructions.ReleaseName.ReleaseFeatures) {
		t.Fatal("independent controls lost after reload")
	}
	cleared, err := module.ResolveInput(t.Context(), first.Input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Presentation: new(""), ReleaseFeatures: new([]api.ReleaseFeature{})}}}})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Input.Instructions.ReleaseName.ReleaseFeatures == nil || len(*cleared.Input.Instructions.ReleaseName.ReleaseFeatures) != 0 || *cleared.Input.Instructions.ReleaseName.Edition != "Custom wording" {
		t.Fatal("None did not persist independently")
	}
	reset, err := module.ResolveInput(t.Context(), cleared.Input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNamePresentation}, {Field: api.CorrectionFieldReleaseNameReleaseFeatures}}}})
	if err != nil {
		t.Fatal(err)
	}
	if reset.Input.Instructions.ReleaseName.Presentation != nil || reset.Input.Instructions.ReleaseName.ReleaseFeatures != nil || *reset.Input.Instructions.ReleaseName.Edition != "Custom wording" {
		t.Fatal("Auto did not reset independently")
	}
	seen := map[string]bool{}
	for _, input := range []api.PrepareInput{first.Input, cleared.Input, reset.Input} {
		compatibility, err := preparationCompatibility(input, "source", 0)
		if err != nil {
			t.Fatal(err)
		}
		if seen[compatibility.FactInstructionFingerprint] {
			t.Fatal("different correction controls reuse an instruction fingerprint")
		}
		seen[compatibility.FactInstructionFingerprint] = true
	}
}

func TestPrepareRecollectsBeforeIndependentEditionControls(t *testing.T) {
	path := writePreparedTestFile(t, "Example.Movie.2026.mkv", "source")
	store := newMemoryStore()
	input := api.PrepareInput{SourcePath: path}
	previous, err := newTestModule(t, store, &recordingCollector{}).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	stale := previous.Release
	stale.Compatibility.ContractVersion = "prepared-release-v41"
	store.mu.Lock()
	store.current[canonicalSourceKey(path)] = stale
	store.mu.Unlock()
	collector := &clientEvidenceTestCollector{base: recordingCollector{}}
	current, err := newTestModule(t, store, collector).Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if collector.collectCount() != 1 || current.Release.Generation != previous.Release.Generation+1 {
		t.Fatal("old manual edition semantics reused")
	}
}

func TestManualCompoundCorrectionSurvivesEditionEditAndAuto(t *testing.T) {
	source := writePreparedTestFile(t, "Example.Movie.2026.mkv", "media")
	store := newMemoryStore()
	module := newTestModule(t, store, &recordingCollector{})
	initial, err := module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{ReleaseName: api.ReleaseNameOverrides{Edition: new("Director's Cut / Hybrid")}}}, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := module.ResolveInput(t.Context(), initial.Input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{Edition: new("Custom")}}}})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Input.Instructions.ReleaseName.ReleaseFeatures == nil || !slices.Equal(*changed.Input.Instructions.ReleaseName.ReleaseFeatures, []api.ReleaseFeature{api.ReleaseFeatureHybrid}) {
		t.Fatal("Edition edit lost compound feature")
	}
	reset, err := module.ResolveInput(t.Context(), changed.Input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameReleaseFeatures}}}})
	if err != nil {
		t.Fatal(err)
	}
	if reset.Input.Instructions.ReleaseName.ReleaseFeatures != nil || *reset.Input.Instructions.ReleaseName.Edition != "Custom" {
		t.Fatal("Auto resurrected compound feature")
	}
}
