// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestExplicitScreenshotSelectionDoesNotAppendEightImportedReferences(t *testing.T) {
	registry := trackers.NewRegistry()
	if err := registry.Register(unit3d.NewWithProfile(Profile())); err != nil {
		t.Fatal(err)
	}
	meta := api.UploadSubject{
		SourcePath: "source",
		MediaBinding: api.PreparedMediaBinding{
			SourcePath:               "source",
			PreparedGeneration:       7,
			PreparedMediaFingerprint: "media",
		},
		Identity: api.ExternalIdentity{
			SourcePath: "source",
			Generation: 7,
			Resolution: api.IdentityResolutionKey{SourceFingerprint: "source"},
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "source",
			Generation: 7,
			ImportedScreenshotEvidence: &api.ImportedScreenshotEvidence{
				Tracker:                "SOURCE",
				TrackerID:              "42",
				SourceFingerprint:      "source",
				PolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
				DescriptionFingerprint: "description",
			},
		},
		Options: api.UploadOptions{Screens: 3},
	}
	for i := range 8 {
		meta.ProviderMetadata.ImportedScreenshotEvidence.References = append(meta.ProviderMetadata.ImportedScreenshotEvidence.References, api.ImportedScreenshotReference{RawURL: fmt.Sprintf("https://images2.imgbox.com/synthetic/imported-%d.png", i)})
	}
	selected := "[img]https://images2.imgbox.com/selected/one.png[/img]\n[img]https://images2.imgbox.com/selected/two.png[/img]\n[img]https://images2.imgbox.com/selected/three.png[/img]"
	for _, explicit := range []bool{false, true} {
		subject := meta
		if explicit {
			subject.SuppressImportedScreenshotReferences = map[string]bool{"LST": true}
		}
		subject = trackers.FilterImportedScreenshotEvidenceForTrackerWithRegistry(registry, config.Config{}, "LST", subject)
		got := Profile().Site.FinalizeDescription(selected, subject)
		want := 11
		if explicit {
			want = 3
		}
		if strings.Count(got, "[img]") != want {
			t.Fatalf("explicit=%v produced %d images, want %d: %s", explicit, strings.Count(got, "[img]"), want, got)
		}
		facts := withImportedReferenceFacts(api.NewTrackerValidationSubject(subject, "LST"))
		if explicit && facts.ImportedScreenshotEvidence != nil {
			t.Fatal("final facts retained unrendered references")
		}
	}
	if len(meta.CurrentImportedScreenshotEvidence().References) != 8 || meta.ExactMedia != nil {
		t.Fatal("selection policy mutated canonical evidence or created artifacts")
	}
}
