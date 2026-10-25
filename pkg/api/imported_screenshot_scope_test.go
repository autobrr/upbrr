// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestImportedScreenshotEvidenceRequiresExactPreparedAuthority(t *testing.T) {
	subject := UploadSubject{
		SourcePath: "source",
		MediaBinding: PreparedMediaBinding{
			SourcePath:               "source",
			PreparedGeneration:       3,
			PreparedMediaFingerprint: "media",
		},
		Identity: ExternalIdentity{
			SourcePath: "source",
			Generation: 3,
			Resolution: IdentityResolutionKey{SourceFingerprint: "source-fingerprint"},
		},
		ProviderMetadata: SourceScopedMetadata{
			SourcePath: "source",
			Generation: 3,
			ImportedScreenshotEvidence: &ImportedScreenshotEvidence{
				Tracker:                "AITHER",
				TrackerID:              "42",
				SourceFingerprint:      "source-fingerprint",
				PolicyVersion:          ImportedScreenshotEvidencePolicyVersion,
				DescriptionFingerprint: "description-images",
				References:             []ImportedScreenshotReference{{RawURL: "https://images.example/full"}},
			},
		},
	}
	got := subject.CurrentImportedScreenshotEvidence()
	if got == nil || len(got.References) != 1 {
		t.Fatal("current receipt was lost")
	}
	got.References[0].RawURL = "changed"
	if subject.ProviderMetadata.ImportedScreenshotEvidence.References[0].RawURL == "changed" {
		t.Fatal("receipt aliases canonical metadata")
	}
	validation := NewTrackerValidationSubject(subject, "LST")
	if validation.ImportedScreenshotEvidence == nil {
		t.Fatal("validation lost current receipt")
	}
	for _, mutate := range []func(*UploadSubject){
		func(s *UploadSubject) { s.ProviderMetadata.SourcePath = "" },
		func(s *UploadSubject) { s.ProviderMetadata.Generation++ },
		func(s *UploadSubject) { s.MediaBinding.PreparedGeneration++ },
		func(s *UploadSubject) { s.Identity.Resolution.SourceFingerprint = "other" },
		func(s *UploadSubject) { s.ProviderMetadata.ImportedScreenshotEvidence.PolicyVersion = "legacy" },
		func(s *UploadSubject) { s.ProviderMetadata.ImportedScreenshotEvidence.DescriptionFingerprint = "" },
	} {
		changed := subject
		changed.ProviderMetadata.ImportedScreenshotEvidence = subject.ProviderMetadata.ImportedScreenshotEvidence.Clone()
		mutate(&changed)
		if changed.CurrentImportedScreenshotEvidence() != nil {
			t.Fatal("stale or unproven receipt accepted")
		}
	}
}
