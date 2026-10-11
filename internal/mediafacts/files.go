// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediafacts

import (
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// ResolveFiles finalizes the primary file from current corrected release facts.
// Other files retain their own inspected evidence; primary corrections are not
// assertions about every episode in a pack.
func ResolveFiles(media api.MediaFacts, resolution string) api.MediaFileFacts {
	facts := media.MediaFileFacts.Clone()
	if len(facts.Files) == 0 {
		return facts
	}
	facts.OriginalLanguage = media.OriginalLanguage
	for i := range facts.Files {
		file := &facts.Files[i]
		if !file.Primary {
			continue
		}
		file.Container, file.Source, file.Resolution = media.Container, media.Source, resolution
		if strings.EqualFold(file.Container, "m2ts") {
			file.Container = "ts"
		}
		file.VideoCodec, file.VideoEncode, file.BitDepth = media.VideoCodec, media.VideoEncode, media.BitDepth
		file.AudioLanguages = slices.Clone(media.AudioLanguages)
		file.SubtitleLanguages = slices.Clone(media.SubtitleLanguages)
		// A complete report is needed to establish empty channels. Explicit clears
		// withdraw evidence and cannot restore the raw inspected language list.
		inspected := file.VideoTrackCount > 0
		file.AudioStatus = fileLanguageStatus(
			media.Tracks,
			api.MediaTrackAudio,
			media.AudioLanguages,
			media.AudioLanguagesProvenance,
			inspected,
			file.AudioStatus,
		)
		if media.LanguageFacts.ProgrammeStatus == api.MetadataEvidenceStatusContradictory {
			file.AudioStatus = api.MetadataEvidenceStatusContradictory
		}
		file.SubtitleStatus = fileLanguageStatus(
			media.Tracks,
			api.MediaTrackSubtitle,
			media.SubtitleLanguages,
			media.SubtitleLanguagesProvenance,
			inspected,
			file.SubtitleStatus,
		)
	}
	return SummarizeFiles(facts)
}

func fileLanguageStatus(
	tracks []api.MediaTrackFacts,
	kind api.MediaTrackKind,
	languages []string,
	provenance api.FactProvenance,
	inspected bool,
	collectedStatus api.MetadataEvidenceStatus,
) api.MetadataEvidenceStatus {
	if !inspected || collectedStatus == api.MetadataEvidenceStatusUnavailable {
		return api.MetadataEvidenceStatusUnavailable
	}
	if provenance == api.FactProvenanceManualEmpty {
		return api.MetadataEvidenceStatusPartial
	}
	if provenance == api.FactProvenanceManual {
		if knownLanguages(languages) {
			return api.MetadataEvidenceStatusComplete
		}
		return api.MetadataEvidenceStatusPartial
	}
	for _, track := range tracks {
		if track.Kind == kind && (kind != api.MediaTrackAudio || !track.Commentary) && !knownLanguages(track.Languages) {
			return api.MetadataEvidenceStatusPartial
		}
	}
	return api.MetadataEvidenceStatusComplete
}

// SummarizeFiles describes the completeness of measured all-file evidence.
// VideoEncode is optional because remuxes need not identify an encoder.
func SummarizeFiles(facts api.MediaFileFacts) api.MediaFileFacts {
	facts.Status, facts.TechnicalStatus, facts.LanguageStatus = api.MetadataEvidenceStatusUnavailable, api.MetadataEvidenceStatusUnavailable, api.MetadataEvidenceStatusUnavailable
	if len(facts.Files) == 0 {
		return facts
	}
	facts.Status, facts.TechnicalStatus, facts.LanguageStatus = api.MetadataEvidenceStatusComplete, api.MetadataEvidenceStatusComplete, api.MetadataEvidenceStatusComplete
	if facts.ExpectedFileCount <= 0 || len(facts.Files) != facts.ExpectedFileCount {
		facts.TechnicalStatus, facts.LanguageStatus = api.MetadataEvidenceStatusPartial, api.MetadataEvidenceStatusPartial
	}
	for _, file := range facts.Files {
		if file.VideoTrackCount <= 0 || strings.TrimSpace(file.Container) == "" || strings.TrimSpace(file.Source) == "" ||
			strings.TrimSpace(file.Resolution) == "" ||
			strings.TrimSpace(file.VideoCodec) == "" ||
			strings.TrimSpace(file.BitDepth) == "" ||
			file.BitDepth == "0" {
			facts.TechnicalStatus = api.MetadataEvidenceStatusPartial
		}
		if file.AudioStatus != api.MetadataEvidenceStatusComplete || file.SubtitleStatus != api.MetadataEvidenceStatusComplete {
			facts.LanguageStatus = api.MetadataEvidenceStatusPartial
		}
	}
	if facts.TechnicalStatus != api.MetadataEvidenceStatusComplete || facts.LanguageStatus != api.MetadataEvidenceStatusComplete {
		facts.Status = api.MetadataEvidenceStatusPartial
	}
	return facts
}
