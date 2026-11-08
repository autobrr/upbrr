// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"slices"

	"github.com/autobrr/upbrr/internal/languageutil"
)

// AudioTrackRole distinguishes programme content from separately assessed audio.
// Empty means the inspected evidence did not establish the role.
type AudioTrackRole string

const (
	AudioRoleProgramme     AudioTrackRole = "programme"
	AudioRoleCommentary    AudioTrackRole = "commentary"
	AudioRoleCompatibility AudioTrackRole = "compatibility"
	AudioRoleIsolatedScore AudioTrackRole = "isolated_score"
	AudioRoleHistorical    AudioTrackRole = "historical"
	AudioRoleAlternateMix  AudioTrackRole = "alternate_mix"
	AudioRoleInterview     AudioTrackRole = "interview"
	AudioRoleNovelty       AudioTrackRole = "novelty"
	AudioRoleDescription   AudioTrackRole = "audio_description"
	AudioRoleVoiceOver     AudioTrackRole = "voice_over"
)

// TrackAudioOrigin distinguishes original programme content from a confirmed dub.
// Empty means no explicit source-level classification has been established.
type TrackAudioOrigin string

const (
	TrackAudioOriginal   TrackAudioOrigin = "original"
	TrackAudioDubbed     TrackAudioOrigin = "dubbed"
	TrackAudioUnresolved TrackAudioOrigin = "unresolved"
)

// LanguageFacts retains finalized language and inspected-track evidence from one
// prepared generation. Presentation overrides cannot alter these facts.
type LanguageFacts struct {
	AudioAbsent            bool
	OriginalLanguages      []string
	OriginalLanguagesKnown bool
	// OriginalLanguageDefinitive distinguishes a verified correction, one spoken
	// language, or original/spoken agreement from unranked dialogue or scalar fallback.
	OriginalLanguageDefinitive bool
	ProgrammeLanguages         []string
	SubtitleLanguages          []string
	FullSubtitleLanguages      []string
	Tracks                     []MediaTrackFacts
	PrimaryAudioTrackID        string
	AudioStatus                MetadataEvidenceStatus
	ProgrammeStatus            MetadataEvidenceStatus
	SubtitleStatus             MetadataEvidenceStatus
	TrackCoverageComplete      bool
}

// Clone detaches all retained language and track slices.
func (f LanguageFacts) Clone() LanguageFacts {
	f.OriginalLanguages = slices.Clone(f.OriginalLanguages)
	f.ProgrammeLanguages = slices.Clone(f.ProgrammeLanguages)
	f.SubtitleLanguages = slices.Clone(f.SubtitleLanguages)
	f.FullSubtitleLanguages = slices.Clone(f.FullSubtitleLanguages)
	f.Tracks = slices.Clone(f.Tracks)
	for i := range f.Tracks {
		f.Tracks[i].Languages = slices.Clone(f.Tracks[i].Languages)
		f.Tracks[i].DetectedLanguages = slices.Clone(f.Tracks[i].DetectedLanguages)
	}
	return f
}

// HasNonEnglishTitleLanguage reports identified non-English title evidence.
// It does not establish a release soundtrack, its origin, or subtitle coverage.
func (f LanguageFacts) HasNonEnglishTitleLanguage() bool {
	return f.OriginalLanguagesKnown && slices.ContainsFunc(f.OriginalLanguages, func(value string) bool {
		return languageutil.IsIdentifiedLanguage(value) && !languageutil.SameLanguage(value, "English") && !languageutil.SameLanguage(value, "ZXX")
	})
}

