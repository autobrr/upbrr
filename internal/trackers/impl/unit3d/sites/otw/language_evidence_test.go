// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package otw

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestOTWLanguageNamingEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		original   string
		languages  []string
		mutate     func(*api.MediaFacts)
		unresolved bool
		marker     string
	}{
		{
			name:      "complete original English",
			original:  "English",
			languages: []string{"English"},
		},
		{
			name:      "complete dubbed",
			original:  "Japanese",
			languages: []string{"English"},
			marker:    "Dubbed ",
		},
		{
			name:      "complete dual",
			original:  "Japanese",
			languages: []string{"Japanese", "English"},
			marker:    "Dual Audio ",
		},
		{
			name:      "complete two dubs without original audio",
			original:  "Japanese",
			languages: []string{"English", "German"},
		},
		{
			name:      "complete multi without original",
			languages: []string{"English", "French", "German"},
			marker:    "MULTI ",
		},
		{name: "one non English without original", languages: []string{"French"}},
		{name: "two non English without original", languages: []string{"French", "German"}},
		{
			name:       "English without original",
			languages:  []string{"English"},
			unresolved: true,
		},
		{
			name:       "English and other without original",
			languages:  []string{"English", "Japanese"},
			unresolved: true,
		},
		{
			name:       "incomplete programme coverage",
			original:   "Japanese",
			languages:  []string{"Japanese", "English"},
			unresolved: true,
			mutate:     func(media *api.MediaFacts) { media.TrackCoverageComplete = false },
		},
		{
			name:       "missing programme role",
			original:   "Japanese",
			languages:  []string{"English", "Japanese"},
			unresolved: true,
			mutate:     func(media *api.MediaFacts) { media.Tracks[1].Role = "" },
		},
		{
			name:       "missing programme language",
			original:   "Japanese",
			languages:  []string{"English", "Japanese"},
			unresolved: true,
			mutate:     func(media *api.MediaFacts) { media.Tracks[1].Languages = nil },
		},
		{
			name:       "contradictory programme correction",
			original:   "Japanese",
			languages:  []string{"Japanese", "English"},
			unresolved: true,
			mutate: func(media *api.MediaFacts) {
				media.AudioLanguages = []string{"English"}
				media.AudioLanguagesProvenance = api.FactProvenanceManual
			},
		},
		{
			name:       "contradictory multi correction",
			languages:  []string{"English"},
			unresolved: true,
			mutate: func(media *api.MediaFacts) {
				media.AudioLanguages = []string{"English", "French", "German"}
				media.AudioLanguagesProvenance = api.FactProvenanceManual
			},
		},
		{
			name:       "cleared programme correction",
			original:   "Japanese",
			languages:  []string{"Japanese", "English"},
			unresolved: true,
			mutate: func(media *api.MediaFacts) {
				media.AudioLanguages = nil
				media.AudioLanguagesProvenance = api.FactProvenanceManualEmpty
			},
		},
		{
			name:       "cleared original correction",
			original:   "Japanese",
			languages:  []string{"English"},
			unresolved: true,
			mutate:     func(media *api.MediaFacts) { media.OriginalLanguage = "" },
		},
		{
			name:      "partial multi without original",
			languages: []string{"English", "French", "German"},
			marker:    "MULTI ",
			mutate:    func(media *api.MediaFacts) { media.TrackCoverageComplete = false },
		},
		{
			name:      "known multi plus unknown role",
			languages: []string{"English", "French", "German", "Japanese"},
			marker:    "MULTI ",
			mutate:    func(media *api.MediaFacts) { media.Tracks[3].Role = "" },
		},
		{
			name:   "inspected absent audio",
			mutate: func(media *api.MediaFacts) { media.AudioAbsent = true },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			media := otwLanguageMedia(test.original, test.languages...)
			if test.mutate != nil {
				test.mutate(&media)
			}
			subject := otwPassingSubject()
			subject.Tracker = "OTW"
			subject.Audio = "DD+ 5.1"
			subject.LanguageFacts = mediafacts.ResolveLanguages(media)
			failures := otwLanguageValidation(t, subject)
			if got := hasOTWValidationRule(failures, "language_naming_evidence"); got != test.unresolved {
				t.Fatalf("unresolved = %t, want %t: %#v", got, test.unresolved, failures)
			}
			if hasOTWValidationRule(failures, "language_english_accessibility") {
				t.Fatalf("independent English subtitles did not retain accessibility: %#v", failures)
			}
			for _, failure := range failures {
				if failure.Rule != "language_naming_evidence" {
					continue
				}
				if failure.Disposition != api.RuleDispositionStrict || failure.EvidenceStatus != api.MetadataEvidenceStatusPartial || !failure.DebugBypass {
					t.Fatalf("unresolved naming outcome = %#v", failure)
				}
				for _, detail := range []string{"Unresolved", "OTW", "Original:", "programme audio:"} {
					if !strings.Contains(failure.Reason, detail) {
						t.Fatalf("missing %q in contextual naming assessment: %#v", detail, failure)
					}
				}
			}
			if got, want := otwAudio(otwUploadSubject(subject)), test.marker+"DD+ 5.1"; got != want {
				t.Fatalf("audio name = %q, want %q", got, want)
			}
		})
	}
}

