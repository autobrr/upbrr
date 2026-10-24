// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestInputLanguageReviewDeduplicatesSelectedTrackers(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"ONE", "TWO"} {
		if err := registry.RegisterDescriptor(Descriptor{
			Name:       name,
			Definition: stubDefinition{name: name},
			Validation: WithLanguagePolicy(NoExtraValidationPolicy(name), LanguagePolicy{OriginalAudio: LanguageProhibited}),
		}); err != nil {
			t.Fatal(err)
		}
	}
	media := api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{
			{
				ID:                  "first",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				ManifestFingerprint: "manifest",
				Languages:           []string{"und"},
			},
			{
				ID:                  "second",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleAlternateMix,
				ManifestFingerprint: "manifest",
			},
			{
				ID:                  "commentary",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleCommentary,
				ManifestFingerprint: "manifest",
			},
		},
	}
	evaluate := func() api.InputReadinessEvaluation {
		t.Helper()
		result, err := EvaluateInputReadiness(registry, []api.TrackerID{"ONE", "TWO", "ONE"}, api.UploadSubject{LanguageFacts: mediafacts.ResolveLanguages(media)})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := evaluate()
	if len(result.Fields) != 4 {
		t.Fatalf("expected independent language and origin questions per programme track: %#v", result.Fields)
	}
	for _, field := range result.Fields {
		if field.TrackID == "commentary" || field.TrackID == "" || field.CorrectionField == nil || (*field.CorrectionField != api.CorrectionFieldMetadataTrackLanguages && *field.CorrectionField != api.CorrectionFieldMetadataTrackAudioOrigins) || !slices.Equal(field.TrackerIDs, []api.TrackerID{"ONE", "TWO"}) {
			t.Fatalf("bad correction target: %#v", field)
		}
	}
	media.Tracks[0].Languages = []string{"Japanese"}
	media.Tracks[1].Languages = []string{"Japanese", "English"}
	media.Tracks[1].AudioOrigin = api.TrackAudioOriginal
	if result := evaluate(); len(result.Fields) != 0 {
		t.Fatalf("resolved tracks asked again: %#v", result.Fields)
	}
}

func TestInputLanguageReviewRequiresTheCurrentPolicyPredicate(t *testing.T) {
	media := api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{{
			ID:                  "one",
			Kind:                api.MediaTrackAudio,
			Role:                api.AudioRoleProgramme,
			ManifestFingerprint: "manifest",
		}},
	}
	binding := WithLanguagePolicy(NoExtraValidationPolicy("test"), LanguagePolicy{EnglishSubtitles: "without_english", MissingSubtitles: LanguageProhibited})
	subject := api.UploadSubject{LanguageFacts: mediafacts.ResolveLanguages(media)}
	if len(inputLanguageReview(binding, subject, "ONE")) != 1 {
		t.Fatal("missing required programme correction")
	}
	subject.LanguageFacts.SubtitleLanguages = []string{"English"}
	if len(inputLanguageReview(binding, subject, "ONE")) != 0 {
		t.Fatal("asked for language unused by the current predicate")
	}
	subject.LanguageFacts.SubtitleLanguages = nil
	subject.Type = "DISC"
	if len(inputLanguageReview(binding, subject, "ONE")) != 0 {
		t.Fatal("asked for exempt full disc")
	}
	subject.Type = "ENCODE"
	subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{AudioAbsent: true, TrackCoverageComplete: true})
	if len(inputLanguageReview(binding, subject, "ONE")) != 0 {
		t.Fatal("invented a track for absent audio")
	}
}
