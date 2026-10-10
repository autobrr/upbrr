// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"context"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestAitherEvidencePolicyPassViolationAndMissingEvidence(t *testing.T) {
	t.Parallel()

	subject := aitherPassingSubject()
	failures := evaluateAitherEvidence(t, subject)
	if len(failures) != 0 {
		t.Fatalf("passing subject failures: %+v", failures)
	}

	subject.PackageFacts.ArchiveFileCount = 1
	assertAitherFailure(
		t,
		evaluateAitherEvidence(t, subject),
		"aither_archive",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)

	subject = aitherPassingSubject()
	subject.MediaFileFacts.Files[0].SubtitleLanguages = []string{"Japanese"}
	subject.LanguageFacts = aitherTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"Japanese"})
	assertAitherFailure(
		t,
		evaluateAitherEvidence(t, subject),
		"language_subtitles",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)

	subject = aitherPassingSubject()
	subject.PackageFacts.Status = api.MetadataEvidenceStatusPartial
	assertAitherFailure(
		t,
		evaluateAitherEvidence(t, subject),
		"aither_archive",
		api.RuleDispositionAdvisory,
		api.MetadataEvidenceStatusPartial,
	)
}

func TestAitherRequiresThreeScreenshots(t *testing.T) {
	t.Parallel()

	subject := aitherPassingSubject()
	subject.AssetFacts.Screenshots.Count = 2
	assertAitherFailure(
		t,
		evaluateAitherEvidence(t, subject),
		"aither_asset_screenshot",
		api.RuleDispositionStrict,
		api.MetadataEvidenceStatusComplete,
	)
}

func TestAitherValidationPolicyVersion(t *testing.T) {
	t.Parallel()

	if got := Profile().ValidationPolicy.ID; got != "unit3d-aither-policy-v8/languages-v1" {
		t.Fatalf("validation policy ID = %q", got)
	}
}

func aitherPassingSubject() api.TrackerValidationSubject {
	return api.TrackerValidationSubject{
		Tracker:       "AITHER",
		LanguageFacts: aitherTestLanguageFacts("Japanese", []string{"Japanese"}, []string{"English"}),
		Identity:      api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		PackageFacts: api.PackageFacts{
			Status:         api.MetadataEvidenceStatusComplete,
			KnownFileCount: 1,
			MediaFileCount: 1,
		},
		MediaFileFacts: api.MediaFileFacts{
			Status:            api.MetadataEvidenceStatusComplete,
			LanguageStatus:    api.MetadataEvidenceStatusComplete,
			ExpectedFileCount: 1,
			OriginalLanguage:  "ja",
			Files: []api.MediaFileFact{{
				FileName:          "Example.Release.2026.1080p-GRP.mkv",
				AudioLanguages:    []string{"Japanese"},
				SubtitleLanguages: []string{"English"},
			}},
		},
		AssetFacts: api.AssetFacts{
			MediaInfoText: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  1,
			},
			Screenshots: api.AssetEvidence{
				Status: api.MetadataEvidenceStatusComplete,
				Ready:  true,
				Count:  3,
			},
		},
	}
}

func evaluateAitherEvidence(t *testing.T, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	failures, err := ValidationPolicy().Check(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatalf("evaluate AITHER evidence: %v", err)
	}
	return failures
}

func assertAitherFailure(
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

// aitherTestLanguageFacts models inspected, identified programme streams and full
// embedded subtitles so unrelated fixtures satisfy the current facts contract.
func aitherTestLanguageFacts(original string, programme, subtitles []string) api.LanguageFacts {
	media := api.MediaFacts{
		OriginalLanguage:      original,
		SubtitleLanguages:     subtitles,
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "audio-0",
	}
	for index, language := range programme {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:        "audio-" + strconv.Itoa(index),
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Languages: []string{language},
			Codec:     "AC-3",
			Default:   index == 0,
		})
	}
	for index, language := range subtitles {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			ID:        "subtitle-" + strconv.Itoa(index),
			Kind:      api.MediaTrackSubtitle,
			Languages: []string{language},
			Default:   index == 0,
		})
	}
	return mediafacts.ResolveLanguages(media)
}

func TestAitherProgrammeExceptionRequiresStaffRatherThanAcknowledgement(t *testing.T) {
	t.Parallel()
	subject := aitherPassingSubject()
	subject.LanguageFacts = aitherTestLanguageFacts("Japanese", []string{"Japanese", "English", "German"}, []string{"English"})
	failures := evaluateAitherEvidence(t, subject)
	assertAitherFailure(t, failures, "language_extra_dub", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	for _, failure := range failures {
		if failure.Rule == "language_extra_dub" && (!strings.Contains(failure.Reason, "Staff approval required") || !trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, true)) {
			t.Fatalf("ordinary acknowledgement waived staff exception: %+v", failure)
		}
	}
	subject.Type = "DISC"
	if failures := evaluateAitherEvidence(t, subject); len(failures) != 0 {
		t.Fatalf("canonical full disc acquired language rules: %+v", failures)
	}
}
