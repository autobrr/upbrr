// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLICollectsNativeQuestionnaireCorrections(t *testing.T) {
	registry := impl.MustNewRegistry()
	source := t.TempDir()
	mediaInfo := filepath.Join(source, "mediainfo.txt")
	if err := os.WriteFile(mediaInfo, []byte("prepared media"), 0o600); err != nil {
		t.Fatal(err)
	}
	generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "WEBDL",
		Title:      "Example Film",
		Year:       2026,
		Resolution: "1080p",
		Source:     "WEB",
		VideoCodec: "H.264",
		Audio:      "DD 2.0",
		Tag:        "-GRP",
	}, api.NopLogger{})
	facts := mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "Japanese",
		SubtitleLanguages:     []string{"English"},
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "audio-0",
		Tracks: []api.MediaTrackFacts{
			{
				ID:        "audio-0",
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleProgramme,
				Codec:     "AC-3",
				Languages: []string{"Japanese", "Korean"},
				Default:   true,
			},
			{
				ID:        "subtitle-0",
				Kind:      api.MediaTrackSubtitle,
				Languages: []string{"English"},
			},
		},
	})
	facts.OriginalLanguages = []string{"Japanese", "Korean"}
	base := api.UploadSubject{
		SourcePath:        source,
		ReleaseName:       generated.Name,
		GeneratedName:     generated.GeneratedName,
		Type:              "WEBDL",
		Source:            "WEB",
		VideoCodec:        "H.264",
		Audio:             "DD 2.0",
		HasEncodeSettings: true,
		Release: api.ReleaseInfo{
			Category:   "MOVIE",
			Title:      "Example Film",
			Year:       2026,
			Resolution: "1080p",
		},
		Identity: api.ExternalIdentity{
			SourcePath: source,
			Generation: 1,
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     123,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: source,
			Generation: 1,
			TMDB: &api.TMDBMetadata{
				TMDBID:   123,
				Category: "MOVIE",
				Title:    "Example Film",
				Year:     2026,
				Genres:   "Drama",
			},
			IMDB: &api.IMDBMetadata{Type: "short"},
		},
		MediaInfoTextPath: mediaInfo,
		LanguageFacts:     facts,
		Assessments:       api.ReleaseAssessments{MediaInfoUniqueID: api.UniqueIDStatusPresent, MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
	}
	balanceKey := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(base, "AITHER"), "multilingual_balance_audio-0")
	for _, test := range []struct{ tracker, key, previous, answer string }{
		{"AITHER", balanceKey, "unresolved", "evenly_split"},
		{"ANT", "requestid", "0", ""},
		{"ANT", "requestid", "text", "42"},
	} {
		t.Run(test.tracker+test.previous, func(t *testing.T) {
			subject := base
			answers := map[string]string{test.key: test.previous}
			if test.tracker == "ANT" {
				answers["type"] = "Short Film"
			}
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{test.tracker: answers}
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: test.tracker, Meta: subject}, "input", "catalog", "config")
			if failure != nil {
				t.Fatal(failure)
			}
			index := slices.IndexFunc(projection.Questionnaire, func(field api.TrackerQuestionnaireRequirement) bool { return field.Key == test.key })
			if index < 0 || !projection.Questionnaire[index].NeedsReview || len(projection.RequiredActions) != 1 {
				t.Fatalf("native rejected answer lost its corrective action: %+v", projection)
			}
			session := &cliWorkflowSession{
				streams:       cliIO{out: io.Discard},
				intent:        cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
				uploadRequest: api.Request{Trackers: []string{test.tracker}, TrackerQuestionnaireAnswers: subject.TrackerQuestionnaireAnswers},
				current:       releaseworkflow.CommandResult{Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{projection}}},
			}
			feedback, declined, err := session.collectCompositeTrackerFeedback(bufio.NewReader(strings.NewReader(test.answer+"\n")),
				api.RequiredAction{Kind: api.RequiredActionAnswerQuestionnaire, TrackerID: projection.TrackerID}, api.ReleaseWorkflowUploadFeedback{})
			if err != nil || declined || feedback.Response.Questionnaire == nil {
				t.Fatalf("native correction was not collected: %+v, %t, %v", feedback, declined, err)
			}
			collected := feedback.Response.Questionnaire.Answers
			if collected[test.key] == nil || *collected[test.key] != test.answer || projection.QuestionnaireAnswers[test.key] != test.previous {
				t.Fatalf("reused rejected input or mutated its snapshot: %+v / %+v", collected, projection)
			}
			corrected := map[string]string{}
			for key, value := range collected {
				if value != nil {
					corrected[key] = *value
				}
			}
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{test.tracker: corrected}
			reviewed, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: test.tracker, Meta: subject}, "reviewed", "catalog", "config")
			if failure != nil || !reviewed.UploadReady || len(reviewed.RequiredActions) != 0 {
				t.Fatalf("native correction repeated its pending action: %+v, %v", reviewed, failure)
			}
		})
	}
}

