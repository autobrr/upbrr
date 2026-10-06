// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNBLEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
	t.Parallel()

	subject := nblPassingSubject()
	failures := evaluateNBLEvidence(t, subject)
	if len(failures) != 0 {
		t.Fatalf("passing subject failures: %+v", failures)
	}

	subject.PackageFacts.ArchiveFileCount = 1
	assertNBLFailure(
		t,
		evaluateNBLEvidence(t, subject),
		"nbl_archive",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)

	subject = nblPassingSubject()
	subject.PackageFacts.Status = api.MetadataEvidenceStatusPartial
	assertNBLFailure(
		t,
		evaluateNBLEvidence(t, subject),
		"nbl_archive",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestNBLRejectsEpisodeFolder(t *testing.T) {
	t.Parallel()

	subject := nblPassingSubject()
	subject.PackageFacts.SingleFileFolder = true
	assertNBLFailure(
		t,
		evaluateNBLEvidence(t, subject),
		"nbl_episode_layout",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
}

func TestNBLIgnoresScreenshotEvidence(t *testing.T) {
	t.Parallel()

	for _, evidence := range []api.AssetEvidence{
		{},
		{Status: api.MetadataEvidenceStatusPartial},
		{Status: api.MetadataEvidenceStatusComplete},
		{
			Status: api.MetadataEvidenceStatusComplete,
			Ready:  true,
			Count:  4,
		},
	} {
		subject := nblPassingSubject()
		subject.AssetFacts.Screenshots = evidence
		subject.AssetFacts.HostedScreenshots = evidence
		if failures := evaluateNBLEvidence(t, subject); len(failures) != 0 {
			t.Errorf("screenshot evidence %+v affected NBL validation: %+v", evidence, failures)
		}
	}
}

func TestNBLDoesNotApplyMediaOnlyPackageRuleToDiscs(t *testing.T) {
	t.Parallel()

	subject := nblPassingSubject()
	subject.DiscType = "DVD"
	subject.PackageFacts.KnownFileCount = 3
	subject.PackageFacts.MediaFileCount = 1
	for _, failure := range evaluateNBLEvidence(t, subject) {
		if failure.Rule == "nbl_media_only" {
			t.Fatalf("disc package received media-only failure: %+v", failure)
		}
	}
}

func TestNBLValidationPolicyVersion(t *testing.T) {
	t.Parallel()

	if got := Profile().ValidationPolicy.ID; got != "standalone-nbl-policy-v4/languages-v1" {
		t.Fatalf("validation policy ID = %q", got)
	}
}

func nblPassingSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		PackageFacts: api.PackageFacts{
			Status:          api.MetadataEvidenceStatusComplete,
			KnownFileCount:  1,
			MediaFileCount:  1,
			DetectedSeasons: []int{1},
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 1,
			OriginalLanguage:  "fr",
			Files: []api.MediaFileFact{{
				FileName:          "Example.Release.2026.S01E01.1080p-GRP.mkv",
				AudioLanguages:    []string{"French"},
				SubtitleLanguages: []string{"English"},
			}},
		},
		AssetFacts: api.AssetFacts{
			MediaInfoText: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  1,
			},
		},
	}
}

func evaluateNBLEvidence(t *testing.T, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	failures, err := checkEvidenceRules(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatalf("evaluate NBL evidence: %v", err)
	}
	return failures
}

func assertNBLFailure(
	t *testing.T,
	failures []api.RuleFailure,
	rule string,
	disposition api.RuleDisposition,
	status api.MetadataEvidenceStatus,
) {
	t.Helper()
	for _, failure := range failures {
		if failure.Rule == rule {
			if failure.Disposition != disposition || failure.EvidenceStatus != status {
				t.Fatalf("failure = %+v", failure)
			}
			return
		}
	}
	t.Fatalf("missing rule %q: %+v", rule, failures)
}

func TestNBLFinalizedLanguageAccessibility(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                            string
		audio, subtitles                []string
		programmeStatus, subtitleStatus api.MetadataEvidenceStatus
		discType, uploadType            string
		wantStatus                      api.MetadataEvidenceStatus
	}{
		{
name: "foreign audio with English captions",
 audio: []string{"Japanese"},
 subtitles: []string{"English"},
},
		{name: "English audio without original metadata", audio: []string{"English"}},
		{name: "English captions without programme metadata", subtitles: []string{"English"}},
		{
name: "complete missing accessibility",
 audio: []string{"Japanese"},
 programmeStatus: api.MetadataEvidenceStatusComplete,
 subtitleStatus: api.MetadataEvidenceStatusComplete,
 wantStatus: api.MetadataEvidenceStatusComplete,
},
		{
name: "unknown programme evidence",
 subtitleStatus: api.MetadataEvidenceStatusComplete,
 wantStatus: api.MetadataEvidenceStatusPartial,
},
		{
name: "unknown subtitle evidence",
 audio: []string{"Japanese"},
 programmeStatus: api.MetadataEvidenceStatusComplete,
 wantStatus: api.MetadataEvidenceStatusPartial,
},
		{
name: "full disc excluded",
 discType: "BDMV",
 uploadType: "DISC",
},
		{name: "canonical full disc excluded", uploadType: "DISC"},
		{
name: "disc sourced remux assessed",
 discType: "BDMV",
 uploadType: "REMUX",
 audio: []string{"Japanese"},
 programmeStatus: api.MetadataEvidenceStatusComplete,
 subtitleStatus: api.MetadataEvidenceStatusComplete,
 wantStatus: api.MetadataEvidenceStatusComplete,
},
	} {
		t.Run(tt.name, func(t *testing.T) {
			subject := nblPassingSubject()
			subject.Tracker = "NBL"
			subject.DiscType, subject.Type = tt.discType, tt.uploadType
			subject.LanguageFacts = api.LanguageFacts{
ProgrammeLanguages: tt.audio,
 SubtitleLanguages: tt.subtitles,
 ProgrammeStatus: tt.programmeStatus,
 SubtitleStatus: tt.subtitleStatus,
}
			failures, err := validationPolicy().Check(context.Background(), subject, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantStatus == "" {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %+v", failures)
				}
			} else {
				assertNBLFailure(t, failures, "language_subtitles", api.RuleDispositionStrict, tt.wantStatus)
			}
		})
	}
}
