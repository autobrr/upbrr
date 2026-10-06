// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lume

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLumeOriginalAndDubGroups(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		tracks []api.MediaTrackFacts
		want   string
	}{
		{
			name: "interleaved original",
			tracks: []api.MediaTrackFacts{
				lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("English", api.AudioRoleProgramme), lumeAudio("Japanese", api.AudioRoleAlternateMix),
			},
			want: "language_original_order",
		},
		{
			name: "commentary before original",
			tracks: []api.MediaTrackFacts{
				lumeAudio("English", api.AudioRoleCommentary), lumeAudio("Japanese", api.AudioRoleProgramme),
			},
			want: "language_original_order",
		},
		{
			name: "commentary splits dubs",
			tracks: []api.MediaTrackFacts{
				lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("English", api.AudioRoleProgramme), lumeAudio("Japanese", api.AudioRoleCommentary), lumeAudio("German", api.AudioRoleProgramme),
			},
			want: "language_dub_order",
		},
		{
			name: "alternate mix splits dubs",
			tracks: []api.MediaTrackFacts{
				lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("English", api.AudioRoleProgramme), lumeAudio("German", api.AudioRoleAlternateMix), lumeAudio("Spanish", api.AudioRoleProgramme),
			},
			want: "language_dub_order",
		},
		{
			name: "English dub follows German",
			tracks: []api.MediaTrackFacts{
				lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("German", api.AudioRoleProgramme), lumeAudio("English", api.AudioRoleProgramme),
			},
			want: "language_dub_order",
		},
		{
			name: "non English dubs out of order",
			tracks: []api.MediaTrackFacts{
				lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("Spanish", api.AudioRoleProgramme), lumeAudio("German", api.AudioRoleProgramme),
			},
			want: "language_dub_order",
		},
		{name: "ordered dubs with secondary after", tracks: []api.MediaTrackFacts{
			lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("Japanese", api.AudioRoleAlternateMix), lumeAudio("English", api.AudioRoleProgramme), lumeAudio("German", api.AudioRoleProgramme), lumeAudio("Spanish", api.AudioRoleCommentary),
		}},
		{name: "extra dub without English is allowed", tracks: []api.MediaTrackFacts{
			lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("German", api.AudioRoleProgramme),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			subject := lumeLanguageSubject(test.tracks)
			failures := languageAssessment(subject)
			if test.want != "" {
				requireLUMEValidationFailure(t, failures, test.want, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
			} else if len(failures) != 0 {
				t.Fatalf("permitted tracks: %+v", failures)
			}
		})
	}
}

func TestLumeUnknownSecondaryAndSubtitleLanguages(t *testing.T) {
	t.Parallel()
	for _, kind := range []api.MediaTrackKind{api.MediaTrackAudio, api.MediaTrackSubtitle} {
		for _, language := range []string{"", "und", "mul", "Multiple Languages", "Unknown language"} {
			t.Run(string(kind)+"/"+language, func(t *testing.T) {
				subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme)})
				subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
					ID:         "ambiguous-track",
					Kind:       kind,
					Role:       api.AudioRoleCommentary,
					Commentary: true,
					Languages:  []string{language},
					Title:      "Commentary",
				})
				requireLUMEValidationFailure(t, languageAssessment(subject), "language_track_language", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
			})
		}
	}
}

