// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDuplicateGuidanceRetiresQuestionsAndAnswerAuthority(t *testing.T) {
	candidate := api.DupeMatchProjection{
		ID:                  "123",
		EvidenceFingerprint: "candidate-v1",
		Relation:            api.DupeRelationManualReview,
		Reason:              "lst_internal_comparison_required",
	}
	meta := api.UploadSubject{
		Tag:                              "GRP",
		PersonalRelease:                  true,
		Identity:                         api.ExternalIdentity{Generation: 1},
		TrackerQuestionnaireAnswers:      map[string]map[string]string{"LST": {}},
		TrackerDuplicateReviewCandidates: map[string][]api.DupeMatchProjection{"LST": {candidate}},
	}
	set := func(key, value string) {
		subject := api.NewTrackerValidationSubject(meta, "LST")
		meta.TrackerQuestionnaireAnswers["LST"][duplicateEvidenceKey(subject, key)] = value
	}
	for key, value := range map[string]string{
		"source":          "https://example.test/source",
		"internal_status": "internal",
		"back_catalogue":  "yes",
		"layer_source":    "native_disc",
		"disc_identity":   "disc-a",
		"disc_form":       "unmodified",
	} {
		set(key, value)
	}
	set("source_reviewed", "yes")
	prefix := duplicateCandidatePrefix(candidate)
	for key, value := range map[string]string{
		"comparison_source":         "https://example.test/comparison",
		"candidate_internal_status": "non_internal",
		"candidate_group":           "GRP",
		"quality":                   "superior",
		"visual_improvement":        "yes",
		"meaningful_benefit":        "yes",
		"defect_improved":           "yes",
		"replacement":               "newer",
		"candidate_disc_identity":   "disc-b",
		"candidate_disc_form":       "custom",
		"candidate_layer_source":    "web_hybrid",
	} {
		set(prefix+key, value)
	}
	set(prefix+"comparison_reviewed", "yes")
	policy := duplicatePolicy()
	if policy.Questionnaire != nil && len(policy.Questionnaire(trackers.PreparationInput{Meta: meta})) != 0 {
		t.Fatal("duplicate reassurance questions remain")
	}
	want := api.DupeReleaseEvidence{PersonalRelease: true, Group: "GRP"}
	for _, change := range []func(){
		func() {},
		func() { meta.Identity.Generation++ },
		func() { meta.TrackerDuplicateReviewCandidates["LST"][0].EvidenceFingerprint = "candidate-v2" },
		func() {
			for key := range meta.TrackerQuestionnaireAnswers["LST"] {
				meta.TrackerQuestionnaireAnswers["LST"][key] = "no"
			}
		},
		func() { meta.TrackerQuestionnaireAnswers = nil },
	} {
		change()
		evidence, comparisons := policy.TargetEvidence(meta)
		if !reflect.DeepEqual(evidence, want) || len(comparisons) != 0 {
			t.Fatalf("retired answers granted duplicate authority: evidence=%+v comparisons=%+v", evidence, comparisons)
		}
	}
}

// Legacy bindings intentionally match saved pre-cleanup answers.
func duplicateEvidenceKey(subject api.TrackerValidationSubject, key string) string {
	dependencies := make([]string, 0, 17)
	if key == "source_reviewed" {
		dependencies = append(dependencies, "source", "internal_status", "back_catalogue", "layer_source", "disc_identity", "disc_form")
	} else if strings.HasPrefix(key, "candidate_") && strings.HasSuffix(key, "_comparison_reviewed") {
		prefix := strings.TrimSuffix(key, "comparison_reviewed")
		dependencies = append(dependencies, "source", "internal_status", "back_catalogue", "layer_source", "disc_identity", "disc_form")
		for _, suffix := range []string{"comparison_source", "candidate_internal_status", "candidate_group", "candidate_layer_source", "candidate_disc_identity", "candidate_disc_form", "quality", "visual_improvement", "meaningful_benefit", "defect_improved", "replacement"} {
			dependencies = append(dependencies, prefix+suffix)
		}
	}
	if len(dependencies) > 0 {
		values := make([]string, 0, len(dependencies))
		for _, dependency := range dependencies {
			values = append(values, subject.QuestionnaireAnswers[duplicateEvidenceKey(subject, dependency)])
		}
		hash, _ := api.CanonicalWorkflowFingerprint(values)
		key += "_" + string(hash)
	}

	return trackers.LanguageQuestionKey(subject, "duplicate_"+key+"_"+trackers.NormalizeTrackerReleaseGroup(subject.Tag))
}

// duplicateCandidatePrefix reproduces the retired candidate binding for regression fixtures.
func duplicateCandidatePrefix(candidate api.DupeMatchProjection) string {
	binding, _ := api.CanonicalWorkflowFingerprint([]string{candidate.ID, string(candidate.EvidenceFingerprint)})
	return "candidate_" + string(binding) + "_"
}