func TestCLICollectsNativeCompatibilityCorrections(t *testing.T) {
	registry := impl.MustNewRegistry()
	base := nativeCompatibilityProjectionSubject(t)
	for _, tracker := range []string{"AITHER", "HHD", "LST", "ULCX"} {
		t.Run(tracker, func(t *testing.T) {
			subject := base
			validationSubject := api.NewTrackerValidationSubject(subject, tracker)
			firstKey := trackers.LanguageQuestionKey(validationSubject, "compatibility_mix_companion-0")
			secondKey := trackers.LanguageQuestionKey(validationSubject, "compatibility_mix_companion-1")
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{tracker: {firstKey: "main", secondKey: "main"}}
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: tracker, Meta: subject}, "input", "catalog", "config")
			if failure != nil || projection.Readiness != api.ReadinessStatusBlocked || len(projection.Questionnaire) != 2 || len(projection.RequiredActions) != 1 {
				t.Fatalf("conflicting current answers lost correction controls: %+v, %v", projection, failure)
			}
			for _, field := range projection.Questionnaire {
				if field.Value != "main" || !field.NeedsReview {
					t.Fatalf("conflicting populated answer cannot be collected: %+v", field)
				}
			}
			session := &cliWorkflowSession{
				streams:       cliIO{out: io.Discard},
				intent:        cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
				uploadRequest: api.Request{Trackers: []string{tracker}, TrackerQuestionnaireAnswers: subject.TrackerQuestionnaireAnswers},
				current:       releaseworkflow.CommandResult{Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{projection}}},
			}
			feedback, declined, err := session.collectCompositeTrackerFeedback(bufio.NewReader(strings.NewReader("main\nalternate\n")),
				api.RequiredAction{Kind: api.RequiredActionAnswerQuestionnaire, TrackerID: projection.TrackerID}, api.ReleaseWorkflowUploadFeedback{})
			if err != nil || declined || feedback.Response.Questionnaire == nil {
				t.Fatalf("association correction was not collected: %+v, %t, %v", feedback, declined, err)
			}
			answers := map[string]string{}
			for key, value := range feedback.Response.Questionnaire.Answers {
				if value != nil {
					answers[key] = *value
				}
			}
			if answers[firstKey] != "main" || answers[secondKey] != "alternate" || projection.QuestionnaireAnswers[secondKey] != "main" {
				t.Fatalf("reused conflict or mutated projection snapshot: %+v / %+v", answers, projection)
			}
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{tracker: answers}
			reviewed, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: tracker, Meta: subject}, "reviewed", "catalog", "config")
			if failure != nil || !reviewed.UploadReady || len(reviewed.RequiredActions) != 0 {
				t.Fatalf("corrected native associations did not recover: %+v, %v", reviewed, failure)
			}
			for _, field := range reviewed.Questionnaire {
				if field.NeedsReview {
					t.Fatalf("accepted association still needs correction: %+v", field)
				}
			}
			for _, boundary := range []string{"debug", "missing companion", "missing companion unanswered", "missing companion unresolved", "unknown companion identity"} {
				t.Run(boundary, func(t *testing.T) {
					input := trackers.PreparationInput{Tracker: tracker, Meta: base}
					input.Meta.LanguageFacts.Tracks = slices.Clone(base.LanguageFacts.Tracks)
					switch boundary {
					case "debug":
						input.ExecutionMode = api.WorkflowExecutionModeDebug
					case "missing companion", "missing companion unanswered", "missing companion unresolved":
						input.Meta.LanguageFacts.Tracks = input.Meta.LanguageFacts.Tracks[:3]
					case "unknown companion identity":
						input.Meta.LanguageFacts.Tracks[3].ID = ""
					}
					currentSubject := api.NewTrackerValidationSubject(input.Meta, tracker)
					input.Meta.TrackerQuestionnaireAnswers = map[string]map[string]string{tracker: {
						trackers.LanguageQuestionKey(currentSubject, "compatibility_mix_companion-0"): "main",
						trackers.LanguageQuestionKey(currentSubject, "compatibility_mix_companion-1"): "main",
					}}
					switch boundary {
					case "missing companion unanswered":
						input.Meta.TrackerQuestionnaireAnswers[tracker] = map[string]string{}
					case "missing companion unresolved":
						input.Meta.TrackerQuestionnaireAnswers[tracker][trackers.LanguageQuestionKey(currentSubject, "compatibility_mix_companion-0")] = "unresolved"
					}

					result, failure := registry.ProjectRelease(t.Context(), input, "boundary", "catalog", "config")
					if failure != nil {
						t.Fatal(failure)
					}
					if boundary == "debug" {
						if !result.UploadReady || len(result.RequiredActions) != 0 || slices.ContainsFunc(result.Questionnaire, func(f api.TrackerQuestionnaireRequirement) bool { return f.NeedsReview }) {
							t.Fatalf("conflict correction overrode debug bypass: %+v", result)
						}
					} else if result.Readiness != api.ReadinessStatusIneligible || len(result.Questionnaire) != 0 || len(result.RequiredActions) != 0 {
						t.Fatalf("physical/evidence failure became answer-correctable: %+v", result)
					}
				})
			}
		})
	}
}

