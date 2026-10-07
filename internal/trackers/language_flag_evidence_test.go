// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestLanguageDefaultEvidenceRequiresKnownWitness(t *testing.T) {
	for _, subtitle := range []bool{false, true} {
		for _, advisory := range []bool{false, true} {
			outcome := LanguageProhibited
			if advisory {
				outcome = LanguageAdvisory
			}
			policy := LanguagePolicy{OriginalDefault: outcome}
			key := "language_original_default"
			track := api.MediaTrackFacts{
				Kind:      api.MediaTrackAudio,
				Role:      api.AudioRoleProgramme,
				Languages: []string{"Japanese"},
			}
			if subtitle {
				policy = LanguagePolicy{SubtitleDefault: outcome}
				key = "language_subtitle_default"
				track.Kind = api.MediaTrackSubtitle
				track.Languages = []string{"English"}
			}
			for _, known := range []bool{false, true} {
				for _, value := range []bool{false, true} {
					track.DefaultKnown = known
					track.Default = value
					subject := api.TrackerValidationSubject{
						Tracker: "SYNTHETIC",
						Type:    "WEBDL",
						LanguageFacts: api.LanguageFacts{
							OriginalLanguages:      []string{"Japanese"},
							OriginalLanguagesKnown: true,
							ProgrammeLanguages:     []string{"Japanese"},
							ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
							SubtitleLanguages:      []string{"English"},
							SubtitleStatus:         api.MetadataEvidenceStatusComplete,
							Tracks:                 []api.MediaTrackFacts{track},
						},
					}
					failures := EvaluateLanguagePolicy(subject, policy)
					index := slices.IndexFunc(failures, func(f api.RuleFailure) bool { return f.Rule == key })
					if known && value {
						if index >= 0 {
							t.Fatalf("known witness rejected: %+v", failures)
						}
						continue
					}
					if index < 0 {
						t.Fatalf("unknown/false default silently accepted: subtitle=%t known=%t value=%t", subtitle, known, value)
					}
					f := failures[index]
					if !known && f.EvidenceStatus != api.MetadataEvidenceStatusPartial {
						t.Fatalf("unknown flag treated as known: %+v", f)
					}
					if advisory && f.Disposition != api.RuleDispositionAdvisory {
						t.Fatalf("guidance became strict: %+v", f)
					}
				}
			}
		}
	}
}

func TestLanguageDefaultKnownWitnessSurvivesUnknownCandidate(t *testing.T) {
	for _, subtitle := range []bool{false, true} {
		track := api.MediaTrackFacts{
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Languages:    []string{"Japanese"},
			Default:      true,
			DefaultKnown: true,
		}
		policy := LanguagePolicy{OriginalDefault: LanguageProhibited}
		if subtitle {
			track.Kind = api.MediaTrackSubtitle
			track.Languages = []string{"English"}
			policy = LanguagePolicy{SubtitleDefault: LanguageProhibited}
		}
		unknown := track
		unknown.DefaultKnown = false
		subject := api.TrackerValidationSubject{Type: "WEBDL", LanguageFacts: api.LanguageFacts{
			OriginalLanguages:      []string{"Japanese"},
			OriginalLanguagesKnown: true,
			ProgrammeLanguages:     []string{"Japanese"},
			ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
			SubtitleLanguages:      []string{"English"},
			SubtitleStatus:         api.MetadataEvidenceStatusComplete,
			Tracks:                 []api.MediaTrackFacts{unknown, track},
		}}
		if failures := EvaluateLanguagePolicy(subject, policy); len(failures) != 0 {
			t.Fatalf("known existential witness lost: %+v", failures)
		}
	}
}