func TestLumePersonalRecommendationExemptions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, answer    string
		personal, anime bool
		want            api.RuleDisposition
		status          api.MetadataEvidenceStatus
	}{
		{
			name:   "ordinary",
			want:   api.RuleDispositionAdvisory,
			status: api.MetadataEvidenceStatusComplete,
		},
		{
			name:     "personal unknown exemption",
			personal: true,
			want:     api.RuleDispositionStrict,
			status:   api.MetadataEvidenceStatusPartial,
		},
		{
			name:     "personal no exemption",
			personal: true,
			answer:   "none",
			want:     api.RuleDispositionStrict,
			status:   api.MetadataEvidenceStatusComplete,
		},
		{
			name:     "TRaSH tier",
			personal: true,
			answer:   "trash_tier",
			want:     api.RuleDispositionAdvisory,
			status:   api.MetadataEvidenceStatusComplete,
		},
		{
			name:     "older upload",
			personal: true,
			answer:   "previously_uploaded",
			want:     api.RuleDispositionAdvisory,
			status:   api.MetadataEvidenceStatusComplete,
		},
		{
			name:     "language tier not exempt",
			personal: true,
			answer:   "language_tier",
			want:     api.RuleDispositionStrict,
			status:   api.MetadataEvidenceStatusPartial,
		},
		{
			name:     "anime must use Anime tier",
			personal: true,
			anime:    true,
			answer:   "trash_tier",
			want:     api.RuleDispositionStrict,
			status:   api.MetadataEvidenceStatusPartial,
		},
		{
			name:     "Anime tier",
			personal: true,
			anime:    true,
			answer:   "anime_tier",
			want:     api.RuleDispositionAdvisory,
			status:   api.MetadataEvidenceStatusComplete,
		},
		{
			name:     "nonanime cannot use Anime tier",
			personal: true,
			answer:   "anime_tier",
			want:     api.RuleDispositionStrict,
			status:   api.MetadataEvidenceStatusPartial,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("English", api.AudioRoleProgramme), lumeAudio("Japanese", api.AudioRoleAlternateMix)})
			subject.PersonalRelease, subject.Anime = test.personal, test.anime
			setLumeAnswer(&subject, "language_personal_exemption", test.answer)
			requireLUMEValidationFailure(t, languageAssessment(subject), "language_original_order", test.want, test.status)
		})
	}
}

func TestLumeMeasuredMetadataAndPersonalReview(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, rule string
		mutate     func(*api.TrackerValidationSubject)
	}{
		{
			name:   "missing descriptive title",
			rule:   "language_track_titles",
			mutate: func(subject *api.TrackerValidationSubject) { subject.LanguageFacts.Tracks[0].Title = "" },
		},
		{
			name: "full subtitles incorrectly forced",
			rule: "language_track_flags",
			mutate: func(subject *api.TrackerValidationSubject) {
				track := api.MediaTrackFacts{
					ID:        "full",
					Kind:      api.MediaTrackSubtitle,
					Languages: []string{"English (Full)"},
					Title:     "Complete dialogue",
					Forced:    true,
				}
				subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
			},
		},
		{
			name: "forced flag missing",
			rule: "language_track_flags",
			mutate: func(subject *api.TrackerValidationSubject) {
				track := api.MediaTrackFacts{
					ID:        "forced",
					Kind:      api.MediaTrackSubtitle,
					Languages: []string{"English (Forced)"},
					Title:     "Foreign dialogue",
				}
				subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, track)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme)})
			test.mutate(&subject)
			requireLUMEValidationFailure(t, languageAssessment(subject), test.rule, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
			subject.PersonalRelease = true
			setLumeAnswer(&subject, "language_personal_exemption", "none")
			setLumeAnswer(&subject, "language_track_metadata", "appropriate")
			requireLUMEValidationFailure(t, languageAssessment(subject), test.rule, api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
			setLumeAnswer(&subject, "language_personal_exemption", "trash_tier")
			requireLUMEValidationFailure(t, languageAssessment(subject), test.rule, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
		})
	}
	subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme)})
	subject.PersonalRelease = true
	setLumeAnswer(&subject, "language_personal_exemption", "none")
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_track_metadata", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	setLumeAnswer(&subject, "language_track_metadata", "inappropriate")
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_track_metadata", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	setLumeAnswer(&subject, "language_track_metadata", "appropriate")
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("reviewed metadata: %+v", failures)
	}
	setLumeAnswer(&subject, "language_personal_exemption", "language_tier")
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_track_metadata", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	setLumeAnswer(&subject, "language_track_metadata", "appropriate")
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_track_metadata", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	setLumeAnswer(&subject, "language_personal_exemption", "none")
	subject.LanguageFacts.Tracks[0].Default = false
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_track_metadata", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestLumeSilentDiscAndMandatoryBoundaries(t *testing.T) {
	t.Parallel()
	subject := lumeLanguageSubject(nil)
	subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "zxx",
		AudioAbsent:           true,
		TrackCoverageComplete: true,
	})
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("no spoken or signed language: %+v", failures)
	}
	subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
		OriginalLanguage:      "French",
		AudioAbsent:           true,
		TrackCoverageComplete: true,
	})
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_subtitles", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject = lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("English", api.AudioRoleProgramme)})
	subject.PersonalRelease = true
	setLumeAnswer(&subject, "language_personal_exemption", "previously_uploaded")
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_original", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.LanguageFacts.SubtitleLanguages = nil
	subject.LanguageFacts.FullSubtitleLanguages = nil
	setLumeAnswer(&subject, "language_personal_exemption", "previously_uploaded")
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_subtitles", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.Type = "REMUX"
	subject.DiscType = "BDMV"
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_original", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	subject.Type = "DISC"
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("full disc assessed: %+v", failures)
	}
}

