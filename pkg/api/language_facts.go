// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "slices"

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
	ProgrammeLanguages     []string
	SubtitleLanguages      []string
	FullSubtitleLanguages  []string
	Tracks                 []MediaTrackFacts
	PrimaryAudioTrackID    string
	AudioStatus            MetadataEvidenceStatus
	ProgrammeStatus        MetadataEvidenceStatus
	SubtitleStatus         MetadataEvidenceStatus
	TrackCoverageComplete  bool
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

// ProgrammeAudioOrigin preserves explicit source evidence. Ordinary matching-language
// tracks remain original without a special flag; a mismatch alone never proves dubbing.
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
	if f.OriginalLanguagesKnown && len(track.Languages) > 0 && !slices.ContainsFunc(track.Languages, func(language string) bool {
		return !slices.Contains(f.OriginalLanguages, language)
	}) {
		return TrackAudioOriginal
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

// HasEnglishDub requires a classified English programme dub, not English dialogue
// within an original multilingual stream or an aggregate language correction.
func (f LanguageFacts) HasEnglishDub() bool {
	if !f.OriginalLanguagesKnown || len(f.OriginalLanguages) == 0 || !slices.Contains(f.ProgrammeLanguages, "English") ||
		slices.Contains(f.OriginalLanguages, "English") || slices.Contains(f.OriginalLanguages, "ZXX") ||
		f.ProgrammeStatus == MetadataEvidenceStatusContradictory {
		return false
	}
	return slices.ContainsFunc(f.Tracks, func(track MediaTrackFacts) bool {
		return f.ProgrammeAudioOrigin(track) == TrackAudioDubbed && slices.Contains(track.Languages, "English")
	})
}

// HasOriginalAudio accepts original programme tracks, including verified multilingual
// content whose main-language tag differs from the title's recorded original language.
func (f LanguageFacts) HasOriginalAudio() bool {
	return f.OriginalLanguagesKnown && len(f.ProgrammeLanguages) > 0 && f.ProgrammeStatus != MetadataEvidenceStatusContradictory &&
		slices.ContainsFunc(f.Tracks, func(track MediaTrackFacts) bool {
			return f.ProgrammeAudioOrigin(track) == TrackAudioOriginal
		})
}
