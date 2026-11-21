// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/metadata/seasonep"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{ID: "unit3d-sam-policy-v2", Check: checkRequirements}
}

func checkRequirements(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("check SAM rules: %w", err)
	}
	var failures []api.RuleFailure
	if prohibitedSource(subject) {
		failures = append(failures, strictFailure("sam_source", "CAM and R5 releases are prohibited", api.MetadataEvidenceStatusComplete))
	}
	if prohibitedContent(subject) {
		failures = append(failures, strictFailure("sam_content", "pornographic releases are prohibited", api.MetadataEvidenceStatusComplete))
	}
	if reason := packageFailure(subject); reason != "" {
		failures = append(failures, strictFailure("sam_package", reason, subject.PackageFacts.Status))
	}
	if subject.Identity.Category == api.CanonicalCategoryTV && subject.TVPack {
		if status, known := completedSeries(subject); !known {
			failures = append(failures, strictFailure("sam_pack_status", "season packs require a known series status", api.MetadataEvidenceStatusUnavailable))
		} else if !status {
			failures = append(
				failures,
				strictFailure("sam_pack_status", "season packs require an ended, cancelled, or completed series", api.MetadataEvidenceStatusComplete),
			)
		}
	}
	if reason := languageFailure(subject); reason != "" {
		failures = append(failures, strictFailure("sam_language", reason, subject.MediaFileFacts.LanguageStatus))
	}
	return failures, nil
}

func prohibitedContent(subject api.TrackerValidationSubject) bool {
	return slices.ContainsFunc(subject.EffectiveMetadata.Genres, func(genre string) bool {
		switch strings.ToLower(strings.TrimSpace(genre)) {
		case "porn", "pornography", "pornografia", "xxx":
			return true
		default:
			return false
		}
	})
}

func prohibitedSource(subject api.TrackerValidationSubject) bool {
	return subject.Assessments.CaptureSource != "" || metautil.ReleaseCaptureSource(subject.Source, subject.Region) != ""
}

func packageFailure(subject api.TrackerValidationSubject) string {
	facts := subject.PackageFacts
	if facts.Status != api.MetadataEvidenceStatusComplete {
		return "complete package evidence is required"
	}
	disc := trackers.IsFullDiscUpload(subject.DiscType, subject.Type)
	if facts.ArchiveFileCount > 0 || hasAuxiliaryVideo(subject) || disc && prohibitedDiscExtras(subject) ||
		!disc && facts.KnownFileCount > facts.MediaFileCount+facts.ExternalSubtitleFileCount {
		return "movie and TV uploads allow only the main content and subtitles"
	}
	if subject.Identity.Category == api.CanonicalCategoryMovie && facts.MediaFileCount != 1 && !disc {
		return "movie uploads require exactly one main video"
	}
	if subject.Identity.Category != api.CanonicalCategoryTV {
		return ""
	}

	if len(facts.DetectedSeasons) > 1 || len(facts.DetectedSeasons) == 1 && facts.DetectedSeasons[0] != subject.SeasonInt {
		return "TV uploads must contain only the selected season"
	}
	if subject.TVPack {
		if disc {
			return "a full-disc season pack requires verified season membership for every submitted programme"
		}
		if !verifiedSeasonPack(subject) {
			return "season packs require verified coverage of the selected season for every main video"
		}
		return ""
	}
	if !disc && facts.MediaFileCount != 1 || subject.MultipleEpisodes || slices.ContainsFunc(facts.DetectedEpisodes, func(season api.SeasonEpisodeFacts) bool {
		return len(season.Episodes) > 1
	}) {
		return "individual TV uploads must contain exactly one episode"
	}
	if slices.ContainsFunc(subject.FileList, func(file string) bool {
		return seasonep.IsVideoFile(file) && seasonep.SeasonLabelStatus(file, subject.SeasonInt, subject.EpisodeInt) == api.MetadataEvidenceStatusContradictory
	}) {
		return "TV uploads must contain only the selected season"
	}
	return ""
}