func lumeAudio(language string, role api.AudioTrackRole) api.MediaTrackFacts {
	return api.MediaTrackFacts{
		Kind:       api.MediaTrackAudio,
		Languages:  []string{language},
		Role:       role,
		Title:      language + " " + string(role),
		Commentary: role == api.AudioRoleCommentary,
	}
}

func lumeLanguageSubject(tracks []api.MediaTrackFacts) api.TrackerValidationSubject {
	tracks = slices.Clone(tracks)
	for i := range tracks {
		tracks[i].ID = string(rune('a' + i))
		tracks[i].Default = i == 0
		tracks[i].StreamOrder = i
		tracks[i].StreamOrderKnown = true
	}
	tracks = append(tracks, api.MediaTrackFacts{
		ID:        "english-subtitles",
		Kind:      api.MediaTrackSubtitle,
		Languages: []string{"English"},
		Title:     "Complete dialogue",
		Default:   true,
	})
	return api.TrackerValidationSubject{
		Tracker: "LUME",
		Type:    "WEBDL",
		LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "Japanese",
			Tracks:                tracks,
			TrackCoverageComplete: true,
			SubtitleLanguages:     []string{"English"},
		}),
	}
}

func setLumeAnswer(subject *api.TrackerValidationSubject, key, value string) {
	if subject.QuestionnaireAnswers == nil {
		subject.QuestionnaireAnswers = map[string]string{}
	}
	if key == "language_track_metadata" {
		subject.QuestionnaireAnswers[trackMetadataQuestionKey(*subject)] = value
		return
	}
	subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(*subject, key)] = value
}

func TestLumeQuestionnaireRecommendationScope(t *testing.T) {
	t.Parallel()
	subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme)})
	meta := api.UploadSubject{LanguageFacts: subject.LanguageFacts, Type: "WEBDL"}
	if q := languageQuestionnaire(trackers.PreparationInput{Meta: meta}); q != nil {
		t.Fatalf("ordinary release required review: %+v", q)
	}
	meta.PersonalRelease = true
	q := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if q == nil {
		t.Fatal("personal metadata review missing")
	}
	if !slices.ContainsFunc(q.Fields, func(field api.TrackerQuestionnaireField) bool {
		return strings.HasPrefix(field.Key, "language_personal_exemption_")
	}) {
		t.Fatalf("missing exemption: %+v", q)
	}
	subject = api.NewTrackerValidationSubject(meta, "LUME")
	meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"LUME": {trackers.LanguageQuestionKey(subject, "language_personal_exemption"): "none"}}
	q = languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if q == nil || !slices.ContainsFunc(q.Fields, func(field api.TrackerQuestionnaireField) bool {
		return strings.HasPrefix(field.Key, "language_track_metadata_") && field.Required
	}) {
		t.Fatalf("missing required metadata review: %+v", q)
	}
	meta.TrackerQuestionnaireAnswers["LUME"][trackers.LanguageQuestionKey(subject, "language_personal_exemption")] = "trash_tier"
	q = languageQuestionnaire(trackers.PreparationInput{Meta: meta})
	if q != nil && slices.ContainsFunc(q.Fields, func(field api.TrackerQuestionnaireField) bool {
		return strings.HasPrefix(field.Key, "language_track_metadata_")
	}) {
		t.Fatalf("exempt release required metadata review: %+v", q)
	}
	meta.Type = "DISC"
	if q := languageQuestionnaire(trackers.PreparationInput{Meta: meta}); q != nil {
		t.Fatalf("full disc required review: %+v", q)
	}
}

