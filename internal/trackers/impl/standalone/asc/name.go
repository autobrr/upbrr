// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	pathutil "github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var ascContentResolution = regexp.MustCompile(`(?i)[._ ](?:2160|1440|1080|720|576|540|480)[pi][._ -]`)
var ascContentVideo = regexp.MustCompile(`(?i)[._ ](?:H[._ ]?26[45]|x26[45]|HEVC|AVC|AV1|VP9|XviD|DivX|VC-1|MPEG-?2)(?:[._ -]|$)`)

var ascContentAudio = regexp.MustCompile(
	`(?i)(?:^|[._ -])(?:DDPA?\+?|DD\+?|AAC|E-?AC-?3|AC-?3|DTS(?:-HD|-X|-ES)?|TrueHD|FLAC|L?PCM|OPUS|MP3|Atmos)(?:\d\.?\d?)?(?:[._ -]|$)`,
)

func validateASCVideoNames(info metainfo.Info, mediaInfo, discType string) error {
	if discType != "" {
		return nil
	}
	videoNames := make(map[string]bool)
	for _, file := range info.UpvertedFiles() {
		name := info.BestName()
		if path := file.BestPath(); len(path) > 0 {
			name = path[len(path)-1]
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".mpg", ".mpeg", ".wmv", ".mov", ".webm":
		default:
			continue
		}
		resolution := ascContentResolution.FindStringIndex(name)
		if resolution == nil {
			return errors.New("video filename must include ASC resolution, audio and video codec tokens")
		}
		tail := name[resolution[1]-1:]
		video := ascContentVideo.FindStringIndex(tail)
		if video == nil || !ascContentAudio.MatchString(tail[:video[0]]) {
			return errors.New("video filename must include the ASC audio token before the video codec")
		}
		videoNames[name] = true
	}
	if len(videoNames) == 0 {
		return errors.New("no video file in the prepared torrent")
	}
	for _, match := range ascReportedName.FindAllStringSubmatch(mediaInfo, -1) {
		if !videoNames[strings.TrimSpace(match[1])] {
			return errors.New("MediaInfo filename does not match the prepared torrent")
		}
	}
	return nil
}

func resolveUploadTitle(meta api.UploadSubject) string {
	base := resolveDisplayTitle(meta)
	if categoryOf(meta) == "TV" {
		seasonEpisode := metautil.FirstNonEmptyTrimmed(
			meta.DailyEpisodeDate,
			strings.TrimSpace(meta.SeasonStr)+strings.TrimSpace(meta.EpisodeStr),
			seasonEpisodeText(meta),
		)
		if seasonEpisode != "" {
			base += " - " + seasonEpisode
		}
		return strings.TrimSpace(base + " " + meta.EpisodeCoverage.Part)
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