func TestCLIHHDConditionalAndInferredCompatibilityCorrections(t *testing.T) {
	registry := impl.MustNewRegistry()
	for _, scenario := range []string{"conditional parent", "inferred companion", "impossible conditional parent"} {
		t.Run(scenario, func(t *testing.T) {
			subject := nativeCompatibilityProjectionSubject(t)
			subject.Type, subject.Source = "WEBDL", "WEB"
			generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       subject.Type,
				Title:      "Example Film",
				Year:       2026,
				Resolution: "1080p",
				Source:     subject.Source,
				VideoCodec: "H.264",
				Audio:      subject.Audio,
				Tag:        "-GRP",
			}, api.NopLogger{})
			subject.ReleaseName, subject.GeneratedName = generated.Name, generated.GeneratedName
			tracks := []api.MediaTrackFacts{subject.LanguageFacts.Tracks[0]}
			if scenario == "impossible conditional parent" {
				tracks = append(tracks, subject.LanguageFacts.Tracks[1])
			}
			first := subject.LanguageFacts.Tracks[2]
			first.ID, first.Role = "dd-a", api.AudioRoleProgramme
			tracks = append(tracks, first)
			if scenario != "inferred companion" {
				second := first
				second.ID = "dd-b"
				tracks = append(tracks, second)
			}
			companion := subject.LanguageFacts.Tracks[3]
			companion.ID = "explicit"
			if scenario != "inferred companion" {
				companion.Codec = "AAC"
			}
			tracks = append(tracks, companion)
			subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      "English",
				TrackCoverageComplete: true,
				PrimaryAudioTrackID:   "main",
				Tracks:                tracks,
			})
			current := api.NewTrackerValidationSubject(subject, "HHD")
			answers := map[string]string{}
			set := func(track, value string) {
				answers[trackers.LanguageQuestionKey(current, "compatibility_mix_"+track)] = value
			}
			input := "not_compatibility\nmain\ndd-a\n"
			if scenario == "inferred companion" {
				set("explicit", "main")
				input = "not_compatibility\nmain\n"
			} else {
				set("dd-a", "main")
				set("dd-b", "main")
				set("explicit", "dd-a")
				if scenario == "impossible conditional parent" {
					set("dd-a", "not_compatibility")
				}
			}
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"HHD": answers}
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "HHD", Meta: subject}, "input", "catalog", "config")
			if failure != nil {
				t.Fatal(failure)
			}
			debugProjection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
				Tracker:       "HHD",
				Meta:          subject,
				ExecutionMode: api.WorkflowExecutionModeDebug,
			}, "debug", "catalog", "config")
			if failure != nil || !debugProjection.UploadReady || len(debugProjection.RequiredActions) != 0 ||
				slices.ContainsFunc(debugProjection.Questionnaire, func(field api.TrackerQuestionnaireRequirement) bool { return field.NeedsReview }) {
				t.Fatalf("HHD assignment review overrode debug bypass: %+v, %v", debugProjection, failure)
			}
			if scenario == "impossible conditional parent" {
				if projection.Readiness != api.ReadinessStatusIneligible || len(projection.Questionnaire) != 0 || len(projection.RequiredActions) != 0 {
					t.Fatalf("impossible parent/companion roles exposed controls: %+v", projection)
				}
				return
			}
			if projection.Readiness != api.ReadinessStatusBlocked || len(projection.Questionnaire) != len(tracks)-1 {
				t.Fatalf("repairable source association lost its controls: %+v", projection)
			}
			for _, field := range projection.Questionnaire {
				if !field.NeedsReview || field.Value == "" {
					t.Fatalf("current or inferred association lost editable value: %+v", field)
				}
			}
			session := &cliWorkflowSession{
				streams:       cliIO{out: io.Discard},
				intent:        cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
				uploadRequest: api.Request{Trackers: []string{"HHD"}, TrackerQuestionnaireAnswers: subject.TrackerQuestionnaireAnswers},
				current:       releaseworkflow.CommandResult{Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{projection}}},
			}
			feedback, declined, err := session.collectCompositeTrackerFeedback(bufio.NewReader(strings.NewReader(input)),
				api.RequiredAction{Kind: api.RequiredActionAnswerQuestionnaire, TrackerID: projection.TrackerID}, api.ReleaseWorkflowUploadFeedback{})
			if err != nil || declined || feedback.Response.Questionnaire == nil {
				t.Fatalf("HHD correction not collected: %+v, %t, %v", feedback, declined, err)
			}
			corrected := map[string]string{}
			for key, value := range feedback.Response.Questionnaire.Answers {
				if value != nil {
					corrected[key] = *value
				}
			}
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"HHD": corrected}
			reviewed, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "HHD", Meta: subject}, "reviewed", "catalog", "config")
			if failure != nil || !reviewed.UploadReady || len(reviewed.RequiredActions) != 0 {
				t.Fatalf("HHD correction did not recover: %+v, %v", reviewed, failure)
			}
		})
	}
}

