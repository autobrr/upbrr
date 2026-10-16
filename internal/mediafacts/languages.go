// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/pkg/api"
)

// AudioRole classifies explicit inspected track-title evidence. It does not
// establish source provenance, mix uniqueness, or tracker permission.
func AudioRole(title string) api.AudioTrackRole {
	title = strings.ToLower(strings.TrimSpace(title))
	switch {
	case strings.Contains(title, "audio description"), strings.Contains(title, "descriptive audio"):
		return api.AudioRoleDescription
	case strings.Contains(title, "voice-over"), strings.Contains(title, "voice over"):
		return api.AudioRoleVoiceOver
	case strings.Contains(title, "commentary"):
		return api.AudioRoleCommentary
	case strings.Contains(title, "compatibility"), strings.Contains(title, "embedded core"):
		return api.AudioRoleCompatibility
	case strings.Contains(title, "isolated score"), strings.Contains(title, "isolated music"):
		return api.AudioRoleIsolatedScore
	case strings.Contains(title, "historical audio"):
		return api.AudioRoleHistorical
	case strings.Contains(title, "interview"):
		return api.AudioRoleInterview
	case strings.Contains(title, "novelty"):
		return api.AudioRoleNovelty
	case strings.Contains(title, "original mix"), strings.Contains(title, "theatrical mix"),
		strings.Contains(title, "alternate mix"), strings.Contains(title, "remix"), strings.Contains(title, "upmix"):
		return api.AudioRoleAlternateMix
	case strings.Contains(title, "main"), strings.Contains(title, "dub"), strings.Contains(title, "original audio"):
		return api.AudioRoleProgramme
	default:
		return ""
	}
}

// ResolveLanguages projects corrected media evidence once at canonical
// preparation. Aggregate corrections do not rewrite inspected track roles.
func ResolveLanguages(media api.MediaFacts) api.LanguageFacts {
	facts := api.LanguageFacts{
		AudioAbsent:           media.AudioAbsent && media.TrackCoverageComplete,
		OriginalLanguages:     languageutil.NormalizeLanguageList([]string{media.OriginalLanguage}),
		SubtitleLanguages:     subtitleBaseLanguages(append(slices.Clone(media.SubtitleLanguages), media.HardcodedSubtitleLanguages...)),
		FullSubtitleLanguages: fullSubtitleLanguages(media),
		Tracks:                media.Tracks,
		PrimaryAudioTrackID:   media.PrimaryAudioTrackID,
		ProgrammeStatus:       api.MetadataEvidenceStatusComplete,
		SubtitleStatus:        api.MetadataEvidenceStatusComplete,
		TrackCoverageComplete: media.TrackCoverageComplete,
	}.Clone()

	audioCount := 0
	for _, track := range facts.Tracks {
		if track.Kind == api.MediaTrackSubtitle {
			if !knownLanguages(track.Languages) {
				facts.SubtitleStatus = api.MetadataEvidenceStatusPartial
			}
			continue
		}
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		audioCount++
		if track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix {
			facts.ProgrammeLanguages = append(facts.ProgrammeLanguages, track.Languages...)
		}
	}
	facts.ProgrammeLanguages = languageutil.NormalizeLanguageList(facts.ProgrammeLanguages)
	if audioCount == 0 {
		facts.ProgrammeLanguages = languageutil.NormalizeLanguageList(media.AudioLanguages)
		if facts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory {
			facts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
		}
	}
	if media.AudioLanguagesProvenance.IsManual() {
		corrected := languageutil.NormalizeLanguageList(media.AudioLanguages)
		if audioCount > 0 && len(corrected) > 0 && !sameLanguages(corrected, facts.ProgrammeLanguages) {
			facts.ProgrammeStatus = api.MetadataEvidenceStatusContradictory
		}
		facts.ProgrammeLanguages = corrected
	}
	facts.ProgrammeStatus = InspectedProgrammeStatus(facts)
	if !media.TrackCoverageComplete {
		if facts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory {
			facts.ProgrammeStatus = api.MetadataEvidenceStatusPartial
		}
		facts.SubtitleStatus = api.MetadataEvidenceStatusPartial
	}
	if media.SubtitleLanguagesProvenance.IsManual() {
		facts.SubtitleStatus = api.MetadataEvidenceStatusComplete
	}
	if facts.AudioAbsent && len(facts.ProgrammeLanguages) == 0 {
		facts.ProgrammeStatus = api.MetadataEvidenceStatusComplete
	}
	facts.OriginalLanguagesKnown = knownLanguages(facts.OriginalLanguages)
	facts.AudioStatus = facts.ProgrammeStatus
	if (len(facts.OriginalLanguages) == 0 || !knownLanguages(facts.OriginalLanguages)) && facts.AudioStatus != api.MetadataEvidenceStatusContradictory {
		facts.AudioStatus = api.MetadataEvidenceStatusPartial
	}
	return facts
}

