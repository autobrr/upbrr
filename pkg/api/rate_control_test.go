// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"testing"
)

func TestRateControlSurvivesPreparedFactProjection(t *testing.T) {
	files := MediaFileFacts{Files: []MediaFileFact{{ResourceID: "one", RateControl: VideoRateControlCRF}, {ResourceID: "two", RateControl: VideoRateControlCBR}}}
	encoded, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	var restored MediaFileFacts
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	subject := UploadSubject{MediaFileFacts: restored}
	validation := NewTrackerValidationSubject(subject, "LST")
	if validation.MediaFileFacts.Files[0].RateControl != VideoRateControlCRF || validation.MediaFileFacts.Files[1].RateControl != VideoRateControlCBR {
		t.Fatalf("projected rate control: %+v", validation.MediaFileFacts)
	}
	validation.MediaFileFacts.Files[1].RateControl = ""
	if subject.MediaFileFacts.Files[1].RateControl != VideoRateControlCBR {
		t.Fatal("projection mutated prepared facts")
	}
	var legacy MediaFileFact
	if err := json.Unmarshal([]byte(`{"ResourceID":"legacy","HasEncodeSettings":true}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.RateControl != "" {
		t.Fatal("legacy settings presence invented a mode")
	}
}
