// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUnknownOriginalCLIReviewDoesNotReadInput(t *testing.T) {
	registry := impl.MustNewRegistry()
	for _, mode := range []api.InteractionMode{api.InteractionModeInteractive, api.InteractionModeUnattended, api.InteractionModeUnattendedConfirm} {
		for _, languages := range [][]string{{"English"}, nil, {"und"}, {"mul"}, {"Japanese", "English"}} {
			t.Run(fmt.Sprintf("%s/%v", mode, languages), func(t *testing.T) {
				media := api.MediaFacts{TrackCoverageComplete: true, Tracks: []api.MediaTrackFacts{{
					ID:                  "programme",
					Kind:                api.MediaTrackAudio,
					Role:                api.AudioRoleProgramme,
					ManifestFingerprint: "report",
					Languages:           languages,
				}}}
				readiness, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{"AITHER", "BHD"}, api.UploadSubject{LanguageFacts: mediafacts.ResolveLanguages(media)})
				if err != nil {
					t.Fatal(err)
				}
				reader := bufio.NewReader(cliNoInput{t})
				var out bytes.Buffer
				corrections, err := collectCLIInputLanguages(reader, &out, mode, readiness.Fields, "Example.Release.mkv", media)
				if err != nil || len(corrections) != 0 {
					t.Fatalf("language corrections=%#v err=%v", corrections, err)
				}
				origins, err := collectCLIInputAudioOrigins(reader, &out, mode, readiness.Fields, "Example.Release.mkv", media)
				if err != nil || len(origins) != 0 || out.Len() != 0 {
					t.Fatalf("origins=%#v err=%v output=%s", origins, err, out.String())
				}
			})
		}
	}
}