func TestOTWLanguageNamingUnavailableEvidenceAndDiscBoundary(t *testing.T) {
	t.Parallel()
	for _, status := range []api.MetadataEvidenceStatus{"", api.MetadataEvidenceStatusUnavailable} {
		for _, kind := range []string{"encode", "canonical disc", "disc type", "nested disc type", "remux", "nested disc remux"} {
			t.Run(string(status)+"/"+kind, func(t *testing.T) {
				t.Parallel()
				subject := otwPassingSubject()
				subject.LanguageFacts = api.LanguageFacts{
					ProgrammeLanguages: []string{"English", "French", "German"},
					ProgrammeStatus:    status,
				}
				switch kind {
				case "canonical disc":
					subject.Type = "DISC"
				case "disc type":
					subject.DiscType = "BDMV"
				case "nested disc type":
					subject.Disc.Type = "BDMV"
				case "remux":
					subject.Type, subject.DiscType = "REMUX", "BDMV"
				case "nested disc remux":
					subject.Type, subject.Disc.Type = "REMUX", "BDMV"
				}
				want := kind == "encode" || kind == "remux" || kind == "nested disc remux"
				if failures := otwLanguageValidation(t, subject); hasOTWValidationRule(failures, "language_naming_evidence") != want {
					t.Fatalf("unresolved naming = %#v, want %t", failures, want)
				}
			})
		}
	}
}

func TestOTWLanguageNamingCompleteInvalidProgrammeFacts(t *testing.T) {
	t.Parallel()
	for _, languages := range [][]string{nil, {"Unknown"}, {"English", "Multiple Languages"}, {"French", "und"}, {"English", "English"}} {
		t.Run(strings.Join(languages, "+"), func(t *testing.T) {
			t.Parallel()
			subject := otwPassingSubject()
			subject.LanguageFacts.ProgrammeLanguages = languages
			if failures := otwLanguageValidation(t, subject); !hasOTWValidationRule(failures, "language_naming_evidence") {
				t.Fatalf("complete status accepted unresolved programme languages: %#v", failures)
			}
		})
	}
}

func TestOTWLanguageNamingPresentationAndDebug(t *testing.T) {
	t.Parallel()
	for _, presentation := range []string{"automatic", "no dub", "no dual", "forced dual", "full manual name"} {
		t.Run(presentation, func(t *testing.T) {
			t.Parallel()
			subject := otwPassingSubject()
			subject.Tracker = "OTW"
			subject.LanguageFacts = mediafacts.ResolveLanguages(otwLanguageMedia("", "English"))
			// English audio independently satisfies the separate accessibility guidance.
			subject.LanguageFacts.SubtitleLanguages = nil
			before := subject.LanguageFacts.Clone()
			switch presentation {
			case "no dub":
				subject.ReleaseNameOverrides.NoDub = new(true)
			case "no dual":
				subject.ReleaseNameOverrides.NoDual = new(true)
			case "forced dual":
				subject.ReleaseNameOverrides.DualAudio = new(true)
			case "full manual name":
				const requested = "Manual Example 2026 Dubbed-GRP"
				prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
					Tracker:             "OTW",
					Meta:                otwUploadSubject(subject),
					RequestedUploadName: new(requested),
				}, unit3d.NewWithProfile(Profile()).ReleaseNamePolicy())
				if failure != nil {
					t.Fatal(failure)
				}
				name, err := prepared.ReviewedUploadName()
				// OTW retains its tracker-owned normalization of requested names.
				if err != nil || name != "Example Release 2026 1080p WEB-DL" {
					t.Fatalf("manual name = %q, err = %v", name, err)
				}
				subject.ReleaseName = name
			}
			failures := otwLanguageValidation(t, subject)
			if len(failures) != 1 || failures[0].Rule != "language_naming_evidence" {
				t.Fatalf("presentation cleared naming facts or changed accessibility: %#v", failures)
			}
			for _, authorized := range []bool{false, true} {
				if !trackers.RuleFailureBlocksExecution(failures[0], api.WorkflowExecutionModeNormal, authorized) ||
					trackers.RuleFailureBlocksExecution(failures[0], api.WorkflowExecutionModeDebug, authorized) {
					t.Fatalf("naming outcome changed normal/debug execution: %#v", failures[0])
				}
			}
			if !reflect.DeepEqual(subject.LanguageFacts, before) {
				t.Fatal("presentation or validation mutated language facts")
			}
			subject.ProviderMetadata.TMDB.Title = ""
			failures = otwLanguageValidation(t, subject)
			for _, failure := range failures {
				if failure.Rule == "otw_naming_metadata" && !trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeDebug, true) {
					t.Fatalf("language debug bypass escaped its finding: %#v", failures)
				}
			}
		})
	}
}

func otwLanguageMedia(original string, languages ...string) api.MediaFacts {
	media := api.MediaFacts{
		OriginalLanguage:      original,
		TrackCoverageComplete: true,
		SubtitleLanguages:     []string{"English"},
	}
	for _, language := range languages {
		media.Tracks = append(media.Tracks, api.MediaTrackFacts{
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Languages: []string{language},
		})
	}
	return media
}

func otwLanguageValidation(t *testing.T, subject api.TrackerValidationSubject) []api.RuleFailure {
	t.Helper()
	failures, err := ValidationPolicy().Check(context.Background(), subject, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	return failures
}
