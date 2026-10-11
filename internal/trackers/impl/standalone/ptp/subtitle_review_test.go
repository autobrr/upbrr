// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

import (
	"context"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
	"slices"
	"testing"
)

func TestPTPReviewedSubtitlePayload(t *testing.T) {
	for _, tt := range []struct {
		name, selection, languages, subtitles, trumpable string
		hardcoded                                        bool
	}{
		{
			name:      "full",
			selection: "English Hardcoded Subs (Full)",
			subtitles: "3",
			trumpable: "4",
		},
		{
			name:      "forced hardcoded",
			selection: "English Hardcoded Subs (Forced)",
			subtitles: "50",
			trumpable: "4",
			hardcoded: true,
		},
		{
			name:      "no english",
			selection: "No English Subs",
			subtitles: "44",
			trumpable: "14",
		},
		{
			name:      "mislabeled",
			selection: "English Softsubs Exist (Mislabeled)",
			subtitles: "44",
			trumpable: "",
		},
		{
			name:      "foreign hardcoded",
			selection: "Hardcoded Subs (Non-English)",
			languages: "French",
			subtitles: "5",
			trumpable: "4,14",
			hardcoded: true,
		},
		{
			name:      "multiple",
			selection: "English Hardcoded Subs (Full),English Hardcoded Subs (Forced)",
			subtitles: "3,50",
			trumpable: "4",
			hardcoded: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			answers := map[string]string{
				"trumpable_review":             "yes",
				"subtitle_tags":                tt.selection,
				"hardcoded_subtitle_languages": tt.languages,
			}
			meta := api.UploadSubject{
				Release:                     api.ReleaseInfo{Resolution: "1080p"},
				Container:                   "mkv",
				AudioLanguages:              []string{"Japanese"},
				HardcodedSubs:               tt.hardcoded,
				TrackerQuestionnaireAnswers: map[string]map[string]string{"PTP": answers},
			}
			fields, err := buildUploadFields(meta, "description", "123", answers, "")
			if err != nil {
				t.Fatal(err)
			}
			if fields["subtitles[]"] != tt.subtitles || fields["trumpable[]"] != tt.trumpable {
				t.Fatalf("subtitles=%q trumpable=%q, want %q %q", fields["subtitles[]"], fields["trumpable[]"], tt.subtitles, tt.trumpable)
			}
		})
	}
}

func TestPTPSubtitleReviewConditions(t *testing.T) {
	for _, tt := range []struct {
		name string
		meta api.UploadSubject
		want bool
	}{
		{
			name: "foreign audio",
			meta: api.UploadSubject{AudioLanguages: []string{"French"}},
			want: true,
		},
		{name: "unknown audio", want: true},
		{
			name: "second English audio",
			meta: api.UploadSubject{AudioLanguages: []string{"French", "English"}},
			want: true,
		},
		{name: "English first audio", meta: api.UploadSubject{AudioLanguages: []string{"English"}}},
		{name: "English subtitles", meta: api.UploadSubject{SubtitleLanguages: []string{"English"}}},
		{name: "forced English subtitles", meta: api.UploadSubject{SubtitleLanguages: []string{"English - Forced"}}},
		{
			name: "hardcoded English",
			meta: api.UploadSubject{HardcodedSubs: true, HardcodedSubtitleLanguages: []string{"English"}},
		},
		{name: "explicit legacy no", meta: api.UploadSubject{TrackerQuestionnaireAnswers: map[string]map[string]string{"PTP": {"no_english_subtitles": "no"}}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			q := subtitleReviewFields(tt.meta, tt.meta.TrackerQuestionnaireAnswers["PTP"])
			if (len(q) > 0) != tt.want {
				t.Fatalf("questionnaire=%#v want review %v", q, tt.want)
			}
		})
	}
}

func TestPTPSubtitleReviewStagesAndNoDecision(t *testing.T) {
	meta := api.UploadSubject{AudioLanguages: []string{"French"}}
	answers := map[string]string{}
	first := subtitleReviewFields(meta, answers)
	if len(first) != 1 || first[0].Key != "trumpable_review" || !subtitleReviewPending(meta, answers) {
		t.Fatalf("first=%#v", first)
	}
	answers["trumpable_review"] = "no"
	if subtitleReviewPending(meta, answers) {
		t.Fatal("explicit no still pending")
	}
	_, tags, err := reviewedSubtitles(meta, answers)
	if err != nil || len(tags) != 0 {
		t.Fatalf("tags=%v err=%v", tags, err)
	}
	answers["trumpable_review"] = "yes"
	fields := subtitleReviewFields(meta, answers)
	if len(fields) != 2 || fields[1].Kind != "multiselect" || len(fields[1].Options) != 5 {
		t.Fatalf("fields=%#v", fields)
	}
	answers["subtitle_tags"] = "not-a-choice"
	if !subtitleReviewPending(meta, answers) {
		t.Fatal("invalid review became ready")
	}
	if _, _, err := reviewedSubtitles(meta, answers); err == nil {
		t.Fatal("invalid review reached payload")
	}
}