func TestCLIAitherAdditionalAudioAssociationCorrections(t *testing.T) {
	registry := impl.MustNewRegistry()
	for _, covered := range []bool{false, true} {
		for _, reversed := range []bool{false, true} {
			name := fmt.Sprintf("covered=%t/reversed=%t", covered, reversed)
			t.Run(name, func(t *testing.T) {
				subject := nativeCompatibilityProjectionSubject(t)
				japanese := subject.LanguageFacts.Tracks[0]
				japanese.Languages = []string{"Japanese"}
				english := subject.LanguageFacts.Tracks[1]
				english.ID, english.Role = "english", api.AudioRoleProgramme
				japaneseCore := subject.LanguageFacts.Tracks[2]
				japaneseCore.ID, japaneseCore.Languages = "japanese-core", []string{"Japanese"}
				ordinary := subject.LanguageFacts.Tracks[3]
				ordinary.ID, ordinary.Role = "english-dd", api.AudioRoleProgramme
				tracks := []api.MediaTrackFacts{japanese, english, japaneseCore, ordinary}
				if reversed {
					tracks = []api.MediaTrackFacts{japanese, ordinary, japaneseCore, english}
				}
				if covered {
					core := ordinary
					core.ID, core.Role = "english-core", api.AudioRoleCompatibility
					tracks = append(tracks, core)
				}
				tracks = append(tracks, api.MediaTrackFacts{
					ID:        "english-sub",
					Kind:      api.MediaTrackSubtitle,
					Languages: []string{"English"},
				})
				for i := range tracks {
					tracks[i].StreamOrder = i + 1
				}
				subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
					OriginalLanguage:      "Japanese",
					SubtitleLanguages:     []string{"English"},
					TrackCoverageComplete: true,
					PrimaryAudioTrackID:   "main",
					Tracks:                tracks,
				})
				validation := api.NewTrackerValidationSubject(subject, "AITHER")
				key := trackers.LanguageQuestionKey(validation, "compatibility_mix_english-dd")
				subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"AITHER": {key: "not_compatibility"}}
				projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "AITHER", Meta: subject}, "input", "catalog", "config")
				if failure != nil || projection.Readiness != api.ReadinessStatusBlocked || len(projection.RequiredActions) != 1 {
					t.Fatalf("answer-derived extra audio lost correction: %+v, %v", projection, failure)
				}
				index := slices.IndexFunc(projection.Questionnaire, func(field api.TrackerQuestionnaireRequirement) bool { return field.Key == key })
				if index < 0 || !projection.Questionnaire[index].NeedsReview || projection.Questionnaire[index].Value != "not_compatibility" {
					t.Fatalf("saved additional-audio choice lost its editor: %+v", projection.Questionnaire)
				}
				additional := slices.IndexFunc(projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
					return decision.Code == "language_additional_main_audio"
				})
				if additional < 0 || reversed && !strings.Contains(projection.PolicyDecisions[additional].Message, "additional main audio english is") {
					t.Fatalf("fixture must exercise extra-audio attribution in both source orders: %+v", projection.PolicyDecisions)
				}
				for _, decision := range projection.PolicyDecisions {
					if decision.Code == "language_additional_main_audio" && decision.Decision != "input_required" {
						t.Fatalf("correctable extra source audio remained independent: %+v", decision)
					}
					if covered && decision.Code == "language_compatibility_missing" {
						t.Fatalf("fixture must isolate extra-audio-only correction: %+v", decision)
					}
				}
				session := &cliWorkflowSession{
					streams:       cliIO{out: io.Discard},
					intent:        cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
					uploadRequest: api.Request{Trackers: []string{"AITHER"}, TrackerQuestionnaireAnswers: subject.TrackerQuestionnaireAnswers},
					current:       releaseworkflow.CommandResult{Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{projection}}},
				}
				responses := make([]string, 0, len(projection.Questionnaire))
				for _, field := range projection.Questionnaire {
					if field.Key == key {
						responses = append(responses, "english")
					} else if field.NeedsReview || field.Required && field.Value == "" {
						responses = append(responses, field.Value)
					}
				}
				feedback, declined, err := session.collectCompositeTrackerFeedback(bufio.NewReader(strings.NewReader(strings.Join(responses, "\n")+"\n")),
					api.RequiredAction{Kind: api.RequiredActionAnswerQuestionnaire, TrackerID: projection.TrackerID}, api.ReleaseWorkflowUploadFeedback{})
				if err != nil || declined || feedback.Response.Questionnaire == nil {
					t.Fatalf("AITHER association correction not collected: %+v, %t, %v", feedback, declined, err)
				}
				answers := map[string]string{}
				for answerKey, value := range feedback.Response.Questionnaire.Answers {
					if value != nil {
						answers[answerKey] = *value
					}
				}
				if answers[key] != "english" || projection.QuestionnaireAnswers[key] != "not_compatibility" {
					t.Fatalf("reused rejected answer or mutated original snapshot: %+v / %+v", answers, projection.QuestionnaireAnswers)
				}
				subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"AITHER": answers}
				reviewed, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "AITHER", Meta: subject}, "reviewed", "catalog", "config")
				if failure != nil || !reviewed.UploadReady || len(reviewed.RequiredActions) != 0 {
					t.Fatalf("corrected extra audio did not recover: %+v, %v", reviewed, failure)
				}
				subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"AITHER": {key: "not_compatibility"}}
				debugProjection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
					Tracker:       "AITHER",
					Meta:          subject,
					ExecutionMode: api.WorkflowExecutionModeDebug,
				}, "debug", "catalog", "config")
				if failure != nil || !debugProjection.UploadReady || len(debugProjection.RequiredActions) != 0 ||
					slices.ContainsFunc(debugProjection.Questionnaire, func(field api.TrackerQuestionnaireRequirement) bool { return field.NeedsReview }) {
					t.Fatalf("extra-audio correction overrode debug bypass: %+v, %v", debugProjection, failure)
				}
			})
		}
	}
}

