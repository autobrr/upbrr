// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"testing"
)

func TestAudioBitDepthSurvivesLanguageProjectionAndJSON(t *testing.T) {
	facts := LanguageFacts{
		PrimaryAudioTrackID: "main",
		Tracks: []MediaTrackFacts{{
			ID:            "main",
			Kind:          MediaTrackAudio,
			Role:          AudioRoleProgramme,
			BitDepth:      24,
			ChannelsKnown: true,
		}},
	}
	subject := NewTrackerValidationSubject(UploadSubject{LanguageFacts: facts}, "LST")
	payload, err := json.Marshal(subject.LanguageFacts)
	if err != nil {
		t.Fatal(err)
	}
	var restored LanguageFacts
	if err := json.Unmarshal(payload, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.Tracks) != 1 || restored.Tracks[0].BitDepth != 24 || !restored.Tracks[0].ChannelsKnown || restored.PrimaryAudioTrackID != "main" {
		t.Fatalf("audio depth lost in projection: %+v", restored)
	}
	var legacy MediaTrackFacts
	if err := json.Unmarshal([]byte(`{"ID":"main","Kind":"audio"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.BitDepth != 0 || legacy.ChannelsKnown {
		t.Fatalf("legacy track invented depth %d", legacy.BitDepth)
	}
}
