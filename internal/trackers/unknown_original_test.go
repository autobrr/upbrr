// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers_test

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUnknownOriginalDoesNotRequireComparisonEvidence(t *testing.T) {
	policy := trackers.LanguagePolicy{
		OriginalAudio:    trackers.LanguageProhibited,
		ExtraDubs:        trackers.LanguageProhibited,
		OriginalFirst:    trackers.LanguageProhibited,
		OriginalDefault:  trackers.LanguageProhibited,
		EnglishSubtitles: "foreign_without_dub",
		MissingSubtitles: trackers.LanguageProhibited,
	}
	for _, languages := range [][]string{{"English"}, nil, {"und"}, {"mul"}, {"Japanese", "English"}} {
		t.Run("track="+languageName(languages), func(t *testing.T) {
			facts := mediafacts.ResolveLanguages(api.MediaFacts{TrackCoverageComplete: true, Tracks: []api.MediaTrackFacts{{
				ID:                  "programme",
				ManifestFingerprint: "report",
				Kind:                api.MediaTrackAudio,
				Role:                api.AudioRoleProgramme,
				Languages:           languages,
			}}})
			if got := trackers.EvaluateLanguagePolicy(api.TrackerValidationSubject{LanguageFacts: facts}, policy); len(got) != 0 {
				t.Fatalf("unknown original required comparison evidence: %#v", got)
			}
			if facts.OriginalLanguagesKnown || facts.HasOriginalAudio() || facts.HasEnglishDub() || !facts.HasUnresolvedProgrammeAudioOrigin() {
				t.Fatalf("unknown facts were classified: %#v", facts)
			}
		})
	}
}

func languageName(languages []string) string {
	if len(languages) == 0 {
		return "missing"
	}
	return languages[0]
}

func TestUnknownOriginalTrackerComparisonsAndConfirmedDubs(t *testing.T) {
	for _, tracker := range []string{"AITHER", "BHD", "HDB", "ULCX", "BTN", "PTP", "AZ", "CZ", "DP", "OTW"} {
		t.Run(tracker, func(t *testing.T) {
			subject := languageSubject("", "English")
			subject.AudioLanguages = []string{"English"}
			subject.LanguageFacts.Tracks[0].AudioOrigin = ""
			if tracker == "BHD" {
				subject.Tracker = tracker
				subject.QuestionnaireAnswers = map[string]string{trackers.LanguageQuestionKey(subject, "existing_release"): "unchanged_or_new"}
			}
			if got := nonAdvisoryFailures(languageFailures(t, tracker, subject)); len(got) != 0 {
				t.Fatalf("missing original blocked unclassified English: %#v", got)
			}
		})
	}
	for _, tracker := range []string{"BHD", "AITHER", "HDB", "ULCX", "CZ"} {
		subject := languageSubject("", "German")
		if got := languageFailures(t, tracker, subject); !slices.ContainsFunc(got, func(f api.RuleFailure) bool {
			return f.Rule == "language_extra_dub" && api.IsStrictRuleFailure(f)
		}) {
			t.Fatalf("%s lost confirmed non-English dub restriction: %#v", tracker, got)
		}
	}
	subject := languageSubject("", "English")
	subject.EffectiveMetadata.Genres = []string{"Drama"}
	if got := languageFailures(t, "HDB", subject); !slices.ContainsFunc(got, func(f api.RuleFailure) bool {
		return f.Rule == "language_english_dub" && api.IsStrictRuleFailure(f)
	}) {
		t.Fatalf("lost confirmed English dub restriction: %#v", got)
	}
	for _, tracker := range []string{"BHD", "HHD"} {
		if got := languageFailures(t, tracker, subject); !slices.ContainsFunc(got, func(f api.RuleFailure) bool {
			return f.Rule == "language_original" && api.IsStrictRuleFailure(f)
		}) {
			t.Fatalf("%s accepted a proven dub-only composition: %#v", tracker, got)
		}
	}
	for _, tracker := range []string{"BTN", "AZ"} {
		if got := languageFailures(t, tracker, languageSubject("", "German")); !slices.ContainsFunc(got, func(f api.RuleFailure) bool {
			return (f.Rule == "language_staff_dub_evidence" || f.Rule == "language_dub_exception_evidence") && api.IsStrictRuleFailure(f)
		}) {
			t.Fatalf("%s lost dub-specific evidence need: %#v", tracker, got)
		}
	}
}

func TestUnknownOriginalEnglishMixesNeedDubEvidence(t *testing.T) {
	subject := languageSubject("", "English", "English")
	for _, origin := range []api.TrackAudioOrigin{"", api.TrackAudioDubbed} {
		for i := range subject.LanguageFacts.Tracks {
			subject.LanguageFacts.Tracks[i].AudioOrigin = origin
		}
		failures := languageFailures(t, "HHD", subject)
		if slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_english_dub_count" }) != (origin == api.TrackAudioDubbed) {
			t.Fatalf("origin=%q failures=%#v", origin, failures)
		}
	}
}

