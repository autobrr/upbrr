// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPTPRemuxTrackOrderDoesNotAffectEligibility(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*api.TrackerValidationSubject)
	}{
		{name: "missing positions", change: func(*api.TrackerValidationSubject) {}},
		{name: "secondary audio first", change: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].StreamOrder, s.LanguageFacts.Tracks[0].StreamOrderKnown = 2, true
			s.LanguageFacts.Tracks[1].StreamOrder, s.LanguageFacts.Tracks[1].StreamOrderKnown = 1, true
			s.LanguageFacts.Tracks[2].StreamOrder, s.LanguageFacts.Tracks[2].StreamOrderKnown = 3, true
		}},
		{name: "subtitles first", change: func(s *api.TrackerValidationSubject) {
			s.LanguageFacts.Tracks[0].StreamOrder, s.LanguageFacts.Tracks[0].StreamOrderKnown = 2, true
			s.LanguageFacts.Tracks[1].StreamOrder, s.LanguageFacts.Tracks[1].StreamOrderKnown = 3, true
			s.LanguageFacts.Tracks[2].StreamOrder, s.LanguageFacts.Tracks[2].StreamOrderKnown = 1, true
		}},
		{name: "duplicate positions", change: func(s *api.TrackerValidationSubject) {
			for i := range s.LanguageFacts.Tracks {
				s.LanguageFacts.Tracks[i].StreamOrder, s.LanguageFacts.Tracks[i].StreamOrderKnown = 1, true
			}
		}},
		{name: "reversed manifest", change: func(s *api.TrackerValidationSubject) { slices.Reverse(s.LanguageFacts.Tracks) }},
		{name: "retained out of order answer", change: func(s *api.TrackerValidationSubject) {
			ptpLanguageAnswer(s, "remux_track_order", "out_of_order")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := ptpLanguageSubject("Japanese", "Japanese", "English")
			subject.Type, subject.DiscType = "REMUX", "BDMV"
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
				ID:        "subtitle",
				Kind:      api.MediaTrackSubtitle,
				Languages: []string{"English"},
			})
			test.change(&subject)
			if failures := ptpNonAdvisoryFailures(subject); len(failures) != 0 {
				t.Fatalf("track order gated eligible remux: %+v", failures)
			}
		})
	}
}

func TestPTPQuestionnairesDoNotRequestRemuxOrder(t *testing.T) {
	for _, mode := range []api.WorkflowExecutionMode{api.WorkflowExecutionModeNormal, api.WorkflowExecutionModeDebug} {
		subject := ptpLanguageSubject("Japanese", "Japanese", "English")
		subject.Type, subject.DiscType = "REMUX", "BDMV"
		meta := api.UploadSubject{
			Type:              subject.Type,
			DiscType:          subject.DiscType,
			Identity:          subject.Identity,
			LanguageFacts:     subject.LanguageFacts,
			AudioLanguages:    []string{"Japanese", "English"},
			SubtitleLanguages: []string{"English"},
		}
		key := trackers.LanguageQuestionKey(api.NewTrackerValidationSubject(meta, "PTP"), "remux_track_order")
		meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"PTP": {key: "out_of_order"}}
		schemas := []*api.TrackerQuestionnaire{
			buildQuestionnaire(meta, "123"),
			New().TrackerAnswerSchema(trackers.PreparationInput{Meta: meta, ExecutionMode: mode}),
			projectionQuestionnaire(trackers.PreparationInput{Meta: meta, ExecutionMode: mode}),
		}
		for _, schema := range schemas {
			if schema == nil {
				continue
			}
			for _, field := range schema.Fields {
				if strings.HasPrefix(field.Key, "remux_track_order_") {
					t.Fatalf("mode %s retained order-only question: %+v", mode, field)
				}
			}
		}
	}
}