// ProgrammeAudioOrigin preserves explicit source evidence. Matching-language tracks
// are original. English programme audio is a dub when the title's non-English
// original is definitive, or the same resource contains non-English original audio.
func (f LanguageFacts) ProgrammeAudioOrigin(track MediaTrackFacts) TrackAudioOrigin {
	if track.Kind != MediaTrackAudio || (track.Role != AudioRoleProgramme && track.Role != AudioRoleAlternateMix) {
		return ""
	}
	if track.AudioOrigin != "" {
		if track.AudioOrigin != TrackAudioOriginal && track.AudioOrigin != TrackAudioDubbed {
			return ""
		}
		return track.AudioOrigin
	}
	if !f.OriginalLanguagesKnown {
		return ""
	}
	matchesOriginalLanguages := func(candidate MediaTrackFacts) bool {
		return len(candidate.Languages) > 0 && !slices.ContainsFunc(candidate.Languages, func(language string) bool {
			return !languageutil.ContainsLanguage(f.OriginalLanguages, language)
		})
	}
	if matchesOriginalLanguages(track) {
		return TrackAudioOriginal
	}
	if len(f.OriginalLanguages) == 0 || languageutil.ContainsLanguage(f.OriginalLanguages, "English") ||
		languageutil.ContainsLanguage(f.OriginalLanguages, "ZXX") ||
		f.ProgrammeStatus == MetadataEvidenceStatusContradictory ||
		!languageutil.ContainsLanguage(f.ProgrammeLanguages, "English") ||
		len(track.Languages) != 1 ||
		!languageutil.SameLanguage(track.Languages[0], "English") {
		return ""
	}
	if f.OriginalLanguageDefinitive {
		return TrackAudioDubbed
	}
	if slices.ContainsFunc(f.Tracks, func(original MediaTrackFacts) bool {
		return original.ResourceID == track.ResourceID && original.Kind == MediaTrackAudio &&
			(original.Role == AudioRoleProgramme || original.Role == AudioRoleAlternateMix) &&
			(original.AudioOrigin == TrackAudioOriginal || original.AudioOrigin == "" && matchesOriginalLanguages(original)) &&
			slices.ContainsFunc(original.Languages, func(language string) bool {
				code := languageutil.NormalizeLanguageCode(language)
				return languageutil.IsIdentifiedLanguage(language) && code != "en" && code != "zxx"
			})
	}) {
		return TrackAudioDubbed
	}
	return ""
}

// HasUnresolvedProgrammeAudioOrigin distinguishes missing soundtrack evidence from
// a proven absence of original audio. Commentary and other non-programme roles do not count.
func (f LanguageFacts) HasUnresolvedProgrammeAudioOrigin() bool {
	found := false
	for _, track := range f.Tracks {
		if track.Kind != MediaTrackAudio || (track.Role != AudioRoleProgramme && track.Role != AudioRoleAlternateMix) {
			continue
		}
		found = true
		if f.ProgrammeAudioOrigin(track) == "" {
			return true
		}
	}
	return !found && len(f.ProgrammeLanguages) > 0
}

// HasEnglishDub identifies classified or inferred English programme dubs, excluding
// English dialogue within an original multilingual stream and aggregate corrections.
// Explicit dub evidence does not require the title's original language.
func (f LanguageFacts) HasEnglishDub() bool {
	if !languageutil.ContainsLanguage(f.ProgrammeLanguages, "English") ||
		f.ProgrammeStatus == MetadataEvidenceStatusContradictory {
		return false
	}
	return slices.ContainsFunc(f.Tracks, func(track MediaTrackFacts) bool {
		return f.ProgrammeAudioOrigin(track) == TrackAudioDubbed && languageutil.ContainsLanguage(track.Languages, "English")
	})
}

// HasEnglishOriginalAudio reports an original programme track identified only as
// English. English dialogue in multilingual track facts and confirmed dubs do not
// establish this evidence.
func (f LanguageFacts) HasEnglishOriginalAudio() bool {
	return f.HasOriginalAudio() && slices.ContainsFunc(f.Tracks, func(track MediaTrackFacts) bool {
		return f.ProgrammeAudioOrigin(track) == TrackAudioOriginal && len(track.Languages) > 0 &&
			!slices.ContainsFunc(track.Languages, func(language string) bool { return !languageutil.SameLanguage(language, "English") })
	})
}

// HasOriginalAudio accepts original programme tracks, including verified multilingual
// content whose main-language tag differs from the title's recorded original language.
// Explicit original-soundtrack evidence survives unknown title-language metadata.
func (f LanguageFacts) HasOriginalAudio() bool {
	return len(f.ProgrammeLanguages) > 0 && f.ProgrammeStatus != MetadataEvidenceStatusContradictory &&
		slices.ContainsFunc(f.Tracks, func(track MediaTrackFacts) bool {
			return f.ProgrammeAudioOrigin(track) == TrackAudioOriginal
		})
}
