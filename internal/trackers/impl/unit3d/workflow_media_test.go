// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
)

func TestWorkflowMediaRequirementsRemainSiteOwnedAndDetached(t *testing.T) {
	t.Parallel()
	policy := &trackers.WorkflowMediaRequirements{AllowImportedScreenshotReferences: true}
	definition := NewWithProfile(Profile{
		Name:          "IMPORTER",
		BaseURL:       "https://tracker.example.invalid",
		WorkflowMedia: policy,
	})
	policy.AllowImportedScreenshotReferences = false
	got := definition.WorkflowMediaRequirements()
	if got == nil || !got.AllowImportedScreenshotReferences {
		t.Fatal("site media policy was not retained independently")
	}
	got.AllowImportedScreenshotReferences = false
	if !definition.WorkflowMediaRequirements().AllowImportedScreenshotReferences {
		t.Fatal("returned media policy aliases the definition")
	}
	registry := trackers.NewRegistry()
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	descriptor, ok := registry.LookupDescriptor("IMPORTER")
	if !ok || descriptor.WorkflowMedia == nil || !descriptor.WorkflowMedia.AllowImportedScreenshotReferences {
		t.Fatal("site media policy was not registered")
	}
	if NewWithProfile(Profile{Name: "OTHER"}).WorkflowMediaRequirements() != nil {
		t.Fatal("undeclared site inherited imported reference policy")
	}
}