func TestCLIHHDNonWebFormatAssociationCorrections(t *testing.T) {
	registry := impl.MustNewRegistry()
	for _, previous := range []string{"main", "unresolved", "missing", "unanswered"} {
		t.Run(previous, func(t *testing.T) {
			subject := nativeCompatibilityProjectionSubject(t)
			ordinary := subject.LanguageFacts.Tracks[3]
			ordinary.ID, ordinary.Role, ordinary.Codec = "ordinary-ddp", api.AudioRoleProgramme, "DD+"
			subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      "English",
				TrackCoverageComplete: true,
				PrimaryAudioTrackID:   "main",
				Tracks:                []api.MediaTrackFacts{subject.LanguageFacts.Tracks[0], subject.LanguageFacts.Tracks[2], ordinary},
			})
			validation := api.NewTrackerValidationSubject(subject, "HHD")
			key := trackers.LanguageQuestionKey(validation, "compatibility_mix_ordinary-ddp")
			coreKey := trackers.LanguageQuestionKey(validation, "compatibility_mix_companion-0")
			answers := map[string]string{coreKey: "main"}
			if previous != "unanswered" {
				answers[key] = previous
			}
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"HHD": answers}
			projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "HHD", Meta: subject}, "input", "catalog", "config")
			if failure != nil || projection.Readiness != api.ReadinessStatusBlocked || len(projection.RequiredActions) != 1 {
				t.Fatalf("answer-derived format rejection lost correction: %+v, %v", projection, failure)
			}
			index := slices.IndexFunc(projection.Questionnaire, func(field api.TrackerQuestionnaireRequirement) bool { return field.Key == key })
			if index < 0 || projection.Questionnaire[index].Value != "" && !projection.Questionnaire[index].NeedsReview {
				t.Fatalf("non-web ordinary association cannot be corrected: %+v", projection.Questionnaire)
			}
			format := false
			for _, decision := range projection.PolicyDecisions {
				if decision.Code == "language_compatibility_format" {
					format = true
					if decision.Decision != "input_required" {
						t.Fatalf("removable format failure remained independent: %+v", decision)
					}
				}
				if decision.Code == "language_compatibility_missing" ||
					(previous == "unresolved" || previous == "missing") && decision.Code == "language_compatibility_count" {
					t.Fatalf("fixture must retain AC-3 coverage and isolate format-only correction: %+v", projection.PolicyDecisions)
				}
			}
			if !format {
				t.Fatal("fixture did not exercise non-web format rejection")
			}
			session := &cliWorkflowSession{
				streams:       cliIO{out: io.Discard},
				intent:        cliWorkflowIntent{interaction: api.InteractionModeUnattendedConfirm},
				uploadRequest: api.Request{Trackers: []string{"HHD"}, TrackerQuestionnaireAnswers: subject.TrackerQuestionnaireAnswers},
				current:       releaseworkflow.CommandResult{Projections: &api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{projection}}},
			}
			var responses []string
			for _, field := range projection.Questionnaire {
				if field.Key == key {
					responses = append(responses, "not_compatibility")
				} else if field.NeedsReview || field.Required && field.Value == "" {
					responses = append(responses, field.Value)
				}
			}
			feedback, declined, err := session.collectCompositeTrackerFeedback(bufio.NewReader(strings.NewReader(strings.Join(responses, "\n")+"\n")),
				api.RequiredAction{Kind: api.RequiredActionAnswerQuestionnaire, TrackerID: projection.TrackerID}, api.ReleaseWorkflowUploadFeedback{})
			if err != nil || declined || feedback.Response.Questionnaire == nil {
				t.Fatalf("HHD format correction not collected: %+v, %t, %v", feedback, declined, err)
			}
			corrected := map[string]string{}
			for answerKey, value := range feedback.Response.Questionnaire.Answers {
				if value != nil {
					corrected[answerKey] = *value
				}
			}
			if corrected[key] != "not_compatibility" || projection.QuestionnaireAnswers[key] != answers[key] {
				t.Fatalf("reused rejected association or mutated old snapshot: %+v / %+v", corrected, projection.QuestionnaireAnswers)
			}
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"HHD": corrected}
			reviewed, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "HHD", Meta: subject}, "reviewed", "catalog", "config")
			if failure != nil || !reviewed.UploadReady || len(reviewed.RequiredActions) != 0 {
				t.Fatalf("ordinary non-compatibility choice did not recover: %+v, %v", reviewed, failure)
			}
			subject.TrackerQuestionnaireAnswers = map[string]map[string]string{"HHD": answers}
			debugProjection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{
				Tracker:       "HHD",
				Meta:          subject,
				ExecutionMode: api.WorkflowExecutionModeDebug,
			}, "debug", "catalog", "config")
			if failure != nil || !debugProjection.UploadReady || len(debugProjection.RequiredActions) != 0 ||
				slices.ContainsFunc(debugProjection.Questionnaire, func(field api.TrackerQuestionnaireRequirement) bool { return field.NeedsReview }) {
				t.Fatalf("format correction overrode debug bypass: %+v, %v", debugProjection, failure)
			}
		})
	}
}

