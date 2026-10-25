// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"testing"
)

func TestThreeDCorrectionPersistsAndResetsIndependently(t *testing.T) {
	var patch ReleaseCorrectionPatch
	if err := json.Unmarshal([]byte(`{"values":{"ReleaseName":{"ThreeD":true,"Edition":"Director's Cut"}}}`), &patch); err != nil {
		t.Fatal(err)
	}
	origin := TrackAudioOriginCorrection{
		TrackID:             "audio:1",
		ManifestFingerprint: "current-manifest",
		Origin:              TrackAudioOriginal,
	}
	patch.Values.Metadata.TrackAudioOrigins = []TrackAudioOriginCorrection{origin}
	stored, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{}, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &patch})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded StoredReleaseCorrectionsV1
	if err := json.Unmarshal(payload, &reloaded); err != nil {
		t.Fatal(err)
	}
	var clearPatch ReleaseCorrectionPatch
	if err := json.Unmarshal([]byte(`{"values":{"ReleaseName":{"ThreeD":false}}}`), &clearPatch); err != nil {
		t.Fatal(err)
	}
	cleared, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{Corrections: reloaded}, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &clearPatch})
	if err != nil {
		t.Fatal(err)
	}
	var reset ReleaseCorrectionPatch
	if err := json.Unmarshal([]byte(`{"values":{},"resetFields":[{"field":"release_name.three_d"}]}`), &reset); err != nil {
		t.Fatal(err)
	}
	auto, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{Corrections: cleared}, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &reset})
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]StoredReleaseCorrectionsV1{
		"set":   reloaded,
		"clear": cleared,
		"auto":  auto,
	} {
		if len(value.Metadata.TrackAudioOrigins) != 1 || value.Metadata.TrackAudioOrigins[0] != origin {
			t.Fatalf("%s changed the independent audio-origin correction", name)
		}
		data, err := json.Marshal(value.ReleaseName)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		expected := any(nil)
		if name == "set" {
			expected = true
		}
		if name == "clear" {
			expected = false
		}
		if fields["ThreeD"] != expected || fields["Edition"] != "Director's Cut" {
			t.Fatalf("%s = %s", name, data)
		}
	}
	audioReset := ReleaseCorrectionPatch{ResetFields: []CorrectionFieldRef{{Field: CorrectionFieldMetadataTrackAudioOrigins, TrackID: origin.TrackID}}}
	audioAuto, err := ApplyReleaseCorrectionUpdate(ReleaseCorrectionsSnapshot{Corrections: cleared}, ReleaseCorrectionUpdate{Mode: ReleaseCorrectionUpdatePatch, Patch: &audioReset})
	if err != nil {
		t.Fatal(err)
	}
	if len(audioAuto.Metadata.TrackAudioOrigins) != 0 || audioAuto.ReleaseName.ThreeD == nil || *audioAuto.ReleaseName.ThreeD {
		t.Fatal("audio-origin reset changed the independent 3D clear correction")
	}
}
