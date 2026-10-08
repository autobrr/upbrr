// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/aither"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/hhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lst"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ulcx"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackersResolveStandaloneDolbyAudioFromMediaInfo(t *testing.T) {
	t.Parallel()
	for _, profile := range []unit3d.Profile{aither.Profile(), hhd.Profile(), lst.Profile(), ulcx.Profile()} {
		for _, companionFirst := range []bool{false, true} {
			for _, labelled := range []bool{false, true} {
				for _, codec := range []string{"AC-3", "DD", "DD+", "DDP", "E-AC3", "E-AC-3"} {
					t.Run(fmt.Sprintf("%s %s first=%t labelled=%t", profile.Name, codec, companionFirst, labelled), func(t *testing.T) {
						meta, companion := standaloneDolbySubject(t, codec, companionFirst, labelled)
						before := meta.LanguageFacts.Clone()
						question := profile.Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta})
						if question != nil {
							t.Fatalf("clear inspected relationship still asks a question: %+v", question)
						}
						failures, err := profile.ValidationPolicy.Check(t.Context(), api.NewTrackerValidationSubject(meta, profile.Name), api.NopLogger{})
						if err != nil {
							t.Fatal(err)
						}
						blocked := false
						guidance := false
						for _, failure := range failures {
							if !strings.HasPrefix(failure.Rule, "language_compatibility_") {
								continue
							}
							blocked = blocked || trackers.RuleFailureBlocksExecution(failure, api.WorkflowExecutionModeNormal, false)
							guidance = guidance || failure.Disposition == api.RuleDispositionAdvisory && failure.EvidenceFingerprint == ""
						}
						wantBlocked := profile.Name == "HHD" && companion.Codec == "DD+"
						if blocked != wantBlocked {
							t.Fatalf("compatibility blocked=%t want=%t: %+v", blocked, wantBlocked, failures)
						}
						if profile.Name == "LST" && companion.Codec == "DD+" && !guidance {
							t.Fatal("LST DD+ substitute lost passive guidance")
						}
						registry := trackers.NewRegistry()
						if err := registry.Register(unit3d.NewWithProfile(profile)); err != nil {
							t.Fatal(err)
						}
						readiness, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{api.TrackerID(profile.Name)}, meta)
						if err != nil {
							t.Fatal(err)
						}
						for _, schema := range readiness.TrackerQuestionnaires {
							if slices.ContainsFunc(schema.Fields, func(field api.TrackerQuestionnaireField) bool {
								return strings.HasPrefix(field.Key, "compatibility_mix_")
							}) {
								t.Fatal("shared CLI/WebUI readiness retained a compatibility question")
							}
						}
						if !reflect.DeepEqual(before, meta.LanguageFacts) {
							t.Fatal("automatic association changed canonical primary or role facts")
						}
					})
				}
			}
		}
	}
}

func standaloneDolbySubject(t *testing.T, codec string, companionFirst, labelled bool) (api.UploadSubject, api.MediaTrackFacts) {
	t.Helper()
	title := codec + " 5.1-EX"
	if labelled {
		title = "Compatibility " + title
	}
	doc := mediaInfoDoc{}
	doc.Media.Track = []map[string]any{
		{
			"@type":             "Audio",
			"ID":                "1",
			"StreamOrder":       "1",
			"Format":            "MLP FBA",
			"CodecID":           "A_TRUEHD",
			"Format_Commercial": "Dolby TrueHD with Dolby Atmos",
			"Title":             "TrueHD Atmos 7.1",
			"Language":          "en",
			"Channels":          "8",
			"Default":           "Yes",
			"ServiceKind":       "CM",
		},
		{
			"@type":       "Audio",
			"ID":          "2",
			"StreamOrder": "2",
			"Format":      codec,
			"Title":       title,
			"Language":    "en",
			"Channels":    "6",
			"Default":     "No",
			"ServiceKind": "CM",
		},
	}
	if companionFirst {
		doc.Media.Track[0]["ID"], doc.Media.Track[0]["StreamOrder"] = "2", "2"
		doc.Media.Track[1]["ID"], doc.Media.Track[1]["StreamOrder"] = "1", "1"
		slices.Reverse(doc.Media.Track)
	}
	tracks, primaryID, _, _, err := mediaTrackFacts(preparationstate.State{SourcePath: "Example.Movie.2026.mkv", VideoPath: "Example.Movie.2026.mkv"}, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("unexpected inspected audio: %+v", tracks)
	}
	mainTrack, companion := tracks[0], tracks[1]
	if companionFirst {
		mainTrack, companion = tracks[1], tracks[0]
	}
	wantRole, wantPrimary := api.AudioRoleProgramme, tracks[0].ID
	if labelled {
		wantRole, wantPrimary = api.AudioRoleCompatibility, mainTrack.ID
	}
	if companion.Role != wantRole || (companion.Codec != "DD" && companion.Codec != "DD+") || primaryID != wantPrimary || companion.Commentary != labelled {
		t.Fatalf("unexpected inspected audio/primary: %+v primary=%q", tracks, primaryID)
	}
	return api.UploadSubject{
		Type:   "REMUX",
		Source: "BluRay",
		LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
			OriginalLanguage:      "English",
			TrackCoverageComplete: true,
			PrimaryAudioTrackID:   primaryID,
			Tracks:                tracks,
		}),
	}, companion
}
