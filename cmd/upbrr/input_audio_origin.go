// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func parseCLITrackAudioOrigins(values []string) ([]api.TrackAudioOriginCorrection, error) {
	var corrections []api.TrackAudioOriginCorrection
	seen := make(map[string]bool)
	for _, raw := range values {
		id, value, ok := strings.Cut(raw, "=")
		id = strings.TrimSpace(id)
		origin := api.TrackAudioOrigin(strings.ToLower(strings.TrimSpace(value)))
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
	fields []api.InputReadinessFieldOutcome, sourcePath string, media api.MediaFacts,
) ([]api.TrackAudioOriginCorrection, error) {
	tracks := media.Tracks
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
			fmt.Fprint(output, safeTerminalText(fmt.Sprintf(
				"Unattended: programme track %s needs original-versus-dubbed evidence for %s. Use --track-audio-origin '%s=original|dubbed|unresolved', or --uac for source review.\n",
				track.ID,
				strings.Join(trackerIDStrings(field.TrackerIDs), ", "),
				track.ID,
			)))
			continue
		}
		answer, err := askCLIField(reader, output, cliAudioOriginPrompt(sourcePath, media, field, track))
		if err != nil {
			return nil, err
		}
		values = append(values, track.ID+"="+answer)
	}
	return buildCLITrackAudioOriginCorrections(values, tracks)
}

func cliAudioOriginPrompt(sourcePath string, media api.MediaFacts, field api.InputReadinessFieldOutcome, track api.MediaTrackFacts) cliPrompt {
	source, resource := "unknown", "unknown"
	if sourcePath != "" {
		source = filepath.Base(sourcePath)
	}
	for _, file := range media.MediaFileFacts.Files {
		if track.ResourceID != "" && file.ResourceID == track.ResourceID && file.FileName != "" {
			resource = filepath.Base(file.FileName)
			break
		}
	}
	original := "unknown"
	if media.LanguageFacts.OriginalLanguagesKnown {
		original = cliAudioOriginDetail(strings.Join(media.LanguageFacts.OriginalLanguages, ", "))
	}
	ordinal, channels := "unknown", "unknown"
	if track.Ordinal > 0 {
		ordinal = strconv.Itoa(track.Ordinal)
	}
	if track.Channels > 0 {
		channels = strconv.Itoa(track.Channels)
		if !track.ChannelsKnown {
			channels += " (partial)"
		}
	}
	if track.ChannelLayout != "" {
		channels += "; layout: " + cliAudioOriginDetail(track.ChannelLayout)
	}
	trackers := cliAudioOriginDetail(strings.Join(trackerIDStrings(field.TrackerIDs), ", "))
	return cliPrompt{
		Kind:     cliPromptText,
		Question: "Programme audio track " + ordinal + " soundtrack origin: ",
		Evidence: fmt.Sprintf("Source: %s\nResource: %s (ID: %s)\nAudio track %s; title: %s; codec: %s; channels: %s\n"+
			"Title original language: %s\nInspected track languages: %s\nEffective track languages: %s\nTrack correction ID: %s\n"+
			"Required by %s to apply original-soundtrack and dub rules: %s",
			cliAudioOriginDetail(source), cliAudioOriginDetail(resource), cliAudioOriginDetail(track.ResourceID), ordinal,
			cliAudioOriginDetail(track.Title), cliAudioOriginDetail(track.Codec), channels, original,
			cliAudioOriginDetail(strings.Join(track.DetectedLanguages, ", ")), cliAudioOriginDetail(strings.Join(track.Languages, ", ")),
			cliAudioOriginDetail(track.ID), trackers, cliAudioOriginDetail(field.Message)),
		Help: fmt.Sprintf("Is this the film/episode's original soundtrack or a translated dub? Programme audio excludes commentary.\n"+
			"Enter original, dubbed, or unresolved from verified source knowledge:\n"+
			"original = the original soundtrack, which may contain several languages.\n"+
			"dubbed = a translated replacement soundtrack. A language difference alone does not prove a dub.\n"+
			"unresolved = you cannot verify either; %s still need this evidence before upload. Other eligible trackers can continue.\n"+
			"Corrections: --original-language LANGUAGE; --track-languages %q; --track-audio-origin %q (or dubbed or unresolved).\n"+
			"Changing the title's original-language metadata, identifying a track's languages and confirming its soundtrack origin are separate actions.",
			trackers, track.ID+"=LANGUAGE", track.ID+"=original"),
		Required: true,
	}
}

func cliAudioOriginDetail(value string) string {
	value = strings.Join(strings.Fields(safeTerminalText(value)), " ")
	if value == "" {
		return "unknown"
	}
	return value
}