func TestPTPExplicitHardcodedChoiceSetsTrackerLocalIntent(t *testing.T) {
	for _, selection := range []string{"English Hardcoded Subs (Forced)", "Hardcoded Subs (Non-English)"} {
		_, tags, err := reviewedSubtitles(api.UploadSubject{}, map[string]string{
			"trumpable_review":             "yes",
			"subtitle_tags":                selection,
			"hardcoded_subtitle_languages": "French",
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(tags) == 0 || tags[0] != 4 || slices.Contains(tags, 50) || slices.Contains(tags, 15) {
			t.Fatalf("hardcoded choice emitted temporary wire tags: %v", tags)
		}
	}
}

func TestPTPProjectionCapturesReviewedPayloadChoices(t *testing.T) {
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	meta := api.UploadSubject{
		ReleaseName:   "Example.Movie.2026.1080p.BluRay.x264-GRP",
		LanguageFacts: ptpLanguageSubject("French", "French").LanguageFacts,
		Release: api.ReleaseInfo{
			Title:      "Example Movie",
			Year:       2026,
			Category:   "MOVIE",
			Resolution: "1080p",
		},
		Identity:       api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 123},
		Container:      "mkv",
		Source:         "BluRay",
		Type:           "ENCODE",
		VideoCodec:     "H.264",
		AudioLanguages: []string{"French"},
	}
	input := trackers.PreparationInput{Tracker: "PTP", Meta: meta}
	pending, failure := registry.ProjectRelease(t.Context(), input, "input", "catalog", "config")
	if failure != nil {
		t.Fatal(failure)
	}
	if len(pending.RequiredActions) != 1 || pending.RequiredActions[0].Kind != api.RequiredActionAnswerQuestionnaire {
		t.Fatalf("missing PTP review: %#v", pending)
	}
	input.Meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"PTP": {"trumpable_review": "yes", "subtitle_tags": "No English Subs"}}
	reviewed, failure := registry.ProjectRelease(t.Context(), input, "input-reviewed", "catalog", "config")
	if failure != nil || !reviewed.UploadReady || reviewed.WaivableRuleFingerprint != "" {
		t.Fatalf("payload choices acquired a source-comparison waiver: %#v, %v", reviewed, failure)
	}
	input.Projection = &reviewed
	input.Intent = trackers.PreparationIntentDryRun
	// Mutable description inputs must not replace the exact accepted review.
	input.Meta.TrackerQuestionnaireAnswers["PTP"]["trumpable_review"] = "no"
	var payload map[string]string
	_, failure = trackers.PrepareAdapter(t.Context(), input, nil, func(_ context.Context, prepared trackers.PreparationInput) (trackers.PreparedOperation, error) {
		var err error
		payload, err = buildUploadFields(prepared.Meta, "description", "123", prepared.Meta.TrackerQuestionnaireAnswers["PTP"], "")
		return trackers.NewPreparedOperation(api.TrackerDryRunEntry{
			Tracker:         "PTP",
			Status:          "ready",
			ReleaseName:     reviewed.UploadReleaseName,
			EditionFeatures: editionFeatures(prepared.Meta),
			Payload:         payload,
		}, nil, nil), err
	})
	if failure != nil {
		t.Fatal(failure)
	}
	if payload["trumpable[]"] != "14" {
		t.Fatalf("accepted review was lost: %#v", payload)
	}
}

func TestPTPHardcodedReviewPreservesLegacyNoEnglishOverride(t *testing.T) {
	meta := api.UploadSubject{
		HardcodedSubs:              true,
		HardcodedSubtitleLanguages: []string{"French"},
		AudioLanguages:             []string{"French"},
	}
	for _, legacy := range []string{"no", "yes"} {
		_, tags, err := reviewedSubtitles(meta, map[string]string{"subtitle_tags": "Hardcoded Subs (Non-English)", "no_english_subtitles": legacy})
		if err != nil {
			t.Fatal(err)
		}
		want := "4"
		if legacy == "yes" {
			want = "4,14"
		}
		if joinInts(tags) != want {
			t.Fatalf("legacy %s tags=%v want %s", legacy, tags, want)
		}
	}
}

