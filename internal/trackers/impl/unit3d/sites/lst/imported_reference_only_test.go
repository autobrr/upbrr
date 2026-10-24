// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestUninspectedImportedReferencesRemainPassive(t *testing.T) {
	subject := lstValidationSubject()
	subject.AssetFacts.Screenshots = api.AssetEvidence{}
	subject.AssetFacts.HostedScreenshots = api.AssetEvidence{}
	subject.AssetFacts.ScreenshotReferences = nil
	subject.ImportedScreenshotEvidence = &api.ImportedScreenshotEvidence{}
	for i := range 8 {
		subject.ImportedScreenshotEvidence.References = append(subject.ImportedScreenshotEvidence.References,
			api.ImportedScreenshotReference{RawURL: fmt.Sprintf("https://images.example.invalid/shot-%d.png", i)})
	}
	// Repeated source tags do not establish additional images.
	subject.ImportedScreenshotEvidence.References = append(subject.ImportedScreenshotEvidence.References,
		subject.ImportedScreenshotEvidence.References[0])
	failures := assetReviewFailures(subject)
	if len(failures) != 1 || failures[0].Rule != "guidance_lst_screenshot_format" ||
		failures[0].Disposition != api.RuleDispositionAdvisory || !strings.Contains(failures[0].Reason, "0 of 8") ||
		!strings.Contains(failures[0].Reason, "8 format unknown") {
		t.Fatalf("uninspected references must retain actual passive counts: %+v", failures)
	}
	if failures[0].QuestionnaireInput {
		t.Fatal("unavailable inspection must not ask for confirmation")
	}
	subject.AssetFacts.Status = api.MetadataEvidenceStatusUnavailable
	if beforeMedia := assetReviewFailures(subject); len(beforeMedia) != 1 || !strings.Contains(beforeMedia[0].Reason, "0 of 8") {
		t.Fatalf("current imported evidence was hidden before local media capture: %+v", beforeMedia)
	}
}

func TestImportedReferenceDescriptionUsesOnlyCurrentFullURLs(t *testing.T) {
	finalize := Profile().Site.FinalizeDescription
	if finalize == nil {
		t.Fatal("LST does not render accepted reference-only screenshots")
	}
	path := filepath.Join(t.TempDir(), "source.mkv")
	meta := api.UploadSubject{
		SourcePath: path,
		MediaBinding: api.PreparedMediaBinding{
			SourcePath:               path,
			PreparedGeneration:       7,
			PreparedMediaFingerprint: "media",
		},
		Identity:         api.ExternalIdentity{SourcePath: path, Generation: 7},
		ProviderMetadata: api.SourceScopedMetadata{SourcePath: path, Generation: 7},
	}
	meta.Identity.Resolution.SourceFingerprint = "source"
	meta.ProviderMetadata.ImportedScreenshotEvidence = &api.ImportedScreenshotEvidence{
		Tracker:                "SOURCE",
		TrackerID:              "42",
		SourceFingerprint:      "source",
		PolicyVersion:          api.ImportedScreenshotEvidencePolicyVersion,
		DescriptionFingerprint: "description",
		References: []api.ImportedScreenshotReference{
			{RawURL: "https://images.example.invalid/first.png"},
			{RawURL: "https://images.example.invalid/second.png"},
			{RawURL: "https://images.example.invalid/second.png"},
		},
	}
	before := "Notes\n[img]https://images.example.invalid/first.png[/img]"
	got := finalize(before, meta)
	if strings.Count(got, "first.png") != 1 || strings.Count(got, "second.png") != 1 || !strings.Contains(got, "[img]https://images.example.invalid/second.png[/img]") {
		t.Fatalf("reference rendering changed distinct source URLs: %s", got)
	}
	if again := finalize(got, meta); again != got {
		t.Fatalf("repeated rendering added duplicate screenshots: %s", again)
	}
	meta.ProviderMetadata.Generation++
	if stale := finalize(before, meta); stale != before {
		t.Fatal("stale generation added reference-only screenshots")
	}
}

func TestImportedReferenceDoesNotDuplicateInspectedImage(t *testing.T) {
	subject := lstValidationSubject()
	subject.AssetFacts.Screenshots.Count = 3
	subject.AssetFacts.Screenshots.InspectedFormatCount = 3
	subject.AssetFacts.Screenshots.LosslessCount = 3
	subject.AssetFacts.ScreenshotReferences = []api.ScreenshotReference{{ID: "selected", RawURL: "https://images.example.invalid/shot.png"}}
	subject.ImportedScreenshotEvidence = &api.ImportedScreenshotEvidence{References: []api.ImportedScreenshotReference{
		{RawURL: "https://images.example.invalid/shot.png"},
	}}
	if failures := assetReviewFailures(subject); len(failures) != 0 {
		t.Fatalf("a duplicate unknown receipt must not downgrade known facts: %+v", failures)
	}
}

func TestImportedReferenceGuidancePreservesInspectedCounts(t *testing.T) {
	subject := lstValidationSubject()
	subject.AssetFacts.Screenshots.Count = 5
	subject.AssetFacts.Screenshots.InspectedFormatCount = 5
	subject.AssetFacts.Screenshots.LosslessCount = 2
	subject.AssetFacts.HostedScreenshots.Count = 5
	subject.ImportedScreenshotEvidence = &api.ImportedScreenshotEvidence{}
	for i := range 3 {
		subject.ImportedScreenshotEvidence.References = append(subject.ImportedScreenshotEvidence.References,
			api.ImportedScreenshotReference{RawURL: fmt.Sprintf("https://images.example.invalid/unknown-%d.png", i)})
	}
	failures := assetReviewFailures(subject)
	if len(failures) != 1 || failures[0].Disposition != api.RuleDispositionAdvisory ||
		!strings.Contains(failures[0].Reason, "2 of 8") || !strings.Contains(failures[0].Reason, "3 confirmed JPEG, 3 format unknown") {
		t.Fatalf("mixed inspected/uninspected counts changed: %+v", failures)
	}
	subject.AssetFacts.Screenshots.LosslessCount = 3
	if failures := assetReviewFailures(subject); len(failures) != 0 {
		t.Fatalf("three actual lossless images remain sufficient: %+v", failures)
	}
}
