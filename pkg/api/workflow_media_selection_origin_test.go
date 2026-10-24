// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMediaAutomaticScreenshotSelectionSurvivesPersistence(t *testing.T) {
	for _, input := range []string{`{}`, `{"automaticScreenshotSelection":true}`} {
		var media MediaArtifactSet
		if err := json.Unmarshal([]byte(input), &media); err != nil {
			t.Fatal(err)
		}
		cloned, err := media.Clone()
		if err != nil {
			t.Fatal(err)
		}
		output, err := json.Marshal(cloned)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Contains(string(output), `"automaticScreenshotSelection":true`), strings.Contains(input, "true"); got != want {
			t.Fatalf("automatic screenshot origin lost across persistence: input=%s output=%s", input, output)
		}
	}
}

func TestDescriptionScreenshotSelectionPolicyIsDetachedAndPrivate(t *testing.T) {
	original := UploadSubject{SuppressImportedScreenshotReferences: map[string]bool{"LST": true}}
	description := NewDescriptionSubject(original)
	description.SuppressImportedScreenshotReferences["LST"] = false
	if !original.SuppressImportedScreenshotReferences["LST"] {
		t.Fatal("description mutated source policy")
	}
	for _, subject := range []any{original, description} {
		payload, err := json.Marshal(subject)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(payload), "SuppressImportedScreenshotReferences") {
			t.Fatal("operation-local policy entered transport JSON")
		}
	}
}
