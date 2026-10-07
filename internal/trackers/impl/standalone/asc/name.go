// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveUploadTitle(meta api.UploadSubject) string {
	base := resolveDisplayTitle(meta)
	if categoryOf(meta) == "TV" {
		seasonEpisode := metautil.FirstNonEmptyTrimmed(
			meta.DailyEpisodeDate,
			strings.TrimSpace(meta.SeasonStr)+strings.TrimSpace(meta.EpisodeStr),
			seasonEpisodeText(meta),
		)
		if seasonEpisode != "" {
			return strings.TrimSpace(base + " - " + seasonEpisode)
		}
	}
	return base
}

func resolveDisplayTitle(meta api.UploadSubject) string {
	ptBR := api.ExtractTrackerLocalizedPTBR(meta)
	main := strings.TrimSpace(meta.Release.Title)
	alt := ""
	if tmdb := meta.ProviderMetadata.TMDB; tmdb != nil {
		main = strings.TrimSpace(metautil.FirstNonEmptyTrimmed(ptBR.Title, tmdb.Title, meta.Release.Title))
		if categoryOf(meta) == "TV" {
			alt = strings.TrimSpace(metautil.FirstNonEmptyTrimmed(tmdb.Title, meta.Release.Title))
		} else {
			alt = strings.TrimSpace(tmdb.OriginalTitle)
		}
	}
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		main = trackers.PreferredTitle(meta, "")
	}
	if meta.EffectiveMetadata.OriginalTitleProvenance.IsManual() {
		alt = trackers.PreferredOriginalTitle(meta, "")
	}
	if meta.NamePresentation.Version == api.ReleaseNamePresentationVersionV1 && meta.NamePresentation.OmitAlternateTitle {
		alt = ""
	}
	if main != "" && alt != "" && !strings.EqualFold(main, alt) {
		return main + " (" + alt + ")"
	}
	if main != "" {
		return main
	}
	if meta.EffectiveMetadata.TitleProvenance.IsManual() {
		return ""
	}
	return strings.TrimSpace(metautil.FirstNonEmptyTrimmed(meta.ReleaseName, pathutil.Base(meta.SourcePath)))
}
func resolveSearchTitle(meta api.UploadSubject) string {
	if title := strings.TrimSpace(meta.Release.Title); title != "" {
		return title
	}
	if name := strings.TrimSpace(meta.ReleaseName); name != "" {
		return name
	}
	return strings.TrimSpace(meta.SourcePath)
}

func seasonEpisodeText(meta api.UploadSubject) string {
	if meta.EpisodeInt > 0 {
		return fmt.Sprintf("S%02dE%02d", meta.SeasonInt, meta.EpisodeInt)
	}
	if meta.SeasonInt > 0 {
		return fmt.Sprintf("S%02d", meta.SeasonInt)
	}
	return ""
}

var (
	fileNameResolutionPattern = regexp.MustCompile(`(?i)[._ ](?:2160|1440|1080|720|576|540|480)[pi](?:[._ -]|$)`)
	fileNameVideoPattern      = regexp.MustCompile(`(?i)([._ ])(?:H[._ ]?26[45]|x26[45]|HEVC|AVC|AV1|VP9|XviD|DivX|VC-1|MPEG-?2)(?:[._ -]|$)`)
	fileNameAudioPattern      = regexp.MustCompile(
		`(?i)(?:^|[._ -])(?:DDP?\+?|AAC|AC3|E-?AC-?3|DTS(?:-HD|-X|-ES)?|TrueHD|FLAC|L?PCM|OPUS|MP3|Atmos)(?:\d(?:\.\d)?)?(?:[._ -]|$)`,
	)
)

// fileNameExtensions lists the container extensions stripped before the file
// name is analysed; folder names never carry one.
var fileNameExtensions = map[string]struct{}{
	".mkv":  {},
	".mp4":  {},
	".m4v":  {},
	".avi":  {},
	".ts":   {},
	".m2ts": {},
	".mpg":  {},
	".mpeg": {},
	".wmv":  {},
	".mov":  {},
}

// complianceFileName returns the name the site requires for a release file or
// folder: the primary audio token placed between the source and the video
// codec (`…1080p.DSNP.WEB-DL.DDP5.1.H.264-GRP`). The original name is returned
// unchanged when it already carries an audio token, when no audio token can be
// derived from the finalized media facts, or when no resolution or video codec
// token anchors a safe insertion point. It never reorders or drops existing tokens.
func complianceFileName(meta api.UploadSubject, name string) string {
	token := audioFileNameToken(meta)
	if token == "" {
		return name
	}
	stem, ext := name, ""
	if dot := strings.LastIndex(name, "."); dot > 0 {
		if _, ok := fileNameExtensions[strings.ToLower(name[dot:])]; ok {
			stem, ext = name[:dot], name[dot:]
		}
	}
	resolution := fileNameResolutionPattern.FindStringIndex(stem)
	if resolution == nil {
		return name
	}
	tail := stem[resolution[0]:]
	if fileNameAudioPattern.MatchString(tail) {
		return name
	}
	video := fileNameVideoPattern.FindStringSubmatchIndex(tail)
	if video == nil {
		return name
	}
	// Group 1 is the separator preceding the codec; insert right after it.
	insertAt := resolution[0] + video[3]
	separator := stem[resolution[0]+video[2] : insertAt]
	return stem[:insertAt] + token + separator + stem[insertAt:] + ext
}