func TestLumeDoesNotInterpretTitleClassificationAsFlags(t *testing.T) {
	t.Parallel()
	tracks := []api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("Japanese", api.AudioRoleCompatibility)}
	// The producer's legacy Commentary classification also covers compatibility.
	tracks[1].Commentary = true
	subject := lumeLanguageSubject(tracks)
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("compatibility title treated as a flag violation: %+v", failures)
	}
}

func TestLumeMultilingualOriginalAndDubOrder(t *testing.T) {
	t.Parallel()
	subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("French", api.AudioRoleProgramme), lumeAudio("English", api.AudioRoleProgramme)})
	subject.LanguageFacts.OriginalLanguages = []string{"Japanese", "French"}
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("multiple originals rejected: %+v", failures)
	}
	subject = lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("English", api.AudioRoleProgramme)})
	subject.LanguageFacts.Tracks[1].Languages = []string{"German", "English"}
	if failures := languageAssessment(subject); len(failures) != 0 {
		t.Fatalf("one multilingual dub is not two reorderable tracks: %+v", failures)
	}
	spanish := lumeAudio("Spanish", api.AudioRoleProgramme)
	spanish.StreamOrder, spanish.StreamOrderKnown = len(subject.LanguageFacts.Tracks), true
	subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, spanish)
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_dub_order_evidence", api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
	subject.PersonalRelease = true
	setLumeAnswer(&subject, "language_personal_exemption", "none")
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_dub_order_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
}

func TestLumePersonalExemptionsPreserveMandatoryLanguages(t *testing.T) {
	t.Parallel()
	for _, exemption := range []string{"trash_tier", "previously_uploaded", "anime_tier"} {
		t.Run(exemption, func(t *testing.T) {
			subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("English", api.AudioRoleProgramme)})
			subject.PersonalRelease = true
			subject.Anime = exemption == "anime_tier"
			subject.LanguageFacts.SubtitleLanguages = nil
			subject.LanguageFacts.FullSubtitleLanguages = nil
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
				ID:        "unassigned",
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleCommentary,
				Title:     "Commentary",
				Languages: []string{"mul"},
			})
			setLumeAnswer(&subject, "language_personal_exemption", exemption)
			if personalRecommendationOutcome(subject) != trackers.LanguageAdvisory {
				t.Fatal("fixture exemption not current")
			}
			failures := languageAssessment(subject)
			requireLUMEValidationFailure(t, failures, "language_original", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
			requireLUMEValidationFailure(t, failures, "language_subtitles", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
			requireLUMEValidationFailure(t, failures, "language_track_language", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
		})
	}
}