func TestUnknownOriginalRetainsEstablishedSoundtrackDefaultAndOrder(t *testing.T) {
	subject := languageSubject("", "English", "German")
	subject.LanguageFacts.Tracks[1].AudioOrigin = api.TrackAudioOriginal
	failures := trackers.EvaluateLanguagePolicy(subject, trackers.LanguagePolicy{
		OriginalFirst: trackers.LanguageProhibited, OriginalDefault: trackers.LanguageProhibited,
	})
	for _, rule := range []string{"language_original_order", "language_original_default"} {
		if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == rule && api.IsStrictRuleFailure(f) }) {
			t.Fatalf("lost established soundtrack requirement %s: %#v", rule, failures)
		}
	}
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:               "unclassified",
		Kind:             api.MediaTrackAudio,
		Role:             api.AudioRoleProgramme,
		Languages:        []string{"French"},
		StreamOrder:      3,
		StreamOrderKnown: true,
		DefaultKnown:     true,
	})
	subject.LanguageFacts.ProgrammeLanguages = append(subject.LanguageFacts.ProgrammeLanguages, "French")
	policy := trackers.LanguagePolicy{OriginalFirst: trackers.LanguageProhibited}
	if failures := trackers.EvaluateLanguagePolicy(subject, policy); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_original_order"
	}) {
		t.Fatalf("later unclassified track erased the first confirmed dub: %#v", failures)
	}
	subject.LanguageFacts.Tracks[0].StreamOrder = 1
	subject.LanguageFacts.Tracks[1].StreamOrder = 2
	subject.LanguageFacts.Tracks[len(subject.LanguageFacts.Tracks)-1].StreamOrder = 0
	if failures := trackers.EvaluateLanguagePolicy(subject, policy); slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_original_order" && f.EvidenceStatus == api.MetadataEvidenceStatusComplete
	}) {
		t.Fatalf("an earlier unclassified track became a proven ordering defect: %#v", failures)
	}
}

func TestUnknownOriginalDubSurvivesAggregateClear(t *testing.T) {
	subject := languageSubject("", "English")
	subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
		Tracks:                   subject.LanguageFacts.Tracks,
		TrackCoverageComplete:    true,
		AudioLanguagesProvenance: api.FactProvenanceManualEmpty,
	})
	subject.EffectiveMetadata.Genres = []string{"Drama"}
	if subject.LanguageFacts.HasEnglishDub() || len(subject.LanguageFacts.ProgrammeLanguages) != 0 {
		t.Fatal("aggregate clear was refilled from inspected tracks")
	}
	if failures := languageFailures(t, "HDB", subject); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_english_dub_evidence" && api.IsStrictRuleFailure(f)
	}) {
		t.Fatalf("aggregate clear waived the confirmed dub restriction: %#v", failures)
	}
}

func TestUnknownOriginalConfirmedDubTargetsOnlyPotentialOriginal(t *testing.T) {
	subject := languageSubject("", "English")
	media := api.MediaFacts{TrackCoverageComplete: true, Tracks: append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
		ID:                  "potential-original",
		Kind:                api.MediaTrackAudio,
		Role:                api.AudioRoleProgramme,
		ManifestFingerprint: "report",
		Languages:           []string{"und"},
	})}
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	failures := languageFailures(t, "BHD", subject)
	if !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_dub_original_origin" && slices.Equal(f.AudioOriginCorrectionTrackIDs, []string{"potential-original"}) &&
			len(f.LanguageCorrectionTrackIDs) == 0 && api.IsStrictRuleFailure(f)
	}) {
		t.Fatalf("confirmed dub lost its specific original-soundtrack requirement: %#v", failures)
	}
	media.Tracks[len(media.Tracks)-1].AudioOrigin = api.TrackAudioOriginal
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	if failures := nonAdvisoryFailures(languageFailures(t, "BHD", subject)); len(failures) != 0 {
		t.Fatalf("verified original still required title or track languages: %#v", failures)
	}
}

func TestUnknownOriginalDubCannotUseContradictoryLanguages(t *testing.T) {
	subject := languageSubject("", "German")
	subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
		Tracks: subject.LanguageFacts.Tracks,
 TrackCoverageComplete: true,
		AudioLanguages: []string{"English"},
 AudioLanguagesProvenance: api.FactProvenanceManual,
	})
	if subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory {
		t.Fatal("fixture must retain contradictory aggregate and inspected evidence")
	}
	if failures := languageFailures(t, "ULCX", subject); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
		return f.Rule == "language_dub_evidence" && api.IsStrictRuleFailure(f)
	}) {
		t.Fatalf("contradictory correction waived a confirmed dub rule: %#v", failures)
	}
}
