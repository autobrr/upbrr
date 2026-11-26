// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

func validationPolicy() trackers.ValidationPolicyBinding {
	return trackers.ValidationPolicyBinding{
		ID: "standalone-asc-constructibility-v2",
		Check: func(ctx context.Context, subject api.TrackerValidationSubject, _ api.Logger) ([]api.RuleFailure, error) {
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("context canceled: %w", err)
			}
			meta := standalone.UploadSubjectForValidation(subject)
			answers := standalone.QuestionnaireAnswers(meta, "ASC")
			failures := make([]api.RuleFailure, 0, 4)
			if !meta.Anime && strings.TrimSpace(resolveIMDbIDText(meta)) == "" {
				failures = append(failures, trackers.NewRuleFailure(
					"required_provider_id",
					"missing IMDb ID",
					api.RuleDispositionStrict,
				))
			}
			if strings.TrimSpace(resolvePoster(meta)) == "" {
				failures = append(failures, trackers.NewRuleFailure("required_poster", "missing poster URL", api.RuleDispositionStrict))
			}
			if len(resolveGenreIDs(resolveGenres(meta, answers))) == 0 {
				failures = append(failures, trackers.NewQuestionnaireRuleFailure("required_genre", "missing genre known to ASC"))
			}
			if strings.TrimSpace(resolveOverview(meta, answers)) == "" {
				failures = append(failures, trackers.NewQuestionnaireRuleFailure("required_overview", "missing overview"))
			}
			return failures, nil
		},
	}
}

// validatePayloadFields checks prepared payload constructibility and returns
// a blocking reason, or "" when the site's required fields are satisfied.
// Movies, series and anime share the site's video profile requirements.
func validatePayloadFields(meta api.UploadSubject, payload uploadPayload, screenshotCount int, coverURL string) string {
	fields := payload.fields
	switch {
	case fields["category_id"] == "":
		return "missing supported ASC category"
	case !meta.Anime && strings.TrimSpace(fields["imdb_id"]) == "":
		return "missing IMDb ID"
	case strings.TrimSpace(fields["year"]) == "":
		return "missing year"
	case payload.qualityID == "":
		return "release type or source has no ASC quality"
	case payload.containerID == "":
		return "missing container"
	case strings.TrimSpace(resolveOverview(meta, standalone.QuestionnaireAnswers(meta, "ASC"))) == "":
		return "missing overview"
	case strings.TrimSpace(fields["description"]) == "":
		return "missing description"
	case hasForeignImages(fields["description"]):
		return "description embeds images hosted outside ASC"
	case strings.TrimSpace(fields["mediainfo"]) == "":
		return "missing MediaInfo report"
	case len(payload.genreIDs) == 0:
		return "missing genre known to ASC"
	case strings.TrimSpace(fields["width"]) == "" || strings.TrimSpace(fields["height"]) == "":
		return "missing video resolution"
	case strings.TrimSpace(coverURL) == "":
		return "missing poster URL"
	case screenshotCount < minScreenshots:
		return fmt.Sprintf("needs at least %d local screenshots, found %d", minScreenshots, screenshotCount)
	}
	return ""
}

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
		case ".mkv", ".mp4", ".m4v", ".avi", ".ts", ".m2ts", ".mpg", ".mpeg", ".wmv", ".mov", ".webm", ".vob":
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
