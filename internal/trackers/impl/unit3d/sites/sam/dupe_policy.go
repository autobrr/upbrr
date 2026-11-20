// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"cmp"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func duplicatePolicy() *trackers.DupePolicy {
	dimensions := []trackers.DupeDimension{trackers.DupeDimensionMediaKind, trackers.DupeDimensionResolution, trackers.DupeDimensionCodec}
	return &trackers.DupePolicy{
		ID:                     "sam/duplicate/v2",
		EvidenceID:             "sam-upload-rules-slots",
		TargetEvidence:         targetAudioEvidence,
		CandidateEvidence:      candidateAudioEvidence,
		CompareEvidence:        compareAudioEvidence,
		SlotDimensions:         dimensions,
		CompleteSlotDimensions: dimensions,
	}
}

func targetAudioEvidence(subject api.UploadSubject) (api.DupeReleaseEvidence, []api.DupeComparisonEvidence) {
	facts := subject.LanguageFacts
	files := subject.MediaFileFacts
	if !facts.TrackCoverageComplete || !usableProgrammeEvidence(facts) ||
		subject.TVPack || files.ExpectedFileCount != 1 || len(files.Files) != 1 || files.Files[0].ResourceID == "" {
		return api.DupeReleaseEvidence{}, nil
	}
	var inventory []api.DupeAudioTrack
	var aggregateCorrection []string
	if subject.ManualLanguages.AggregateAudio {
		aggregateCorrection = subject.ManualLanguages.Audio
	}
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.ResourceID != files.Files[0].ResourceID || !programmeRole(track.Role) {
			continue
		}
		value, ok := programmeTrackLanguage(track, aggregateCorrection)
		if !ok {
			return api.DupeReleaseEvidence{}, nil
		}
		if languageutil.SameLanguage(value, "zxx") {
			continue
		}
		audio := audioTrack(value, track.Codec, 0)
		if audio.Language == "" {
			return api.DupeReleaseEvidence{}, nil
		}
		audio.Codec = refineDTSCodec(audio.Codec, track.AudioLabel)
		if track.ChannelsKnown {
			audio.Channels = track.Channels
		}
		inventory = append(inventory, audio)
	}
	return api.DupeReleaseEvidence{ProgrammeAudio: sortedAudio(inventory)}, nil
}

func candidateAudioEvidence(entry api.DupeEntry) api.DupeReleaseEvidence {
	evidence := entry.Evidence
	for _, track := range mediafacts.AudioTracksFromMediaInfoText(entry.MediaInfo) {
		if role := mediafacts.AudioRole(track.Title); role != "" && !programmeRole(role) {
			continue
		}
		value, ok := regionalTrackLanguage(track.Language, track.Title)
		if !ok {
			return entry.Evidence
		}
		if languageutil.SameLanguage(value, "zxx") {
			continue
		}
		audio := audioTrack(value, track.Format, track.Channels)
		if audio.Language == "" {
			return entry.Evidence
		}
		audio.Codec = refineDTSCodec(audio.Codec, track.CommercialName)
		if strings.EqualFold(track.Format, "MPEG Audio") {
			switch strings.ToLower(track.FormatProfile) {
			case "layer 2":
				audio.Codec = "mp2"
			case "layer 3":
				audio.Codec = "mp3"
			}
		}
		evidence.ProgrammeAudio = append(evidence.ProgrammeAudio, audio)
	}
	evidence.ProgrammeAudio = sortedAudio(evidence.ProgrammeAudio)
	return evidence
}

