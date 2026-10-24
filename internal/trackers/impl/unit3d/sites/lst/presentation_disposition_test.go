// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPresentationDefaultWarningDoesNotClaimTrumpAuthority(t *testing.T) {
	for _, personal := range []bool{false, true} {
		subject := lstValidationSubject()
		subject.PersonalRelease = personal
		subject.LanguageFacts.Tracks[0].Default = false
		before := subject.LanguageFacts.Clone()
		failures := languageAssessment(subject)
		for _, rule := range []string{"language_default_audio", "language_original_default"} {
			index := slices.IndexFunc(failures, func(f api.RuleFailure) bool { return f.Rule == rule })
			if index < 0 {
				t.Fatalf("missing %s", rule)
			}
			f := failures[index]
			if f.Disposition != api.RuleDispositionWaivable || f.EvidenceStatus != api.MetadataEvidenceStatusComplete || f.EvidenceFingerprint == "" || strings.Contains(f.Reason, "Trumpable") || strings.Contains(f.Reason, "supersede") {
				t.Fatalf("wrong presentation warning: %+v", f)
			}
			if !trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, false) || trackers.RuleFailureBlocksExecution(f, api.WorkflowExecutionModeNormal, true) {
				t.Fatalf("warning authorization contract lost: %+v", f)
			}
		}
		if !reflect.DeepEqual(before, subject.LanguageFacts) {
			t.Fatal("presentation warning mutated canonical correction facts")
		}
	}
}

func TestUnknownPresentationEvidenceIsGuidanceForEveryRelease(t *testing.T) {
	for _, personal := range []bool{false, true} {
		for _, status := range []api.MetadataEvidenceStatus{api.MetadataEvidenceStatusPartial, api.MetadataEvidenceStatusContradictory} {
			subject := lstValidationSubject()
			subject.PersonalRelease = personal
			subject.LanguageFacts.ProgrammeStatus = status
			subject.LanguageFacts.Tracks[0].DefaultKnown = false
			subject.LanguageFacts.Tracks[0].StreamOrderKnown = false
			for _, f := range languageAssessment(subject) {
				if slices.Contains([]string{"language_default_audio", "language_original_default", "language_original_order", "language_evidence", "language_original_evidence"}, f.Rule) && f.Disposition != api.RuleDispositionAdvisory {
					t.Fatalf("uncertain presentation blocked personal=%t: %+v", personal, f)
				}
			}
		}
	}
}

func TestPresentationWarningAcknowledgementExpiresWithEvidence(t *testing.T) {
	s := lstValidationSubject()
	s.LanguageFacts.Tracks[0].Default = false
	before, err := trackers.WaivableRuleFailureFingerprint("LST", languageAssessment(s))
	if err != nil || before == "" {
		t.Fatalf("missing warning fingerprint: %v", err)
	}
	s.Identity.Generation++
	after, err := trackers.WaivableRuleFailureFingerprint("LST", languageAssessment(s))
	if err != nil || after == before {
		t.Fatalf("new generation reused presentation authority: %v", err)
	}
	s.LanguageFacts.Tracks[0].Default = true
	for _, f := range languageAssessment(s) {
		if f.Rule == "language_default_audio" || f.Rule == "language_original_default" {
			t.Fatalf("corrected default still blocked: %+v", f)
		}
	}
}
