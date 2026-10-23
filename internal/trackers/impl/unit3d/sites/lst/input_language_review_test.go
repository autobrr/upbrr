// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestInputLanguageReviewPreservesResourceScopeAndAdvisoryEvidence(t *testing.T) {
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(Profile())); err != nil {
		t.Fatal(err)
	}
	validation := resourceScopeSubject()
	for _, index := range []int{0, 2} {
		validation.LanguageFacts.Tracks[index].Languages = nil
		validation.LanguageFacts.Tracks[index].ManifestFingerprint = "current"
	}
	subject := api.UploadSubject{Type: validation.Type, LanguageFacts: validation.LanguageFacts}
	check := func(want string) {
		t.Helper()
		evaluation, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{"LST"}, subject)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, field := range evaluation.Fields {
			if field.TrackID != "" {
				ids = append(ids, field.TrackID)
			}
		}
		if want == "" && len(ids) != 0 || want != "" && (len(ids) != 1 || ids[0] != want) {
			t.Fatalf("wrong required resource tracks: %v", ids)
		}
	}
	check("b")
	subject.LanguageFacts.Tracks[2].Languages = []string{"Japanese"}
	check("")
}