func TestOriginalAudioOrderUsesMeasuredEvidence(t *testing.T) {
	for _, test := range []struct {
		name                    string
		originalOrder, dubOrder int
		originalKnown, dubKnown bool
		reverse                 bool
		want                    api.MetadataEvidenceStatus
	}{
		{"measured original first", 1, 2, true, true, true, ""},
		{"measured dub first", 2, 1, true, true, false, api.MetadataEvidenceStatusComplete},
		{"unknown original order", 0, 1, false, true, false, api.MetadataEvidenceStatusPartial},
		{"unknown dub order", 1, 0, true, false, false, api.MetadataEvidenceStatusPartial},
		{"duplicate order", 1, 1, true, true, false, api.MetadataEvidenceStatusPartial},
		{"negative order", -1, 2, true, true, false, api.MetadataEvidenceStatusPartial},
	} {
		for _, advisory := range []bool{false, true} {
			t.Run(test.name, func(t *testing.T) {
				original := api.MediaTrackFacts{
					Kind:             api.MediaTrackAudio,
					Role:             api.AudioRoleProgramme,
					Languages:        []string{"Japanese"},
					StreamOrder:      test.originalOrder,
					StreamOrderKnown: test.originalKnown,
				}
				dub := api.MediaTrackFacts{
					Kind:             api.MediaTrackAudio,
					Role:             api.AudioRoleProgramme,
					Languages:        []string{"English"},
					StreamOrder:      test.dubOrder,
					StreamOrderKnown: test.dubKnown,
				}
				tracks := []api.MediaTrackFacts{original, dub}
				if test.reverse {
					slices.Reverse(tracks)
				}
				subject := api.TrackerValidationSubject{Type: "WEBDL", LanguageFacts: api.LanguageFacts{
					OriginalLanguages:      []string{"Japanese"},
					OriginalLanguagesKnown: true,
					ProgrammeLanguages:     []string{"Japanese", "English"},
					ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
					Tracks:                 tracks,
				}}
				outcome := LanguageProhibited
				if advisory {
					outcome = LanguageAdvisory
				}
				failures := EvaluateLanguagePolicy(subject, LanguagePolicy{OriginalFirst: outcome})
				if test.want == "" {
					if len(failures) != 0 {
						t.Fatalf("valid measured order rejected: %+v", failures)
					}
					return
				}
				if len(failures) != 1 || failures[0].EvidenceStatus != test.want {
					t.Fatalf("order evidence misclassified: %+v", failures)
				}
				if advisory && failures[0].Disposition != api.RuleDispositionAdvisory {
					t.Fatalf("guidance became strict: %+v", failures)
				}
				if test.want == api.MetadataEvidenceStatusComplete {
					subject.LanguageFacts.Tracks = append(subject.LanguageFacts.Tracks, api.MediaTrackFacts{
						Kind:      api.MediaTrackAudio,
						Role:      api.AudioRoleAlternateMix,
						Languages: []string{"Japanese"},
					})
					failures = EvaluateLanguagePolicy(subject, LanguagePolicy{OriginalFirst: outcome})
					if len(failures) != 1 || failures[0].EvidenceStatus != api.MetadataEvidenceStatusPartial {
						t.Fatalf("unknown original could precede known dub: %+v", failures)
					}
				}
			})
		}
	}
}

func TestDefaultAudioNameRejectsUnknownContender(t *testing.T) {
	for _, truthy := range []bool{false, true} {
		doc := &api.ReleaseNameDocument{Version: api.ReleaseNameDocumentVersionV1, Components: []api.ReleaseNameComponent{{
			Role:    api.NameRoleAudio,
			Value:   "AAC 2.0",
			Present: true,
		}}}
		facts := api.LanguageFacts{Tracks: []api.MediaTrackFacts{{
			Kind:         api.MediaTrackAudio,
			Default:      true,
			DefaultKnown: true,
			Codec:        "FLAC",
			AudioLabel:   "FLAC 2.0",
		}, {
			Kind:       api.MediaTrackAudio,
			Role:       api.AudioRoleCommentary,
			Default:    truthy,
			Codec:      "AAC",
			AudioLabel: "AAC 2.0",
		}}}
		if err := ApplyDefaultAudioName(&NameEditor{document: doc}, api.UploadSubject{Type: "WEBDL", LanguageFacts: facts}); err == nil {
			t.Fatal("unknown secondary default accepted as unique")
		}
		facts.Tracks[1].Default = false
		facts.Tracks[1].DefaultKnown = true
		if err := ApplyDefaultAudioName(&NameEditor{document: doc}, api.UploadSubject{Type: "WEBDL", LanguageFacts: facts}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOriginalFirstDoesNotRequireEveryOriginalBeforeDubs(t *testing.T) {
	original := api.MediaTrackFacts{
		Kind:             api.MediaTrackAudio,
		Role:             api.AudioRoleProgramme,
		Languages:        []string{"Japanese"},
		StreamOrder:      1,
		StreamOrderKnown: true,
	}
	dub := api.MediaTrackFacts{
		Kind:             api.MediaTrackAudio,
		Role:             api.AudioRoleProgramme,
		Languages:        []string{"English"},
		StreamOrder:      2,
		StreamOrderKnown: true,
	}
	alternate := original
	alternate.Role = api.AudioRoleAlternateMix
	alternate.StreamOrder = 3
	subject := api.TrackerValidationSubject{Type: "WEBDL", LanguageFacts: api.LanguageFacts{
		OriginalLanguages:      []string{"Japanese"},
		OriginalLanguagesKnown: true,
		ProgrammeLanguages:     []string{"Japanese", "English"},
		ProgrammeStatus:        api.MetadataEvidenceStatusComplete,
	}}
	for _, test := range []struct {
		name   string
		tracks []api.MediaTrackFacts
		want   api.MetadataEvidenceStatus
	}{
		{"original dub original", []api.MediaTrackFacts{original, dub, alternate}, ""},
		{"unknown extra original", []api.MediaTrackFacts{original, dub, {
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleAlternateMix,
			Languages: []string{"Japanese"},
		}}, ""},
		{"known earliest dub plus unknown dub", []api.MediaTrackFacts{alternate, dub, {
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Languages: []string{"English"},
		}}, api.MetadataEvidenceStatusComplete},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject.LanguageFacts.Tracks = test.tracks
			f := EvaluateLanguagePolicy(subject, LanguagePolicy{OriginalFirst: LanguageProhibited})
			if test.want == "" {
				if len(f) != 0 {
					t.Fatalf("first original rejected: %+v", f)
				}
				return
			}
			if len(f) != 1 || f[0].EvidenceStatus != test.want {
				t.Fatalf("wrong first evidence: %+v", f)
			}
		})
	}
}
