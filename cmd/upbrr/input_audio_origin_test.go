// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCLITrackAudioOriginsNormalizeValuesOnly(t *testing.T) {
	opts, _, _, err := parseCLIOptions([]string{
		"--track-audio-origin", " Audio = Original ",
		"--track-audio-origin", "audio=DuBbEd",
		"--track-audio-origin", "other= UNRESOLVED ",
		"example.mkv",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseCLITrackAudioOrigins(opts.TrackAudioOrigins)
	want := []api.TrackAudioOriginCorrection{
		{TrackID: "Audio", Origin: api.TrackAudioOriginal},
		{TrackID: "audio", Origin: api.TrackAudioDubbed},
		{TrackID: "other", Origin: api.TrackAudioUnresolved},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("corrections=%#v, err=%v, want=%#v", got, err, want)
	}
}

func TestCLITrackAudioOriginsRemainManifestBound(t *testing.T) {
	tracks := []api.MediaTrackFacts{{
		ID:                  "audio",
		ManifestFingerprint: "current",
		Kind:                api.MediaTrackAudio,
		Role:                api.AudioRoleProgramme,
	}}
	got, err := buildCLITrackAudioOriginCorrections([]string{"audio=original"}, tracks)
	if err != nil || len(got) != 1 || got[0].ManifestFingerprint != "current" || got[0].Origin != api.TrackAudioOriginal {
		t.Fatalf("corrections=%#v, err=%v", got, err)
	}
	for _, values := range [][]string{{"audio"}, {"audio=German"}, {"=original"}, {"audio=original", "audio=dubbed"}, {"other=dubbed"}} {
		if _, err := buildCLITrackAudioOriginCorrections(values, tracks); err == nil {
			t.Fatalf("accepted %v", values)
		}
	}
	tracks[0].Role = api.AudioRoleCommentary
	if _, err := buildCLITrackAudioOriginCorrections([]string{"audio=dubbed"}, tracks); err == nil {
		t.Fatal("classified commentary as a programme dub")
	}
}

func TestCLITrackAudioOriginReviewBatchesWithoutWaivingUnknown(t *testing.T) {
	field := api.InputReadinessFieldOutcome{
		TrackID:         "audio",
		CorrectionField: new(api.CorrectionFieldMetadataTrackAudioOrigins),
		Status:          api.InputReadinessFieldMissing,
		TrackerIDs:      []api.TrackerID{"AITHER", "BHD"},
	}
	tracks := []api.MediaTrackFacts{{
		ID:                  "audio",
		ManifestFingerprint: "current",
		Kind:                api.MediaTrackAudio,
		Role:                api.AudioRoleProgramme,
	}}
	for _, mode := range []api.InteractionMode{api.InteractionModeInteractive, api.InteractionModeUnattendedConfirm} {
		var out bytes.Buffer
		got, err := collectCLIInputAudioOrigins(bufio.NewReader(strings.NewReader(" UnReSoLvEd \n")), &out, mode, []api.InputReadinessFieldOutcome{field, field}, tracks)
		if err != nil || len(got) != 1 || got[0].Origin != api.TrackAudioUnresolved || got[0].ManifestFingerprint != "current" || strings.Count(out.String(), "soundtrack origin:") != 1 {
			t.Fatalf("got=%#v err=%v output=%s", got, err, out.String())
		}
	}
	var out bytes.Buffer
	got, err := collectCLIInputAudioOrigins(bufio.NewReader(strings.NewReader("")), &out, api.InteractionModeUnattended, []api.InputReadinessFieldOutcome{field}, tracks)
	if err != nil || len(got) != 0 || !strings.Contains(out.String(), "--track-audio-origin") || strings.Contains(out.String(), "soundtrack origin:") {
		t.Fatalf("unattended prompted: %#v %v %s", got, err, out.String())
	}
}

func TestCLITrackOriginAndLanguageCorrectionsAreIndependent(t *testing.T) {
	tracks := []api.MediaTrackFacts{{
		ID:                  "audio",
		ManifestFingerprint: "current",
		Kind:                api.MediaTrackAudio,
		Role:                api.AudioRoleProgramme,
	}}
	patch, err := buildCLIInputCorrectionPatch(cliOptions{TrackLanguages: []string{"audio=Japanese,English"}, TrackAudioOrigins: []string{"audio=original"}}, nil, tracks)
	if err != nil || patch == nil || len(patch.Values.Metadata.TrackLanguages) != 1 || len(patch.Values.Metadata.TrackAudioOrigins) != 1 {
		t.Fatalf("patch=%#v err=%v", patch, err)
	}
	_, err = buildCLIInputCorrectionPatch(cliOptions{TrackAudioOrigins: []string{"audio=original"}, ResetInput: []string{"metadata.track_audio_origins:audio"}}, nil, tracks)
	if err == nil {
		t.Fatal("accepted set and reset for the same origin")
	}
}