func nativeCompatibilityProjectionSubject(t *testing.T) api.UploadSubject {
	t.Helper()
	source := t.TempDir()
	mediaInfo := filepath.Join(source, "mediainfo.txt")
	if err := os.WriteFile(mediaInfo, []byte("prepared media"), 0o600); err != nil {
		t.Fatal(err)
	}
	generated := metadata.BuildReleaseName(api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "REMUX",
		Title:      "Example Film",
		Year:       2026,
		Resolution: "1080p",
		Source:     "BluRay",
		VideoCodec: "H.264",
		Audio:      "TrueHD 5.1",
		Tag:        "-GRP",
	}, api.NopLogger{})
	base := api.UploadSubject{
		SourcePath:        source,
		ReleaseName:       generated.Name,
		GeneratedName:     generated.GeneratedName,
		Type:              "REMUX",
		Source:            "BluRay",
		VideoCodec:        "H.264",
		Audio:             "TrueHD 5.1",
		HasEncodeSettings: true,
		Release: api.ReleaseInfo{
			Category:   "MOVIE",
			Title:      "Example Film",
			Year:       2026,
			Resolution: "1080p",
		},
		Identity: api.ExternalIdentity{
			SourcePath: source,
			Generation: 1,
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     123,
			IMDBID:     1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: source,
			Generation: 1,
			IMDB: &api.IMDBMetadata{
				IMDBID: 1234567,
				Title:  "Example Film",
				Year:   2026,
				Type:   "movie",
			},
			TMDB: &api.TMDBMetadata{
				TMDBID:   123,
				Category: "MOVIE",
				Title:    "Example Film",
				Year:     2026,
				Genres:   "Drama",
			},
		},
		MediaInfoTextPath: mediaInfo,
		LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "English",
			TrackCoverageComplete: true,
			PrimaryAudioTrackID:   "main",
			Tracks: []api.MediaTrackFacts{
				{
					ID:               "main",
					Kind:             api.MediaTrackAudio,
					Role:             api.AudioRoleProgramme,
					Codec:            "TrueHD",
					Languages:        []string{"English"},
					Default:          true,
					DefaultKnown:     true,
					StreamOrderKnown: true,
					StreamOrder:      1,
				},
				{
					ID:               "alternate",
					Kind:             api.MediaTrackAudio,
					Role:             api.AudioRoleAlternateMix,
					Codec:            "TrueHD",
					Languages:        []string{"English"},
					DefaultKnown:     true,
					StreamOrderKnown: true,
					StreamOrder:      2,
				},
				{
					ID:               "companion-0",
					Kind:             api.MediaTrackAudio,
					Role:             api.AudioRoleCompatibility,
					Codec:            "AC-3",
					Languages:        []string{"English"},
					DefaultKnown:     true,
					StreamOrderKnown: true,
					StreamOrder:      3,
				},
				{
					ID:               "companion-1",
					Kind:             api.MediaTrackAudio,
					Role:             api.AudioRoleCompatibility,
					Codec:            "AC-3",
					Languages:        []string{"English"},
					DefaultKnown:     true,
					StreamOrderKnown: true,
					StreamOrder:      4,
				},
			},
		}),
		Assessments: api.ReleaseAssessments{MediaInfoUniqueID: api.UniqueIDStatusPresent, MediaInfoEncodeSettings: api.EncodeSettingsStatusPresent},
	}
	return base
}
