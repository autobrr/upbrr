// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package lst

import (
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func hdEncodeAudio(subject api.TrackerValidationSubject) bool {
	return strings.EqualFold(subject.Type, "ENCODE") && (subject.Release.Resolution == "720p" || subject.Release.Resolution == "1080p")
}

func losslessAudio(codec string) bool {
	return slices.Contains(
		[]string{
			"FLAC",
			"LPCM",
			"PCM",
			"DTS-HD MA",
			"DTS:X",
			"TRUEHD",
			"TRUEHD ATMOS",
			"ALAC",
			"WAVPACK",
			"MONKEY'S AUDIO",
			"APE",
			"MLP",
			"DST",
			"WMA LOSSLESS",
		},
		strings.ToUpper(strings.TrimSpace(codec)),
	)
}

func lossyAudio(codec string) bool {
	return slices.Contains(
		[]string{
			"DD",
			"DD+",
			"DD+ ATMOS",
			"DDP",
			"AC-3",
			"AC3",
			"E-AC-3",
			"E-AC3",
			"DTS",
			"DTS-ES",
			"DTS-HD HRA",
			"ADPCM",
			"DTS 96/24",
			"AAC",
			"HE-AAC",
			"OPUS",
			"VORBIS",
			"MP3",
			"MP2",
			"MPEG AUDIO",
		},
		strings.ToUpper(strings.TrimSpace(codec)),
	)
}

// exactAudioLayout never promotes a channel count to an evidenced multichannel layout.
func exactAudioLayout(track api.MediaTrackFacts) string {
	layout := strings.Join(strings.Fields(strings.ToUpper(track.ChannelLayout)), " ")
	var value string
	switch layout {
	case "1.0", "C", "MONO":
		value = "1.0"
	case "2.0", "L R", "STEREO":
		value = "2.0"
	case "5.1", "L R C LFE LS RS", "L R C LFE LB RB", "L R C LFE SL SR":
		value = "5.1"
	case "6.0", "7.1", "5.0", "4.0", "3.0":
		value = layout
	}
	if value == "" && track.ChannelsKnown && track.Channels > 2 && track.Channels != 6 {
		return "other"
	}
	if value == "" && layout == "" && track.ChannelsKnown {
		if track.Channels == 1 {
			return "1.0"
		}
		if track.Channels == 2 {
			return "2.0"
		}
	}
	if track.ChannelsKnown {
		expected := map[string]int{
			"1.0": 1,
			"2.0": 2,
			"3.0": 3,
			"4.0": 4,
			"5.0": 5,
			"5.1": 6,
			"6.0": 6,
			"7.1": 8,
		}[value]
		if expected > 0 && expected != track.Channels {
			return ""
		}
	}

	return value
}

func audioTierFailures(subject api.TrackerValidationSubject) []api.RuleFailure {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var failures []api.RuleFailure
	add := func(track api.MediaTrackFacts, key, reason string, outcome trackers.LanguageOutcome) {
		failures = append(failures, trackers.LanguageRuleFailure(
			subject,
			"lst_audio_"+key,
			fmt.Sprintf(
				"resource %s, track %s (%s, layout %q, %d-bit): %s",
				track.ResourceID,
				track.ID,
				track.Codec,
				track.ChannelLayout,
				track.BitDepth,
				reason,
			),
			outcome,
		))
	}
	releaseHD := hdEncodeAudio(subject)
	if releaseHD && !subject.LanguageFacts.TrackCoverageComplete {
		failures = append(
			failures,
			trackers.LanguageRuleFailure(subject, "lst_audio_coverage", "audio evidence does not cover every programme resource", trackers.LanguageUnresolved),
		)
	}
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		hd := releaseHD
		for _, file := range subject.MediaFileFacts.Files {
			if file.ResourceID != "" && file.ResourceID == track.ResourceID {
				hd = strings.EqualFold(subject.Type, "ENCODE") && (file.Resolution == "720p" || file.Resolution == "1080p")
				if strings.EqualFold(subject.Type, "ENCODE") && file.Resolution == "" {
					add(track, "tier_evidence", "resource resolution is unknown", trackers.LanguageUnresolved)
				}
				break
			}
		}
		codec := strings.ToUpper(strings.TrimSpace(track.Codec))
		layout := exactAudioLayout(track)
		if hd && !losslessAudio(codec) && !lossyAudio(codec) {
			add(track, "codec_evidence", "audio codec compression class is unresolved", trackers.LanguageUnresolved)
		}
		if hd && losslessAudio(codec) && track.Role == "" && !track.Commentary {
			add(track, "role_evidence", "establish whether this track is programme, commentary or isolated score", trackers.LanguageUnresolved)
		}
		if hd && losslessAudio(codec) {
			if track.Commentary || track.Role == api.AudioRoleCommentary || track.Role == api.AudioRoleIsolatedScore {
				add(track, "secondary_codec", "commentary and isolated scores require lossy audio", trackers.LanguageProhibited)
			} else if codec != "FLAC" {
				add(track, "codec", "use lossy audio or eligible 16-bit FLAC", trackers.LanguageProhibited)
			}
		}
		if codec == "FLAC" {
			switch {
			case layout == "":
				add(track, "layout_evidence", "an exact permitted channel layout must be established", trackers.LanguageUnresolved)
			case layout == "5.1" && !subject.AnimeKnown:
				add(track, "anime_evidence", "confirm the prepared anime classification before applying a multichannel exception", trackers.LanguageUnresolved)
			case layout != "1.0" && layout != "2.0" && (!subject.Anime || layout != "5.1"):
				add(track, "layout", "multichannel FLAC requires exact anime 5.1 audio", trackers.LanguageProhibited)
			}
			if hd {
				if track.BitDepth == 0 {
					add(track, "depth_evidence", "audio sample depth is unknown", trackers.LanguageUnresolved)
				} else if track.BitDepth != 16 {
					add(track, "depth", "FLAC must be 16-bit; dither when reducing a higher source depth", trackers.LanguageProhibited)
				}
			}
			if track.BitDepth == 16 &&
				subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "audio_processing_"+track.ID)] == "reduced_dither_unknown" {
				add(track, "dither_evidence", "higher-depth reduction is established but dithering remains unresolved", trackers.LanguageUnresolved)
				failures[len(failures)-1].QuestionnaireInput = true
			}
			if track.BitDepth == 16 &&
				subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "audio_processing_"+track.ID)] == "reduced_without_dither" {
				add(track, "dither", "a confirmed higher-depth reduction requires dithering", trackers.LanguageProhibited)
			}
		}
		if hd {
			continue
		}
		if codec == "LPCM" || codec == "PCM" {
			required := "convert to DTS-HD MA or TrueHD with permitted compatibility audio"
			if layout == "" {
				required = "LPCM requires conversion; establish the channel layout before choosing an eligible target"
			}
			if layout == "1.0" {
				required = "convert to FLAC 1.0 or DTS-HD MA 1.0"
			}
			if layout == "2.0" {
				required = "convert to FLAC 2.0"
			}
			if subject.Anime && layout == "5.1" {
				required += "; exact FLAC 5.1 is also permitted"
			}
			add(track, "pcm", required, trackers.LanguageProhibited)
		}
		if (codec == "DTS-HD MA" || codec == "TRUEHD" || codec == "TRUEHD ATMOS") && layout == "2.0" {
			outcome := trackers.LanguageAdvisory
			if subject.PersonalRelease {
				outcome = trackers.LanguageProhibited
			}
			add(track, "stereo_codec", "stereo lossless audio should use FLAC", outcome)
		}
	}
	return failures
}

func audioProcessingQuestions(subject api.TrackerValidationSubject) []api.TrackerQuestionnaireField {
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return nil
	}
	var fields []api.TrackerQuestionnaireField
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.ID == "" || !strings.EqualFold(track.Codec, "FLAC") || track.BitDepth != 16 {
			continue
		}
		fields = append(fields, api.TrackerQuestionnaireField{
			Key:         trackers.LanguageQuestionKey(subject, "audio_processing_"+track.ID),
			Label:       "Audio processing for resource " + track.ResourceID + ", track " + track.ID,
			Kind:        "select",
			Required:    subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "audio_processing_"+track.ID)] == "reduced_dither_unknown",
			NeedsReview: subject.QuestionnaireAnswers[trackers.LanguageQuestionKey(subject, "audio_processing_"+track.ID)] == "reduced_dither_unknown",
			Options:     []string{"unknown", "native_16_bit", "reduced_with_dither", "reduced_without_dither", "reduced_dither_unknown"},
			Help:        "Use source logs or your direct knowledge of this exact track. The final sample depth alone does not establish processing history.",
		})
	}
	return fields
}