func compareAudioEvidence(context api.DupeEvidenceContext) api.DupeEvidenceDecision {
	proposed, existing := context.Target.Evidence.ProgrammeAudio, context.Candidate.Evidence.ProgrammeAudio
	// The existing release's report describes one resource, even when it is a pack.
	if len(proposed) == 0 || len(existing) == 0 || context.Target.Pack || context.Candidate.Pack ||
		!context.SameWorkAndContent || context.Relation == api.DupeRelationManualReview {
		return api.DupeEvidenceDecision{}
	}
	if slices.ContainsFunc(proposed, func(track api.DupeAudioTrack) bool { return track.Language == "pt-BR" }) &&
		!slices.ContainsFunc(existing, func(track api.DupeAudioTrack) bool {
			return track.Language == "pt" || track.Language == "pt-BR"
		}) {
		return api.DupeEvidenceDecision{Relation: api.DupeRelationCoexists, Reason: "sam_adds_brazilian_portuguese_audio"}
	}
	unknownPortuguese := func(track api.DupeAudioTrack) bool { return track.Language == "pt" }
	ambiguousPortuguese := slices.ContainsFunc(proposed, unknownPortuguese) || slices.ContainsFunc(existing, unknownPortuguese)
	pairPortuguese := ambiguousPortuguese && singlePortugueseProgramme(proposed) && singlePortugueseProgramme(existing)
	for _, track := range proposed {
		match := func(other api.DupeAudioTrack) bool { return other.Language == track.Language }
		if ambiguousPortuguese && isPortugueseProgramme(track) {
			if !pairPortuguese {
				continue
			}
			// The pair establishes a format comparison without establishing a region.
			match = isPortugueseProgramme
		}
		// Compare only when both releases have one programme format for this language.
		if slices.ContainsFunc(proposed, func(other api.DupeAudioTrack) bool {
			return match(other) && other != track
		}) {
			continue
		}
		at := slices.IndexFunc(existing, match)
		if at == -1 || slices.ContainsFunc(existing[at+1:], match) {
			continue
		}
		other := existing[at]
		if audioCodecsDiffer(track.Codec, other.Codec) ||
			track.Channels > 0 && other.Channels > 0 && track.Channels != other.Channels {
			return api.DupeEvidenceDecision{Relation: api.DupeRelationCoexists, Reason: "sam_different_audio_format"}
		}
	}
	return api.DupeEvidenceDecision{}
}

func singlePortugueseProgramme(tracks []api.DupeAudioTrack) bool {
	count := 0
	for _, track := range tracks {
		if isPortugueseProgramme(track) {
			count++
		}
	}
	return count == 1
}

func isPortugueseProgramme(track api.DupeAudioTrack) bool {
	return track.Language == "pt" || strings.HasPrefix(track.Language, "pt-")
}

func audioTrack(language, format string, channels int) api.DupeAudioTrack {
	if isBrazilianPortuguese(language) {
		return api.DupeAudioTrack{
			Language: "pt-BR",
			Codec:    audioCodec(format),
			Channels: channels,
		}
	}
	if !languageutil.IsIdentifiedLanguage(language) {
		return api.DupeAudioTrack{}
	}
	code := languageutil.NormalizeLanguageCode(language)
	if code == "pt" {
		if tag, ok := languageutil.LanguageTag(language); ok && tag.String() == "pt-PT" {
			return api.DupeAudioTrack{
				Language: "pt-PT",
				Codec:    audioCodec(format),
				Channels: channels,
			}
		}
	}
	return api.DupeAudioTrack{
		Language: code,
		Codec:    audioCodec(format),
		Channels: channels,
	}
}

func audioCodec(format string) string {
	format = strings.ToLower(strings.TrimSpace(format))
	// MediaInfo appends profile and extension labels to these format names.
	family := func(names ...string) bool {
		return slices.ContainsFunc(names, func(name string) bool { return format == name || strings.HasPrefix(format, name+" ") })
	}
	switch {
	case family("aac", "heaac", "he-aac"):
		return "aac"
	case family("e-ac-3", "eac3", "ddp", "dd+", "dolby digital plus"):
		return "eac3"
	case family("ac-3", "ac3", "dd", "dolby digital"):
		return "ac3"
	case family("mlp fba", "truehd", "dolby truehd"):
		return "truehd"
	case family("dts xll", "dts-hd ma", "dts-hd master audio"):
		return "dts-ma"
	case family("dts xbr", "dts-hd hra", "dts-hd high resolution audio", "dts-hd high-res audio"):
		return "dts-hra"
	case family("dts lbr", "dts express"):
		return "dts-lbr"
	case family("dts", "dts-es", "dts:x"):
		return "dts"
	case family("pcm", "lpcm"):
		return "pcm"
	case format == "flac", format == "opus", format == "mp3", format == "mp2", format == "vorbis":
		return format
	default:
		return ""
	}
}

func audioCodecsDiffer(left, right string) bool {
	// A bare DTS label can describe either of the specific DTS-HD profiles.
	return left != "" && right != "" && left != right &&
		(left != "dts" || !strings.HasPrefix(right, "dts-")) &&
		(right != "dts" || !strings.HasPrefix(left, "dts-"))
}

func refineDTSCodec(codec, label string) string {
	if codec != "dts" {
		return codec
	}
	if profile := audioCodec(label); strings.HasPrefix(profile, "dts-") {
		return profile
	}
	return codec
}

func sortedAudio(tracks []api.DupeAudioTrack) []api.DupeAudioTrack {
	slices.SortFunc(tracks, func(a, b api.DupeAudioTrack) int {
		return cmp.Or(cmp.Compare(a.Language, b.Language), cmp.Compare(a.Codec, b.Codec), cmp.Compare(a.Channels, b.Channels))
	})
	return slices.Compact(tracks)
}