func TestLumeUsesMeasuredContainerOrder(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		languages []string
		want      string
	}{
		{name: "compliant container with reversed document order", languages: []string{"Japanese", "English", "German"}},
		{
name: "original after dub",
 languages: []string{"English", "Japanese"},
 want: "language_original_order",
},
		{
name: "English dub after German",
 languages: []string{"Japanese", "German", "English"},
 want: "language_dub_order",
},
	} {
		t.Run(test.name, func(t *testing.T) {
			audio := make([]api.MediaTrackFacts, 0, len(test.languages))
			for _, language := range test.languages {
				audio = append(audio, lumeAudio(language, api.AudioRoleProgramme))
			}
			subject := lumeLanguageSubject(audio)
			slices.Reverse(subject.LanguageFacts.Tracks)
			before := subject.LanguageFacts.Clone()
			failures := languageAssessment(subject)
			if test.want != "" {
				requireLUMEValidationFailure(t, failures, test.want, api.RuleDispositionAdvisory, api.MetadataEvidenceStatusComplete)
			} else if len(failures) != 0 {
				t.Fatalf("container order ignored: %+v", failures)
			}
			if !reflect.DeepEqual(before, subject.LanguageFacts) {
				t.Fatal("assessment changed canonical track order")
			}
		})
	}
}

func TestLumeUnknownContainerOrderPreservesRecommendationLevel(t *testing.T) {
	t.Parallel()
	for _, evidence := range []string{"missing", "negative", "duplicate"} {
		for _, exemption := range []string{"ordinary", "", "none", "trash_tier", "previously_uploaded", "anime_tier"} {
			t.Run(evidence+"/"+exemption, func(t *testing.T) {
				subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("English", api.AudioRoleProgramme), lumeAudio("Japanese", api.AudioRoleProgramme)})
				switch evidence {
				case "missing":
					subject.LanguageFacts.Tracks[0].StreamOrderKnown = false
				case "negative":
					subject.LanguageFacts.Tracks[0].StreamOrder = -1
				case "duplicate":
					subject.LanguageFacts.Tracks[0].StreamOrder = subject.LanguageFacts.Tracks[1].StreamOrder
				}
				subject.PersonalRelease = exemption != "ordinary"
				subject.Anime = exemption == "anime_tier"
				setLumeAnswer(&subject, "language_personal_exemption", exemption)
				setLumeAnswer(&subject, "language_track_metadata", "appropriate")
				want := api.RuleDispositionAdvisory
				if exemption == "" || exemption == "none" {
					want = api.RuleDispositionStrict
				}
				failures := languageAssessment(subject)
				requireLUMEValidationFailure(t, failures, "language_track_order_evidence", want, api.MetadataEvidenceStatusPartial)
				for _, failure := range failures {
					if failure.Rule == "language_original_order" || failure.Rule == "language_dub_order" {
						t.Fatalf("unmeasured order became a violation: %+v", failures)
					}
				}
				for _, disc := range []string{"BDMV", "DVD", "UHD"} {
					subject.Type, subject.DiscType = "DISC", disc
					if failures := languageAssessment(subject); len(failures) != 0 {
						t.Fatalf("full disc order assessed: %+v", failures)
					}
				}
			})
		}
	}
}

