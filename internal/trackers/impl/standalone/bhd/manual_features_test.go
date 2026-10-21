// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhd

import (
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"testing"
)

func TestExplicitAnnotationNoneOverridesLegacyEditionFallback(t *testing.T) {
	subject := api.UploadSubject{Edition: "Custom Hybrid Open Matte wording", ReleaseNameOverrides: api.ReleaseNameOverrides{Presentation: new(""), ReleaseFeatures: new([]api.ReleaseFeature{})}}
	tags := resolveTags(subject)
	if slices.Contains(tags, "Hybrid") || slices.Contains(tags, "OpenMatte") {
		t.Fatalf("None restored from opaque edition: %v", tags)
	}
	subject.Presentation = "Open Matte"
	subject.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid}
	tags = resolveTags(subject)
	if !slices.Contains(tags, "Hybrid") || !slices.Contains(tags, "OpenMatte") {
		t.Fatalf("explicit facts lost: %v", tags)
	}
}
