// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func parseCLITrackAudioOrigins(values []string) ([]api.TrackAudioOriginCorrection, error) {
	var corrections []api.TrackAudioOriginCorrection
	seen := make(map[string]bool)
	for _, raw := range values {
		id, value, ok := strings.Cut(raw, "=")
		id = strings.TrimSpace(id)
		origin := api.TrackAudioOrigin(strings.TrimSpace(value))
		if !ok || id == "" || (origin != api.TrackAudioOriginal && origin != api.TrackAudioDubbed && origin != api.TrackAudioUnresolved) {
			return nil, fmt.Errorf("track-audio-origin requires track-id=original|dubbed|unresolved: %q", raw)
		}
		if seen[id] {
			return nil, fmt.Errorf("duplicate track-audio-origin target %q", id)
		}
		seen[id] = true
		corrections = append(corrections, api.TrackAudioOriginCorrection{TrackID: id, Origin: origin})
	}
	return corrections, nil
}

func buildCLITrackAudioOriginCorrections(values []string, tracks []api.MediaTrackFacts) ([]api.TrackAudioOriginCorrection, error) {
	corrections, err := parseCLITrackAudioOrigins(values)
	if err != nil {
		return nil, err
	}
	for i := range corrections {
		index := slices.IndexFunc(tracks, func(track api.MediaTrackFacts) bool { return track.ID == corrections[i].TrackID })
		if index < 0 || tracks[index].ManifestFingerprint == "" {
			return nil, fmt.Errorf("track-audio-origin requires a prepared manifest track %q; run --input-only first", corrections[i].TrackID)
		}
		track := tracks[index]
		if track.Kind != api.MediaTrackAudio || (track.Role != api.AudioRoleProgramme && track.Role != api.AudioRoleAlternateMix) {
			return nil, fmt.Errorf("track-audio-origin requires a programme audio track: %q", track.ID)
		}
		corrections[i].ManifestFingerprint = track.ManifestFingerprint
	}
	return corrections, nil
}

// collectCLIInputAudioOrigins records source evidence, never permission to waive a
// tracker rule. Unknown answers remain unresolved through canonical preparation.
func collectCLIInputAudioOrigins(reader *bufio.Reader, output io.Writer, interaction api.InteractionMode,
	fields []api.InputReadinessFieldOutcome, tracks []api.MediaTrackFacts,
) ([]api.TrackAudioOriginCorrection, error) {
	var values []string
	seen := make(map[string]bool)
	for _, field := range fields {
		if field.CorrectionField == nil || *field.CorrectionField != api.CorrectionFieldMetadataTrackAudioOrigins ||
			field.Status == api.InputReadinessFieldReady || seen[field.TrackID] {
			continue
		}
		seen[field.TrackID] = true
		index := slices.IndexFunc(tracks, func(track api.MediaTrackFacts) bool { return track.ID == field.TrackID })
		if index < 0 || tracks[index].ManifestFingerprint == "" {
			return nil, errors.New("upbrr: soundtrack review no longer matches inspected source; reprepare the input")
		}
		track := tracks[index]
		if interaction == api.InteractionModeUnattended {
			fmt.Fprintf(
				output,
				"Unattended: programme track %s needs original-versus-dubbed evidence for %s. Use --track-audio-origin '%s=original|dubbed|unresolved', or --uac for source review.\n",
				track.ID,
				strings.Join(trackerIDStrings(field.TrackerIDs), ", "),
				track.ID,
			)
			continue
		}
		answer, err := askCLIField(reader, output, cliPrompt{
			Kind:     cliPromptText,
			Question: fmt.Sprintf("Programme audio track %d (%s) soundtrack origin: ", track.Ordinal, track.ID),
			Help:     "Enter original, dubbed, or unresolved from verified source knowledge. A multilingual original may contain several languages; a language tag alone does not prove a dub.",
			Required: true,
		})
		if err != nil {
			return nil, err
		}
		values = append(values, track.ID+"="+answer)
	}
	return buildCLITrackAudioOriginCorrections(values, tracks)
}