func verifiedSeasonPack(subject api.TrackerValidationSubject) bool {
	season := subject.SeasonInt
	sourceLabel := seasonep.SeasonLabelStatus(subject.SourcePath, season, 0)
	coverage := seasonep.Coverage(
		season,
		season > 0 || sourceLabel == api.MetadataEvidenceStatusComplete,
		subject.SourcePath,
		subject.FileList,
		subject.PackageFacts.Status,
	)
	if sourceLabel == api.MetadataEvidenceStatusContradictory || coverage.Status == api.MetadataEvidenceStatusContradictory {
		return false
	}
	// Parts may identify the season directly without using episode numbers.
	found := false
	seasonKnown := coverage.SeasonKnown
	for _, file := range subject.FileList {
		if !seasonep.IsVideoFile(file) {
			continue
		}
		found = true
		label := seasonep.SeasonLabelStatus(file, season, 0)
		if label == api.MetadataEvidenceStatusContradictory {
			return false
		}
		if label == api.MetadataEvidenceStatusComplete {
			seasonKnown = true
			continue
		}
		if coverage.Status != api.MetadataEvidenceStatusComplete &&
			seasonep.Coverage(season, true, "", []string{file}, subject.PackageFacts.Status).Status != api.MetadataEvidenceStatusComplete {
			return false
		}
	}
	return found && seasonKnown
}

func prohibitedDiscExtras(subject api.TrackerValidationSubject) bool {
	facts := subject.PackageFacts
	if facts.ExternalFileCount != 0 {
		return true
	}
	if facts.KnownFileCount == 1 && slices.Contains(facts.Extensions, ".iso") {
		return false
	}
	return slices.ContainsFunc(subject.FileList, func(file string) bool {
		extension := strings.ToLower(filepath.Ext(file))
		switch extension {
		case ".nfo", ".bat", ".cmd", ".com", ".exe", ".msi", ".ps1", ".scr":
			return true
		case ".ass", ".idx", ".smi", ".srt", ".ssa", ".sub", ".sup", ".vtt":
			return false
		default:
			return !discLayoutFile(subject.SourcePath, subject.DiscType, file, extension)
		}
	})
}

func hasAuxiliaryVideo(subject api.TrackerValidationSubject) bool {
	return slices.ContainsFunc(subject.FileList, func(file string) bool {
		if !seasonep.IsVideoFile(file) {
			return false
		}
		stem := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		words := fileNameWords(stem)
		if len(words) == 0 || words[len(words)-1] != "sample" && words[len(words)-1] != "proof" {
			return false
		}
		for _, title := range []string{subject.Release.Title, subject.EpisodeTitle} {
			titleWords := fileNameWords(title)
			if len(titleWords) > 0 && len(words) >= len(titleWords) && slices.Equal(words[len(words)-len(titleWords):], titleWords) {
				return false
			}
		}
		return true
	})
}

func fileNameWords(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return r == '.' || r == '_' || r == '-' || r == ' '
	})
}

func discLayoutFile(source, discType, file, extension string) bool {
	relative, err := filepath.Rel(source, file)
	if err != nil {
		return false
	}
	switch strings.ToUpper(discType) {
	case "DVD":
		if extension == ".aob" {
			return discFolder(source, relative, "AUDIO_TS")
		}
		return (extension == ".ifo" || extension == ".bup" || extension == ".vob") && discFolder(source, relative, "VIDEO_TS")
	case "BDMV":
		return bluRayLayoutFile(source, relative, extension)
	case "HDDVD":
		// Author-supplied advanced navigation assets may use arbitrary filenames.
		if discFolder(source, relative, "ADV_OBJ") {
			return !seasonep.IsVideoFile(file) || extension == ".evo"
		}
		switch extension {
		case ".evo", ".ifo", ".bup", ".map", ".vti":
			return discFolder(source, relative, "HVDVD_TS")
		default:
			return discProtectionFile(source, relative, extension)
		}
	default:
		return false
	}
}

func bluRayLayoutFile(source, relative, extension string) bool {
	if discProtectionFile(source, relative, extension) {
		return true
	}
	if discFolder(source, relative, "CERTIFICATE") {
		return extension == ".bdmv" || extension == ".cert"
	}
	if !discFolder(source, relative, "BDMV") {
		return false
	}
	// Match disc assets to the logical directories defined for their format.
	// See the BDMV layout in https://aacsla.com/wp-content/uploads/2019/02/AACS_Spec_BD_Prerecorded.921.pdf.
	switch extension {
	case ".bdmv", ".bdm":
		return true
	case ".mpls", ".mpl":
		return discFolder(source, relative, "PLAYLIST")
	case ".clpi", ".cpi":
		return discFolder(source, relative, "CLIPINF")
	case ".m2ts", ".mts", ".ssif":
		return discFolder(source, relative, "STREAM")
	case ".bdjo":
		return discFolder(source, relative, "BDJO")
	case ".jar":
		return discFolder(source, relative, "JAR")
	case ".otf", ".ttf":
		return discFolder(source, relative, "AUXDATA")
	case ".xml", ".png", ".jpg", ".jpeg":
		return discFolder(source, relative, "META")
	default:
		return false
	}
}

