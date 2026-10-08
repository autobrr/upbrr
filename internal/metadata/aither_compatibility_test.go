// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/aither"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAitherDiscoversStandaloneDolbyAudioFromMediaInfo(t *testing.T) {
	t.Parallel()
	for _, companionFirst := range []bool{false, true} {
		for _, codec := range []string{"AC-3", "DD", "DD+", "DDP", "E-AC3", "E-AC-3"} {
			t.Run(fmt.Sprintf("%s/companion-first=%t", codec, companionFirst), func(t *testing.T) {
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
						"Title":       codec + " 5.1-EX",
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
				mainTrack, companionTrack := tracks[0], tracks[1]
				if companionFirst {
					mainTrack, companionTrack = tracks[1], tracks[0]
				}
				if companionTrack.Role != api.AudioRoleProgramme || (companionTrack.Codec != "DD" && companionTrack.Codec != "DD+") || mainTrack.ID == companionTrack.ID || primaryID != tracks[0].ID {
					t.Fatalf("unexpected inspected audio or primary selection: %+v primary=%q", tracks, primaryID)
				}
				meta := api.UploadSubject{
					Type: "REMUX",
					LanguageFacts: mediafacts.ResolveLanguages(api.MediaFacts{
						OriginalLanguage:      "English",
						TrackCoverageComplete: true,
						PrimaryAudioTrackID:   primaryID,
						Tracks:                tracks,
					}),
				}
				profile := aither.Profile()
				question := profile.Site.ProjectionQuestionnaire(trackers.PreparationInput{Meta: meta})
				if question == nil || len(question.Fields) != 1 || !question.Fields[0].Required || !slices.Contains(question.Fields[0].Options, mainTrack.ID) {
					t.Fatalf("ordinary Dolby track has no association review: %+v", question)
				}
				check := func() []api.RuleFailure {
					t.Helper()
					failures, err := profile.ValidationPolicy.Check(t.Context(), api.NewTrackerValidationSubject(meta, "AITHER"), nil)
					if err != nil {
						t.Fatal(err)
					}
					return failures
				}
				if failures := check(); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool {
					return f.Rule == "language_compatibility_missing" && f.EvidenceStatus == api.MetadataEvidenceStatusPartial && strings.HasPrefix(f.Reason, "Unresolved")
				}) {
					t.Fatalf("available candidate was declared absent: %+v", failures)
				}
				meta.TrackerQuestionnaireAnswers = map[string]map[string]string{"AITHER": {question.Fields[0].Key: mainTrack.ID}}
				for _, failure := range check() {
					if strings.HasPrefix(failure.Rule, "language_") {
						t.Fatalf("confirmed companion retains a language finding: %+v", failure)
					}
				}
				if meta.LanguageFacts.PrimaryAudioTrackID != primaryID || !slices.ContainsFunc(meta.LanguageFacts.Tracks, func(track api.MediaTrackFacts) bool {
					return track.ID == companionTrack.ID && track.Role == api.AudioRoleProgramme
				}) {
					t.Fatal("tracker review mutated canonical programme role")
				}
				meta.Identity.Generation++
				if failures := check(); !slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "language_compatibility_mix" }) {
					t.Fatalf("stale association was reused: %+v", failures)
				}
			})
		}
	}
}
