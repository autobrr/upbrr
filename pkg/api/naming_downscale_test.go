// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"testing"
)

func TestReleaseNameDocumentPersistsHiddenDownscale(t *testing.T) {
	t.Parallel()
	document := ReleaseNameDocument{
		Version: ReleaseNameDocumentVersionV1,
		Components: []ReleaseNameComponent{
			{
				Role:           NameRoleTitle,
				Value:          "Example",
				AvailableValue: "Example",
				Present:        true,
			},
			{
				Role:           NameRoleDownscale,
				Value:          "DS4K",
				AvailableValue: "DS4K",
				Join:           " ",
			},
		},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var restored ReleaseNameDocument
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatal(err)
	}
	marker, ok := restored.Component(NameRoleDownscale)
	if !ok || marker.Present || marker.AvailableValue != "DS4K" || restored.Render().Name != "Example" {
		t.Fatalf("hidden source evidence did not survive: %#v", restored)
	}
	clone := restored.Clone()
	clone.Components[1].Present = true
	if clone.Render().Name != "Example DS4K" || restored.Render().Name != "Example" {
		t.Fatal("projection changed canonical rendering")
	}
}
