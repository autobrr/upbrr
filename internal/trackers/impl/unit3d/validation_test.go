// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestValidationAdapterPreservesEditionCategories(t *testing.T) {
	t.Parallel()
	original := api.UploadSubject{
		EditionSet:   "2in1",
		Cut:          "Extended",
		Edition:      "Collector's",
		Presentation: "Open Matte",
	}
	validated := api.NewTrackerValidationSubject(original, "EXAMPLE")
	restored := unit3DUploadSubject(validated)
	if restored.EditionSet != original.EditionSet || restored.Cut != original.Cut || restored.Edition != original.Edition || restored.Presentation != original.Presentation {
		t.Fatalf("adapter lost finalized edition categories: %#v", restored)
	}
}
