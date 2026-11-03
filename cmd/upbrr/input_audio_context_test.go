// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPlainPromptShowsEvidenceAndHelp(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		var output bytes.Buffer
		reader := bufio.NewReader(strings.NewReader("unresolved\n"))
		var writer io.Writer = &output
		if wrapped {
			writer = &cliPresentationWriter{
				Writer:    &output,
				ctx:       t.Context(),
				presenter: &cliPlainPresenter{reader: reader, output: &output},
			}
		}
		answer, err := askCLIField(reader, writer, cliPrompt{
			Question: "Soundtrack origin: ",
			Evidence: "Source: Example.Release.mkv; title original language: Japanese",
			Help:     "Enter original, dubbed, or unresolved.",
		})
		text := output.String()
		evidenceAt := strings.Index(text, "Source: Example.Release.mkv")
		helpAt := strings.Index(text, "Enter original, dubbed, or unresolved.")
		questionAt := strings.Index(text, "Soundtrack origin: ")
		if err != nil || answer != "unresolved" || evidenceAt < 0 || helpAt <= evidenceAt || questionAt <= helpAt {
			t.Fatalf("wrapped=%t answer=%q err=%v output=%q", wrapped, answer, err, output.String())
		}
	}
}

func TestSoundtrackContextSanitizesSourceResourceAndTrackersInPlainAndTUI(t *testing.T) {
	hostile := "\n\x1b]52;c;CLIPBOARD\a\x1b[2Jpassword=SECRET_SENTINEL"
	track := api.MediaTrackFacts{
		ID:         "track_example",
		ResourceID: "resource",
		Title:      "Main" + hostile,
	}
	media := api.MediaFacts{MediaFileFacts: api.MediaFileFacts{Files: []api.MediaFileFact{{ResourceID: "resource", FileName: "Resource" + hostile}}}}
	field := api.InputReadinessFieldOutcome{TrackerIDs: []api.TrackerID{api.TrackerID("TRACKER" + hostile)}}
	prompt := cliAudioOriginPrompt("Source"+hostile, media, field, track)
	for _, terminal := range []bool{false, true} {
		text := cliPlainPromptText(prompt)
		if terminal {
			model := newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
			model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			model.Update(cliPromptRequest{prompt: prompt, reply: make(chan cliAnswer, 1)})
			text = model.evidence.GetContent()
		}
		for _, expected := range []string{"Source: Source", "Resource: Resource", "Required by TRACKER", "title: Main"} {
			if !strings.Contains(text, expected) {
				t.Fatalf("terminal=%t: missing safe field %q", terminal, expected)
			}
		}
		if strings.ContainsAny(text, "\x00\x1b\a\r\t") || strings.Contains(text, "CLIPBOARD") || strings.Contains(text, "SECRET_SENTINEL") {
			t.Fatalf("terminal=%t: source context escaped producer sanitization", terminal)
		}
	}
}

func TestSoundtrackReviewExplainsKnownOriginalAmbiguityInPlainAndTUI(t *testing.T) {
	media := api.MediaFacts{
		OriginalLanguage:      "Japanese",
		TrackCoverageComplete: true,
		MediaFileFacts:        api.MediaFileFacts{Files: []api.MediaFileFact{{ResourceID: "resource", FileName: filepath.Join("source", "Example.S01E01.mkv")}}},
		Tracks: []api.MediaTrackFacts{{
			ID:                  "track_example",
			ResourceID:          "resource",
			ManifestFingerprint: "scan",
			Kind:                api.MediaTrackAudio,
			Role:                api.AudioRoleProgramme,
			Ordinal:             1,
			Title:               "Main surround",
			Codec:               "AC-3",
			Channels:            6,
			ChannelsKnown:       true,
			DetectedLanguages:   []string{"de"},
			Languages:           []string{"German"},
		}},
	}
	media.LanguageFacts = mediafacts.ResolveLanguages(media)
	readiness, err := trackers.EvaluateInputReadiness(impl.MustNewRegistry(), []api.TrackerID{"AITHER", "BHD"}, api.UploadSubject{LanguageFacts: media.LanguageFacts})
	if err != nil {
		t.Fatal(err)
	}
	for _, terminal := range []bool{false, true} {
		for _, answer := range []string{"original", "dubbed", "unresolved"} {
			var out bytes.Buffer
			reader := bufio.NewReader(strings.NewReader(answer + "\n"))
			var presenter cliPresenter = &cliPlainPresenter{reader: reader, output: &out}
			var model *cliTUIModel
			var terminalPresenter *modelPresenter
			mode := api.InteractionModeInteractive
			if terminal {
				mode = api.InteractionModeUnattendedConfirm
				model = newCLITUIModel(t.Context(), newCLITUIBridge(), func() {}, nil, nil, false, false, true)
				model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
				terminalPresenter = &modelPresenter{model: model, answers: []string{answer}}
				presenter = terminalPresenter
			}
			writer := &cliPresentationWriter{
				Writer:    &out,
				presenter: presenter,
				ctx:       t.Context(),
				terminal:  terminal,
			}
			corrections, err := collectCLIInputAudioOrigins(reader, writer, mode, readiness.Fields, "Example Season", media)
			if err != nil || len(corrections) != 1 || corrections[0].Origin != api.TrackAudioOrigin(answer) {
				t.Fatalf("terminal=%t answer=%s corrections=%#v err=%v", terminal, answer, corrections, err)
			}
			text := out.String()
			if model != nil {
				if len(terminalPresenter.prompts) != 1 {
					t.Fatal("trackers asked separate source questions")
				}
				model.Update(cliPromptRequest{prompt: terminalPresenter.prompts[0], reply: make(chan cliAnswer, 1)})
				text = model.evidence.GetContent()
			}
			for _, expected := range []string{"Example Season", "Example.S01E01.mkv", "Main surround", "AC-3", "channels: 6", "Title original language: Japanese", "Inspected track languages: de", "Effective track languages: German", "AITHER, BHD", "original-soundtrack and dub rules", "original, dubbed, or unresolved", "Programme audio excludes commentary", "language difference alone does not prove a dub", "still need this evidence before upload", "--original-language LANGUAGE", "--track-languages", "track_example=LANGUAGE", "--track-audio-origin", "track_example=original", "separate actions"} {
				if !strings.Contains(text, expected) {
					t.Fatalf("terminal=%t missing %q in %s", terminal, expected, text)
				}
			}
		}
	}
}

func TestSoundtrackContextKeepsMissingFactsAndTerminalControlsSafe(t *testing.T) {
	track := api.MediaTrackFacts{ID: "track_example", Title: "Main\n\x1b]52;c;CLIPBOARD\a\x1b[2Jpassword=SECRET_SENTINEL"}
	prompt := cliAudioOriginPrompt("", api.MediaFacts{}, api.InputReadinessFieldOutcome{}, track)
	text := cliPlainPromptText(prompt)
	for _, expected := range []string{"Source: unknown", "Resource: unknown", "Audio track unknown", "codec: unknown", "channels: unknown", "Title original language: unknown", "Inspected track languages: unknown", "Effective track languages: unknown"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q", expected)
		}
	}
	if strings.ContainsAny(text, "\x1b\a") || strings.Contains(text, "SECRET_SENTINEL") || strings.Contains(text, "CLIPBOARD") || strings.Contains(text, "Main\n") {
		t.Fatal("source details escaped terminal sanitization")
	}
}