func TestPTPReviewDoesNotOverrideCorrectedEnglishEvidence(t *testing.T) {
	answers := map[string]string{"trumpable_review": "yes", "subtitle_tags": "No English Subs"}
	meta := api.UploadSubject{AudioLanguages: []string{"French"}, TrackerQuestionnaireAnswers: map[string]map[string]string{"PTP": answers}}
	if !requiresSubtitleReview(meta) {
		t.Fatal("initial foreign release should require review")
	}
	for _, correctAudio := range []bool{false, true} {
		corrected := meta
		if correctAudio {
			corrected.AudioLanguages = []string{"English"}
		} else {
			corrected.SubtitleLanguages = []string{"English"}
		}
		if len(subtitleReviewFields(corrected, corrected.TrackerQuestionnaireAnswers["PTP"])) != 0 {
			t.Fatal("stale missing-English question survived correction")
		}
		_, tags, err := reviewedSubtitles(corrected, answers)
		if err != nil || len(tags) != 0 {
			t.Fatalf("stale tag14 survived correction: %v, %v", tags, err)
		}
	}
}

func TestPTPConflictingSubtitleClaimsStayPending(t *testing.T) {
	for _, tt := range []struct {
		name, selection string
		subtitles       []string
	}{
		{name: "full and none", selection: "English Hardcoded Subs (Full),No English Subs"},
		{name: "forced and none", selection: "English Hardcoded Subs (Forced),No English Subs"},
		{name: "mislabeled and none", selection: "English Softsubs Exist (Mislabeled),No English Subs"},
		{
			name:      "prepared English",
			selection: "No English Subs",
			subtitles: []string{"English"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			answers := map[string]string{"subtitle_tags": tt.selection}
			meta := api.UploadSubject{HardcodedSubs: true, SubtitleLanguages: tt.subtitles}
			if !subtitleReviewPending(meta, answers) {
				t.Fatal("contradictory claim became review-ready")
			}
			if _, _, err := reviewedSubtitles(meta, answers); err == nil {
				t.Fatal("contradictory claim reached payload")
			}
		})
	}
}

func TestPTPHardcodedLanguageAnswerCannotContradictNoEnglish(t *testing.T) {
	for _, legacy := range []string{"", "yes"} {
		selected := "Hardcoded Subs (Non-English)"
		if legacy == "" {
			selected += ",No English Subs"
		}
		answers := map[string]string{
			"subtitle_tags":                selected,
			"hardcoded_subtitle_languages": "English",
			"no_english_subtitles":         legacy,
		}
		meta := api.UploadSubject{HardcodedSubs: true}
		if !subtitleReviewPending(meta, answers) {
			t.Fatal("English language answer became ready with No English claim")
		}
		if _, _, err := reviewedSubtitles(meta, answers); err == nil {
			t.Fatal("English language answer reached payload with No English tag")
		}
	}
}

func TestPTPPresetHardcodedLanguagesAvoidPrompts(t *testing.T) {
	for _, tt := range []struct{ language, subtitle, tags string }{{"English (Full)", "3", "4"}, {"English (Forced)", "50", "4"}, {"Spanish", "4", "4,14"}} {
		meta := api.UploadSubject{
			HardcodedSubs:              true,
			HardcodedSubtitleLanguages: []string{tt.language},
			AudioLanguages:             []string{"French"},
			Release:                    api.ReleaseInfo{Resolution: "1080p"},
			Container:                  "mkv",
		}
		if len(subtitleReviewFields(meta, meta.TrackerQuestionnaireAnswers["PTP"])) != 0 {
			t.Fatalf("preset %s asked again", tt.language)
		}
		fields, err := buildUploadFields(meta, "description", "123", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		if fields["subtitles[]"] != tt.subtitle || fields["trumpable[]"] != tt.tags {
			t.Fatalf("%s payload=%v", tt.language, fields)
		}
	}
	if len(subtitleReviewFields(api.UploadSubject{HardcodedSubs: true}, nil)) == 0 {
		t.Fatal("unknown hardcoded language must prompt")
	}
}

func TestPTPConsumesTypedHardcodedCoverage(t *testing.T) {
	for _, coverage := range []api.SubtitleCoverage{api.SubtitleCoverageFull, api.SubtitleCoverageForced} {
		subject := api.UploadSubject{
			HardcodedSubs:              true,
			HardcodedSubtitleLanguages: []string{"English"},
			HardcodedSubtitleCoverage:  []api.SubtitleLanguageCoverage{{Language: "English", Coverage: coverage}},
		}
		got := resolveSubtitles(subject)
		want := 3
		if coverage == api.SubtitleCoverageForced {
			want = 50
		}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("%s = %v", coverage, got)
		}
	}
}

func TestPTPMixedUnqualifiedAndForcedHardcodedCoverage(t *testing.T) {
	meta := api.UploadSubject{
		HardcodedSubs:              true,
		HardcodedSubtitleLanguages: []string{"English"},
		HardcodedSubtitleCoverage:  []api.SubtitleLanguageCoverage{{Language: "English", Coverage: api.SubtitleCoverageUnspecified}, {Language: "English", Coverage: api.SubtitleCoverageForced}},
	}
	got := resolveSubtitles(meta)
	if !slices.Equal(got, []int{3, 50}) {
		t.Fatalf("mixed coverage = %v", got)
	}
}
