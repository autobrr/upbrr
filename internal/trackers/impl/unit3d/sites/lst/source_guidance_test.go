// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"reflect"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRetiredSourceAnswersCannotSupplyEvidence(t *testing.T) {
	subject := reviewedLSTEncode(t)
	subject.VideoEncoder = "H.264"
	baseline := sourceValidationFailures(subject)
	for _, answer := range []string{"x264", "unsupported", "other", "entirely_uncropped", "retail_untouched"} {
		for _, key := range []string{"encode_encoder", "encode_rate_control", "encode_crop", "encode_source"} {
			subject.QuestionnaireAnswers[sourceQuestionKey(subject, key)] = answer
		}
		if got := sourceValidationFailures(subject); !reflect.DeepEqual(got, baseline) {
			t.Fatalf("retired answer %q changed facts: %+v", answer, got)
		}
	}
}

func TestSourceEvidenceWarningAndGuidance(t *testing.T) {
	subject := reviewedLSTEncode(t)
	subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusMissing
	failures := sourceValidationFailures(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "lst_encode_settings" && f.Disposition == api.RuleDispositionWaivable && f.EvidenceStatus == api.MetadataEvidenceStatusComplete && !f.QuestionnaireInput
	}) {
		t.Fatalf("missing settings not waivable: %+v", failures)
	}
	subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusUnknown
	for _, f := range sourceValidationFailures(subject) {
		if f.Disposition != api.RuleDispositionAdvisory {
			t.Fatalf("unknown evidence blocked: %+v", f)
		}
	}
}

func TestDiscRetiredAnswersRemainGuidance(t *testing.T) {
	subject := lstValidationSubject()
	subject.Type, subject.DiscType = "DISC", "BDMV"
	baseline := discReviewFailures(subject)
	for _, f := range baseline {
		if f.Disposition != api.RuleDispositionAdvisory || f.QuestionnaireInput {
			t.Fatalf("unknown disc provenance blocks: %+v", f)
		}
	}
	for _, key := range []string{"source", "contents", "protection", "form", "set", "custom_availability"} {
		subject.QuestionnaireAnswers[discQuestionKey(subject, key)] = "unmodified_same_disc_exists"
	}
	if got := discReviewFailures(subject); !reflect.DeepEqual(got, baseline) {
		t.Fatalf("retired disc answers changed guidance: %+v", got)
	}
}

func TestLegacyAvailabilityIsNotAnAttestation(t *testing.T) {
	subject := exceptionTestSubject(t, "avi", "DVD", "XviD")
	fields := exceptionQuestionnaireFields(subject)
	if len(fields) != 1 || fields[0].Kind != "text" || !fields[0].Required {
		t.Fatalf("only required explanation should remain: %+v", fields)
	}
	baseline := exceptionValidationFailures(subject)
	subject.QuestionnaireAnswers[legacySoleSourceQuestionKey(subject)] = "other_version_exists"
	if got := exceptionValidationFailures(subject); !reflect.DeepEqual(got, baseline) {
		t.Fatalf("retired availability answer changed authority: %+v", got)
	}
}

func TestSourceWarningsBindActualGenerationEvidence(t *testing.T) {
	subject := reviewedLSTEncode(t)
	subject.Assessments.MediaInfoEncodeSettings = api.EncodeSettingsStatusMissing
	first, err := trackers.WaivableRuleFailureFingerprint("LST", sourceValidationFailures(subject))
	if err != nil {
		t.Fatal(err)
	}
	subject.Identity.Generation++
	second, err := trackers.WaivableRuleFailureFingerprint("LST", sourceValidationFailures(subject))
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first == second {
		t.Fatal("source warning acknowledgement survived a new generation")
	}
	subject.MediaFileFacts.TechnicalStatus = api.MetadataEvidenceStatusContradictory
	for _, f := range sourceValidationFailures(subject) {
		if f.Disposition != api.RuleDispositionAdvisory {
			t.Fatalf("contradictory technical evidence became a definite violation: %+v", f)
		}
	}
}

func TestSecondaryBroadcastSourceCannotHideBehindPrimaryException(t *testing.T) {
	subject := exceptionTestSubject(t, "ts", "HDTV", "")
	subject.MediaFileFacts = api.MediaFileFacts{ExpectedFileCount: 2, Files: []api.MediaFileFact{
		{
			ResourceID: "primary",
			Primary:    true,
			Container:  "ts",
			Source:     "HDTV",
		},
		{ResourceID: "secondary", Container: "ts"},
	}}
	subject.QuestionnaireAnswers[exceptionQuestionKey(subject)] = "Container exception explanation for both resources."
	failures := exceptionValidationFailures(subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "guidance_lst_broadcast_source" && f.Disposition == api.RuleDispositionAdvisory
	}) {
		t.Fatalf("secondary source guidance omitted: %+v", failures)
	}
}

func TestPrimaryInspectionPreservesCanonicalSource(t *testing.T) {
	for _, source := range []string{"CAM", "SCREENER"} {
		t.Run(source, func(t *testing.T) {
			subject := lstValidationSubject()
			subject.Source = source
			// The programme inspector supplies technical facts, not source provenance.
			subject.MediaFileFacts = api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{
				{
					ResourceID: "primary",
					Primary:    true,
					Container:  "mkv",
				},
			}}
			if !slices.ContainsFunc(sourceValidationFailures(subject), func(f api.RuleFailure) bool {
				return f.Rule == "lst_prohibited_source" && f.Disposition == api.RuleDispositionStrict
			}) {
				t.Fatal("primary inspection erased the established prohibited source")
			}
		})
	}

	t.Run("broadcast", func(t *testing.T) {
		subject := exceptionTestSubject(t, "ts", "HDTV", "")
		subject.MediaFileFacts = api.MediaFileFacts{ExpectedFileCount: 1, Files: []api.MediaFileFact{
			{
				ResourceID: "primary",
				Primary:    true,
				Container:  "ts",
			},
		}}
		if got := containerException(subject); got != "broadcast" {
			t.Fatalf("canonical broadcast source lost: %q", got)
		}
		subject.QuestionnaireAnswers[exceptionQuestionKey(subject)] = "Broadcast container explanation."
		if failures := exceptionValidationFailures(subject); len(failures) != 0 {
			t.Fatalf("known primary broadcast became unresolved: %+v", failures)
		}
		secondary := api.MediaFileFact{ResourceID: "secondary", Container: "ts"}
		if got := singleContainerException(containerFileSubject(subject, secondary)); got != "unresolved_broadcast" {
			t.Fatalf("secondary inherited primary provenance: %q", got)
		}
	})
}
