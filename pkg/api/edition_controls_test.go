// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestIndependentEditionCorrectionLifecycle(t *testing.T) {
	features := []ReleaseFeature{ReleaseFeatureHybrid, ReleaseFeature4KRestoration}
	snapshot := ReleaseCorrectionsSnapshot{Corrections: StoredReleaseCorrectionsV1{Version: 1, ReleaseName: ReleaseNameOverrides{
		Edition:         new("Custom cut"),
		Presentation:    new("Open Matte"),
		ReleaseFeatures: &features,
	}}}
	update := ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{Values: ReleaseCorrectionValues{ReleaseName: ReleaseNameOverrides{Presentation: new(""), ReleaseFeatures: new([]ReleaseFeature{})}}}}
	cleared, err := ApplyReleaseCorrectionUpdate(snapshot, update)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.ReleaseName.Presentation == nil || *cleared.ReleaseName.Presentation != "" || cleared.ReleaseName.ReleaseFeatures == nil || len(*cleared.ReleaseName.ReleaseFeatures) != 0 || *cleared.ReleaseName.Edition != "Custom cut" {
		t.Fatalf("independent clear lost: %+v", cleared.ReleaseName)
	}
	encoded, err := json.Marshal(cleared.ReleaseName)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ReleaseNameOverrides
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ReleaseFeatures == nil || *decoded.ReleaseFeatures == nil {
		t.Fatalf("None became Auto: %s", encoded)
	}
	automatic, err := ApplyReleaseCorrectionUpdate(snapshot, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{ResetFields: []CorrectionFieldRef{{Field: CorrectionFieldReleaseNamePresentation}, {Field: CorrectionFieldReleaseNameReleaseFeatures}}}})
	if err != nil {
		t.Fatal(err)
	}
	if automatic.ReleaseName.Presentation != nil || automatic.ReleaseName.ReleaseFeatures != nil || *automatic.ReleaseName.Edition != "Custom cut" {
		t.Fatalf("independent reset lost: %+v", automatic.ReleaseName)
	}
	clone := cloneReleaseNameOverrides(snapshot.Corrections.ReleaseName)
	(*clone.ReleaseFeatures)[0] = ReleaseFeatureExtras
	if !slices.Equal(features, []ReleaseFeature{ReleaseFeatureHybrid, ReleaseFeature4KRestoration}) {
		t.Fatal("feature clone aliases source")
	}
}

func TestEditionCorrectionVocabulary(t *testing.T) {
	for _, value := range []ReleaseNameOverrides{{Presentation: new("Custom IMAX label")}, {ReleaseFeatures: new([]ReleaseFeature{"unknown"})}} {
		if err := (ReleaseCorrectionPatch{Values: ReleaseCorrectionValues{ReleaseName: value}}).Validate(); err == nil {
			t.Fatalf("invalid value accepted: %+v", value)
		}
	}
	for _, value := range []ReleaseNameOverrides{{Presentation: new(" imax open matte ")}, {ReleaseFeatures: new([]ReleaseFeature{ReleaseFeatureHybrid, ReleaseFeature4KRestoration})}} {
		if err := (ReleaseCorrectionPatch{Values: ReleaseCorrectionValues{ReleaseName: value}}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManualCompoundPersistsIndependentCorrections(t *testing.T) {
	legacy := ReleaseCorrectionsSnapshot{Corrections: StoredReleaseCorrectionsV1{Version: 1, ReleaseName: ReleaseNameOverrides{Edition: new("Director's Cut / Hybrid")}}}
	changed, err := ApplyReleaseCorrectionUpdate(legacy, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{Values: ReleaseCorrectionValues{ReleaseName: ReleaseNameOverrides{Edition: new("Custom wording")}}}})
	if err != nil {
		t.Fatal(err)
	}
	if *changed.ReleaseName.Edition != "Custom wording" || changed.ReleaseName.ReleaseFeatures == nil || !slices.Equal(*changed.ReleaseName.ReleaseFeatures, []ReleaseFeature{ReleaseFeatureHybrid}) {
		t.Fatalf("legacy feature erased by Edition edit: %+v", changed.ReleaseName)
	}
	automatic, err := ApplyReleaseCorrectionUpdate(legacy, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{ResetFields: []CorrectionFieldRef{{Field: CorrectionFieldReleaseNameReleaseFeatures}}}})
	if err != nil {
		t.Fatal(err)
	}
	if *automatic.ReleaseName.Edition != "Director's Cut" || automatic.ReleaseName.ReleaseFeatures != nil {
		t.Fatalf("Auto retained compound feature: %+v", automatic.ReleaseName)
	}
	explicit, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{}, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{Values: ReleaseCorrectionValues{ReleaseName: ReleaseNameOverrides{Edition: new("Director's Cut / Hybrid"), ReleaseFeatures: new([]ReleaseFeature{})}}}})
	if err != nil {
		t.Fatal(err)
	}
	if *explicit.ReleaseName.Edition != "Director's Cut" || explicit.ReleaseName.ReleaseFeatures == nil || len(*explicit.ReleaseName.ReleaseFeatures) != 0 {
		t.Fatal("explicit None lost to compound")
	}
}

func TestCompoundControlsRespectExistingExplicitAndReset(t *testing.T) {
	for _, existing := range [][]ReleaseFeature{{}, {ReleaseFeatureExtras}} {
		snapshot := ReleaseCorrectionsSnapshot{Corrections: StoredReleaseCorrectionsV1{Version: 1, ReleaseName: ReleaseNameOverrides{ReleaseFeatures: new(existing)}}}
		stored, err := ApplyReleaseCorrectionUpdate(snapshot, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{Values: ReleaseCorrectionValues{ReleaseName: ReleaseNameOverrides{Edition: new("Hybrid")}}}})
		if err != nil {
			t.Fatal(err)
		}
		if stored.ReleaseName.Edition == nil || *stored.ReleaseName.Edition != "" || stored.ReleaseName.ReleaseFeatures == nil || !slices.Equal(*stored.ReleaseName.ReleaseFeatures, existing) {
			t.Fatal("compound replaced independent explicit control")
		}
	}
	stored, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{}, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &ReleaseCorrectionPatch{Values: ReleaseCorrectionValues{ReleaseName: ReleaseNameOverrides{Edition: new("Hybrid")}}, ResetFields: []CorrectionFieldRef{{Field: CorrectionFieldReleaseNameReleaseFeatures}}}})
	if err != nil {
		t.Fatal(err)
	}
	if stored.ReleaseName.ReleaseFeatures != nil || stored.ReleaseName.Edition == nil || *stored.ReleaseName.Edition != "" {
		t.Fatal("reset lost to inferred compound")
	}
	both := SeparateEditionOverride("Open Matte / IMAX / Open Matte")
	if both.Presentation == nil || *both.Presentation != "IMAX Open Matte" {
		t.Fatal("compound presentation not canonical")
	}
}
