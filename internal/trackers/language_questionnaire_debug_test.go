// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLanguageEligibilityQuestionsPreserveDebugBypass(t *testing.T) {
	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	facts := languageSubject("Japanese", "Japanese", "English", "German").LanguageFacts
	facts.SubtitleLanguages = nil
	facts.Tracks = append(facts.Tracks,
		api.MediaTrackFacts{
ID: "compatibility",
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleCompatibility,
 Codec: "AC-3",
 Languages: []string{"Japanese"},
},
		api.MediaTrackFacts{
ID: "commentary",
 Kind: api.MediaTrackAudio,
 Role: api.AudioRoleCommentary,
 Languages: []string{"Japanese"},
},
	)
	meta := api.UploadSubject{
Type: "REMUX",
 Source: "BluRay",
 PersonalRelease: true,
 LanguageFacts: facts,
}
	for _, name := range []string{"BHD", "BTN", "HHD", "AITHER", "LST", "ULCX", "AZ", "HDB", "LUME", "RF", "PTP"} {
		t.Run(name, func(t *testing.T) {
			descriptor, ok := registry.LookupDescriptor(name)
			if !ok {
				t.Fatal("missing descriptor")
			}
			provider, ok := descriptor.Definition.(trackers.ProjectionQuestionnaireProvider)
			if !ok {
				t.Fatal("missing questionnaire provider")
			}
			normalMeta := meta
			if name == "AZ" {
				normalMeta.Type = "ENCODE"
			}
			normal := provider.ProjectionQuestionnaire(trackers.PreparationInput{Tracker: name, Meta: normalMeta})
			debug := provider.ProjectionQuestionnaire(trackers.PreparationInput{
Tracker: name,
 Meta: normalMeta,
 ExecutionMode: api.WorkflowExecutionModeDebug,
})
			if normal == nil || debug == nil || len(normal.Fields) != len(debug.Fields) {
				t.Fatal("debug lost visible review evidence")
			}
			changed := 0
			for i, field := range normal.Fields {
				actual := debug.Fields[i]
				if field.Key != actual.Key {
					t.Fatal("debug changed evidence binding")
				}
				payload := name == "BTN" && strings.HasPrefix(field.Key, "primary_audio_country_") || name == "PTP" && (field.Key == "trumpable_review" || field.Key == "no_english_subtitles")
				if payload {
					if actual.Required != field.Required {
						t.Fatalf("payload requirement relaxed: %s", field.Key)
					}
				} else if field.Required {
					changed++
					if actual.Required {
						t.Fatalf("debug re-blocked bypassed eligibility: %s", field.Key)
					}
				}
			}
			if changed == 0 {
				t.Fatal("fixture did not exercise required eligibility evidence")
			}
		})
	}
}

func TestAitherLanguageBalanceStillRequiredInDebug(t *testing.T) {
	registry, err := impl.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, _ := registry.LookupDescriptor("AITHER")
	provider, ok := descriptor.Definition.(trackers.ProjectionQuestionnaireProvider)
	if !ok {
		t.Fatal("missing questionnaire provider")
	}
	facts := languageSubject("Japanese", "Japanese").LanguageFacts
	facts.Tracks[0].Languages = []string{"Japanese", "English"}
	schema := provider.ProjectionQuestionnaire(trackers.PreparationInput{
Tracker: "AITHER",
 Meta: api.UploadSubject{Type: "ENCODE", LanguageFacts: facts},
 ExecutionMode: api.WorkflowExecutionModeDebug,
})
	if schema == nil {
		t.Fatal("missing balance question")
	}
	for _, field := range schema.Fields {
		if strings.HasPrefix(field.Key, "multilingual_balance_") && field.Required {
			return
		}
	}
	t.Fatal("debug waived evidence needed for truthful naming")
}
