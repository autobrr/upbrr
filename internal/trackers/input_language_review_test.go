// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"strings"
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
	media.OriginalLanguage = ""
	media.Tracks[0].Languages = []string{"und"}
	media.Tracks[1].AudioOrigin = ""
	if result := evaluate(); len(result.Fields) != 0 {
		t.Fatalf("unknown original interrogated programme tracks: %#v", result.Fields)
	}
	media.Tracks[0].Languages = []string{"English"}
	media.Tracks[0].AudioOrigin = api.TrackAudioDubbed
	media.Tracks[1].Languages = []string{"und"}
	if result := evaluate(); len(result.Fields) != 1 || result.Fields[0].TrackID != "second" ||
		*result.Fields[0].CorrectionField != api.CorrectionFieldMetadataTrackAudioOrigins || !strings.Contains(result.Fields[0].Message, "confirmed dub") {
		t.Fatalf("confirmed dub did not target only its potential original companion: %#v", result.Fields)
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

func TestOriginalMultilingualReviewTargetsUnresolvedCompanion(t *testing.T) {
	media := api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		Tracks: []api.MediaTrackFacts{
			{
				ID:                  "original",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				ManifestFingerprint: "manifest",
				Languages:           []string{"mul"},
				AudioOrigin:         api.TrackAudioOriginal,
			},
			{
				ID:                  "companion",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				ManifestFingerprint: "manifest",
				Languages:           []string{"und"},
			},
		},
	}
	binding := WithLanguagePolicy(NoExtraValidationPolicy("test"), LanguagePolicy{OriginalAudio: LanguageProhibited, ExtraDubs: LanguageProhibited})
	subject := api.UploadSubject{LanguageFacts: mediafacts.ResolveLanguages(media)}
	fields := inputLanguageReview(binding, subject, "ONE")
	if len(fields) == 0 || slices.ContainsFunc(fields, func(field api.InputReadinessFieldOutcome) bool { return field.TrackID != "companion" }) {
		t.Fatalf("original soundtrack requested language enumeration: %+v", fields)
	}
	media.Tracks[1].Languages = []string{"English"}
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	fields = inputLanguageReview(binding, subject, "ONE")
	if len(fields) != 1 || fields[0].TrackID != "companion" || fields[0].CorrectionField == nil || *fields[0].CorrectionField != api.CorrectionFieldMetadataTrackAudioOrigins {
		t.Fatalf("unclassified companion origin was accepted without review: %+v", fields)
	}
	media.Tracks[1].Languages = []string{"German"}
	media.Tracks[1].AudioOrigin = api.TrackAudioDubbed
	failures := EvaluateLanguagePolicy(api.TrackerValidationSubject{LanguageFacts: mediafacts.ResolveLanguages(media)}, LanguagePolicy{OriginalAudio: LanguageProhibited, ExtraDubs: LanguageProhibited})
	if len(failures) != 1 || failures[0].Rule != "language_extra_dub" || !api.IsStrictRuleFailure(failures[0]) {
		t.Fatalf("original multilingual soundtrack permitted an unrelated dub: %+v", failures)
	}
}