// InspectedProgrammeStatus assesses finalized languages and roles in the inspected
// tracks, without asserting collection-wide coverage. Conflicts and clears remain unresolved.
func InspectedProgrammeStatus(facts api.LanguageFacts) api.MetadataEvidenceStatus {
	if facts.ProgrammeStatus == api.MetadataEvidenceStatusContradictory {
		return facts.ProgrammeStatus
	}
	if !knownLanguages(facts.ProgrammeLanguages) {
		return api.MetadataEvidenceStatusPartial
	}
	var languages []string
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio {
			continue
		}
		if track.Role == "" {
			return api.MetadataEvidenceStatusPartial
		}
		if track.Role == api.AudioRoleProgramme || track.Role == api.AudioRoleAlternateMix {
			if !knownLanguages(track.Languages) {
				return api.MetadataEvidenceStatusPartial
			}
			languages = append(languages, track.Languages...)
		}
	}
	if !sameLanguages(languageutil.NormalizeLanguageList(languages), facts.ProgrammeLanguages) {
		return api.MetadataEvidenceStatusPartial
	}
	return api.MetadataEvidenceStatusComplete
}

func knownLanguages(values []string) bool {
	return len(values) > 0 && !slices.ContainsFunc(values, func(value string) bool {
		code := languageutil.NormalizeLanguageCode(value)
		return code == "" || code == "und" || code == "mul"
	})
}

func sameLanguages(left, right []string) bool {
	return len(left) == len(right) && !slices.ContainsFunc(left, func(value string) bool { return !slices.Contains(right, value) })
}

func subtitleBaseLanguages(values []string) []string {
	languages := make([]string, 0, len(values))
	for _, value := range languageutil.NormalizeLanguageList(values) {
		base, _ := languageutil.SubtitleLanguageParts(value)
		languages = append(languages, base)
	}
	return languageutil.NormalizeLanguageList(languages)
}

func fullSubtitleLanguages(media api.MediaFacts) []string {
	var full []string
	forcedOnly := map[string]bool{}
	for _, value := range languageutil.NormalizeLanguageList(media.SubtitleLanguages) {
		language, coverage := languageutil.SubtitleLanguageParts(value)
		switch strings.ToLower(coverage) {
		case "full":
			full = append(full, language)
		case "forced":
			forcedOnly[language] = true
		}
	}
	for _, track := range media.Tracks {
		if track.Kind != api.MediaTrackSubtitle || track.Forced {
			continue
		}
		for _, value := range track.Languages {
			language, coverage := languageutil.SubtitleLanguageParts(value)
			if strings.EqualFold(coverage, "Forced") || (media.SubtitleLanguagesProvenance.IsManual() && forcedOnly[language]) {
				continue
			}
			if slices.Contains(subtitleBaseLanguages(media.SubtitleLanguages), language) {
				full = append(full, language)
			}
		}
	}
	for _, coverage := range media.HardcodedSubtitleCoverage {
		if coverage.Coverage == api.SubtitleCoverageFull {
			full = append(full, coverage.Language)
		}
	}
	return languageutil.NormalizeLanguageList(full)
}