func discProtectionFile(source, relative, extension string) bool {
	switch extension {
	case ".cer", ".cert", ".inf", ".tbl", ".bin", ".dat", ".sig", ".svm":
		return discFolder(source, relative, "AACS", "BDSVM")
	default:
		return false
	}
}

func discFolder(source, relative string, names ...string) bool {
	if slices.Contains(names, strings.ToUpper(filepath.Base(source))) {
		return true
	}
	for folder := filepath.Dir(relative); folder != "."; folder = filepath.Dir(folder) {
		if slices.Contains(names, strings.ToUpper(filepath.Base(folder))) {
			return true
		}
	}
	return false
}

func completedSeries(subject api.TrackerValidationSubject) (ended, known bool) {
	metadata := subject.ProviderMetadata
	if !metadata.IsCurrentFor(subject.SourcePath, subject.Identity) {
		return false, false
	}
	identity := subject.Identity
	var statuses []string
	if data := currentSAMTMDB(subject.SourcePath, identity, metadata); data != nil && data.SeriesStatus != nil {
		statuses = append(statuses, *data.SeriesStatus)
	}
	if metadata.TVDB != nil && identity.TVDBID > 0 && metadata.TVDB.TVDBID == identity.TVDBID {
		statuses = append(statuses, metadata.TVDB.Status)
	}
	if metadata.TVmaze != nil && identity.TVmazeID > 0 && metadata.TVmaze.TVmazeID == identity.TVmazeID {
		statuses = append(statuses, metadata.TVmaze.Status)
	}
	for _, status := range statuses {
		switch strings.ToLower(strings.TrimSpace(status)) {
		case "":
			continue
		case "ended", "cancelled", "canceled", "completed", "complete":
			known = true
		default:
			return false, true
		}
	}
	return known, known
}

func languageFailure(subject api.TrackerValidationSubject) string {
	languages := subject.LanguageFacts
	if !languages.OriginalLanguagesKnown || len(languages.OriginalLanguages) == 0 {
		return "original-language evidence is required"
	}
	if languages.AudioAbsent && languageutil.ContainsLanguage(languages.OriginalLanguages, "zxx") {
		return ""
	}
	requiresSubtitles := slices.ContainsFunc(languages.OriginalLanguages, func(language string) bool {
		return !languageutil.SameLanguage(language, "pt")
	})
	if trackers.IsFullDiscUpload(subject.DiscType, subject.Type) {
		return discLanguageFailure(subject, requiresSubtitles)
	}
	files := subject.MediaFileFacts
	if files.ExpectedFileCount == 0 || len(files.Files) != files.ExpectedFileCount {
		return "audio and subtitle evidence is required for every main video"
	}
	if subject.ManualLanguages.AggregateAudio {
		if !primaryAudioCorrectionMatches(languages, files, subject.ManualLanguages.Audio) {
			return "the corrected audio languages must match the inspected primary video"
		}
	} else if !usableProgrammeEvidence(languages) {
		return "original programme audio evidence is missing or contradictory"
	}
	hardcoded := files.ExpectedFileCount == 1 && hasHardcodedBrazilianSubtitles(subject)
	for _, file := range files.Files {
		if file.VideoTrackCount <= 0 {
			return "each programme resource requires inspected video evidence"
		}
		if !hasDefaultOriginalAudio(languages, file.ResourceID) {
			return "each main video must have original programme audio set as the default"
		}
		if requiresSubtitles && !hardcoded && !hasBrazilianSubtitles(subject, file) {
			return "each non-Portuguese main video requires Brazilian Portuguese subtitles"
		}
	}
	return ""
}