func TestLumeOrderAttestationIsBoundedToMissingEvidence(t *testing.T) {
	t.Parallel()
	subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("English", api.AudioRoleProgramme)})
	subject.PersonalRelease = true
	subject.LanguageFacts.Tracks[1].StreamOrderKnown = false
	setLumeAnswer(&subject, "language_personal_exemption", "none")
	setLumeAnswer(&subject, "language_track_metadata", "appropriate")
	key := trackOrderQuestionKey(subject)
	for _, answer := range []string{"", "unresolved", "out_of_order", "ordered"} {
		subject.QuestionnaireAnswers[key] = answer
		failures := languageAssessment(subject)
		switch answer {
		case "ordered":
			if len(failures) != 0 {
				t.Fatalf("bounded order evidence not accepted: %+v", failures)
			}
		case "out_of_order":
			requireLUMEValidationFailure(t, failures, "language_track_order", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
		default:
			requireLUMEValidationFailure(t, failures, "language_track_order_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
		}
	}
	// A change in measured order invalidates the exact-evidence answer even when
	// the other track remains unmeasured.
	subject.LanguageFacts.Tracks[0].StreamOrder++
	setLumeAnswer(&subject, "language_personal_exemption", "none")
	setLumeAnswer(&subject, "language_track_metadata", "appropriate")
	if key == trackOrderQuestionKey(subject) {
		t.Fatal("order answer survived changed stream evidence")
	}
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_track_order_evidence", api.RuleDispositionStrict, api.MetadataEvidenceStatusPartial)
	subject.QuestionnaireAnswers[trackOrderQuestionKey(subject)] = "ordered"
	subject.LanguageFacts.Tracks[0].Title = ""
	setLumeAnswer(&subject, "language_personal_exemption", "none")
	setLumeAnswer(&subject, "language_track_metadata", "appropriate")
	subject.QuestionnaireAnswers[trackOrderQuestionKey(subject)] = "ordered"
	requireLUMEValidationFailure(t, languageAssessment(subject), "language_track_titles", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
}

func TestLumeOrderAttestationCannotOverrideMeasuredViolations(t *testing.T) {
	t.Parallel()
	for _, extraUnknown := range []bool{false, true} {
		subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("English", api.AudioRoleProgramme), lumeAudio("Japanese", api.AudioRoleProgramme)})
		if extraUnknown {
			subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, lumeAudio("German", api.AudioRoleProgramme))
		}
		subject.PersonalRelease = true
		setLumeAnswer(&subject, "language_personal_exemption", "none")
		setLumeAnswer(&subject, "language_track_metadata", "appropriate")
		subject.QuestionnaireAnswers[trackOrderQuestionKey(subject)] = "ordered"
		requireLUMEValidationFailure(t, languageAssessment(subject), "language_original_order", api.RuleDispositionStrict, api.MetadataEvidenceStatusComplete)
	}
}

func TestLumeOrderQuestionRequiresPersonalMissingEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, exemption               string
		personal, missing, disc, want bool
	}{
		{name: "ordinary missing", missing: true},
		{
name: "unresolved personal exemption",
 personal: true,
 missing: true,
},
		{
name: "personal measured",
 personal: true,
 exemption: "none",
},
		{
name: "personal missing",
 personal: true,
 missing: true,
 exemption: "none",
 want: true,
},
		{
name: "personal exempt",
 personal: true,
 missing: true,
 exemption: "trash_tier",
},
		{
name: "older release exempt",
 personal: true,
 missing: true,
 exemption: "previously_uploaded",
},
		{
name: "full disc",
 personal: true,
 missing: true,
 exemption: "none",
 disc: true,
},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := lumeLanguageSubject([]api.MediaTrackFacts{lumeAudio("Japanese", api.AudioRoleProgramme), lumeAudio("English", api.AudioRoleProgramme)})
			subject.LanguageFacts.Tracks[1].StreamOrderKnown = !test.missing
			meta := api.UploadSubject{
LanguageFacts: subject.LanguageFacts,
 Type: "WEBDL",
 PersonalRelease: test.personal,
}
			if test.disc {
				meta.Type = "DISC"
			}
			subject = api.NewTrackerValidationSubject(meta, "LUME")
			meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"LUME": {trackers.LanguageQuestionKey(subject, "language_personal_exemption"): test.exemption}}
			q := languageQuestionnaire(trackers.PreparationInput{Meta: meta})
			found := false
			if q != nil {
				for _, field := range q.Fields {
					if !strings.HasPrefix(field.Key, "language_track_order_") {
						continue
					}
					found = true
					if !field.Required || !slices.Equal(field.Options, []string{"ordered", "out_of_order", "unresolved"}) {
						t.Fatalf("incorrect bounded order question: %+v", field)
					}
				}
			}
			if found != test.want {
				t.Fatalf("order question = %t, want %t: %+v", found, test.want, q)
			}
		})
	}
}
