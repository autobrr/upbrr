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

// HasEnglishDub and HasOriginalAudio describe programme audio, never names or
// commentary. Unresolved original-language evidence cannot establish a dub.
func (f LanguageFacts) HasEnglishDub() bool {
	return f.OriginalLanguagesKnown && len(f.OriginalLanguages) > 0 && !slices.Contains(f.OriginalLanguages, "English") &&
		!slices.Contains(f.OriginalLanguages, "ZXX") &&
		!slices.Contains(f.OriginalLanguages, "Multiple Languages") &&
		slices.Contains(f.ProgrammeLanguages, "English")
}

func (f LanguageFacts) HasOriginalAudio() bool {
	if !f.OriginalLanguagesKnown {
		return false
	}
	for _, language := range f.OriginalLanguages {
		if slices.Contains(f.ProgrammeLanguages, language) {
			return true
		}
	}
	return false
}