func hasBrazilianSubtitles(subject api.TrackerValidationSubject, file api.MediaFileFact) bool {
	knownBrazilian := hasBrazilianPortuguese(file.SubtitleLanguages)
	if file.Primary && subject.ManualLanguages.AggregateSubtitles {
		return knownBrazilian
	}
	if !knownBrazilian && !languageutil.ContainsLanguage(file.SubtitleLanguages, "pt") {
		return false
	}
	foundTracks := false
	for _, track := range subject.LanguageFacts.Tracks {
		if track.Kind != api.MediaTrackSubtitle || track.ResourceID != file.ResourceID {
			continue
		}
		foundTracks = true
		for _, value := range track.Languages {
			if track.LanguageProvenance.IsManual() {
				if isBrazilianPortuguese(value) {
					return true
				}
				continue
			}
			if resolved, ok := regionalTrackLanguage(value, track.Title); ok && isBrazilianPortuguese(resolved) {
				return true
			}
		}
	}
	return !foundTracks && knownBrazilian
}

func discLanguageFailure(subject api.TrackerValidationSubject, requiresSubtitles bool) string {
	if len(subject.Disc.Items) > 1 || subject.Disc.PlaylistCount > 1 {
		return "audio and subtitle evidence must identify one selected disc programme"
	}
	languages := subject.LanguageFacts
	if len(subject.AudioLanguages) == 0 || languages.ProgrammeStatus == api.MetadataEvidenceStatusContradictory {
		return "the selected disc programme requires original audio evidence"
	}
	original := false
	brazilianSubtitles := hasBrazilianPortuguese(subject.SubtitleLanguages)
	if strings.EqualFold(subject.DiscType, "BDMV") {
		// The prepared audio summary covers the selected BDInfo playlist.
		original = slices.ContainsFunc(subject.AudioLanguages, func(language string) bool {
			return languageutil.ContainsLanguage(languages.OriginalLanguages, language)
		})
	} else if usableProgrammeEvidence(languages) && languages.PrimaryAudioTrackID != "" {
		primary := slices.IndexFunc(languages.Tracks, func(track api.MediaTrackFacts) bool {
			return track.Kind == api.MediaTrackAudio && track.ID == languages.PrimaryAudioTrackID
		})
		if primary >= 0 && languages.Tracks[primary].ResourceID != "" {
			resourceID := languages.Tracks[primary].ResourceID
			original = slices.ContainsFunc(languages.Tracks, func(track api.MediaTrackFacts) bool {
				return track.ResourceID == resourceID && languages.ProgrammeAudioOrigin(track) == api.TrackAudioOriginal
			})
			brazilianSubtitles = hasBrazilianSubtitles(subject, api.MediaFileFact{
				ResourceID:        resourceID,
				Primary:           true,
				SubtitleLanguages: subject.SubtitleLanguages,
			})
		}
	}
	if !original {
		return "the selected disc programme requires original audio"
	}
	if requiresSubtitles && !brazilianSubtitles && !hasHardcodedBrazilianSubtitles(subject) {
		return "the selected non-Portuguese disc programme requires Brazilian Portuguese subtitles"
	}
	return ""
}

func hasHardcodedBrazilianSubtitles(subject api.TrackerValidationSubject) bool {
	return subject.HardcodedSubs && hasBrazilianPortuguese(subject.HardcodedSubtitleLanguages)
}

func hasDefaultOriginalAudio(facts api.LanguageFacts, resourceID string) bool {
	if resourceID == "" {
		return false
	}
	audioCount, originalCount := 0, 0
	originalDefault, defaultsKnown := false, true
	for _, track := range facts.Tracks {
		if track.Kind != api.MediaTrackAudio || track.ResourceID != resourceID {
			continue
		}
		original := facts.ProgrammeAudioOrigin(track) == api.TrackAudioOriginal
		audioCount++
		if original {
			originalCount++
		}
		if track.DefaultKnown && track.Default && !original {
			return false
		}
		defaultsKnown = defaultsKnown && track.DefaultKnown
		originalDefault = originalDefault || track.DefaultKnown && track.Default && original
	}
	if audioCount == 1 && !defaultsKnown {
		return originalCount > 0
	}
	return defaultsKnown && originalDefault
}

func strictFailure(rule, reason string, status api.MetadataEvidenceStatus) api.RuleFailure {
	return trackers.NewEvidenceRuleFailure(rule, reason, api.RuleDispositionStrict, status)
}
