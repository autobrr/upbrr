// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestThreeDCorrectionReloadResetAndFingerprint(t *testing.T) {
	source := writePreparedTestFile(t, "Example.Movie.2026.mkv", "media")
	store := newMemoryStore()
	module := newTestModule(t, store, &recordingCollector{})
	first, err := module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{ReleaseName: api.ReleaseNameOverrides{Edition: new("Extended"), ThreeD: new(true)}}}, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := newTestModule(t, store, &recordingCollector{}).ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Input.Instructions.ReleaseName.ThreeD == nil || !*reloaded.Input.Instructions.ReleaseName.ThreeD {
		t.Fatal("reload lost Set")
	}
	cleared, err := module.ResolveInput(t.Context(), first.Input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{ReleaseName: api.ReleaseNameOverrides{ThreeD: new(false)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Input.Instructions.ReleaseName.ThreeD == nil || *cleared.Input.Instructions.ReleaseName.ThreeD {
		t.Fatal("Clear lost presence")
	}
	auto, err := module.ResolveInput(t.Context(), cleared.Input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldReleaseNameThreeD}}}})
	if err != nil {
		t.Fatal(err)
	}
	if auto.Input.Instructions.ReleaseName.ThreeD != nil || auto.Input.Instructions.ReleaseName.Edition == nil || *auto.Input.Instructions.ReleaseName.Edition != "Extended" {
		t.Fatal("Auto failed independent reset")
	}
	seen := map[string]bool{}
	for _, input := range []api.PrepareInput{first.Input, cleared.Input, auto.Input} {
		compatibility, err := preparationCompatibility(input, "source", 0)
		if err != nil {
			t.Fatal(err)
		}
		if seen[compatibility.FactInstructionFingerprint] {
			t.Fatal("3D states reused fingerprint")
		}
		seen[compatibility.FactInstructionFingerprint] = true
	}
}
