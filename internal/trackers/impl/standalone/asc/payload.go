// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

// uploadPayload holds the `POST /torrents` form fields. attributeIDs are sent
// as repeated `attribute_ids[]` values and screenshot paths as repeated
// `screenshot_paths[]`; other fields are single-valued. qualityID, containerID
// and genreIDs repeat their attribute_ids entries so validation can check the
// payload that will actually be sent.
type uploadPayload struct {
	fields       map[string]string
	attributeIDs []string
	qualityID    string
	containerID  string
	genreIDs     []string
}

// buildPayload maps resolved facts to the form fields. Movies, series and
// anime share one site profile, so every video category sends the same shape.
func buildPayload(
	meta api.UploadSubject,
	trackerCfg config.TrackerConfig,
	description string,
	releaseName string,
	mediaInfo string,
) uploadPayload {
	answers := standalone.QuestionnaireAnswers(meta, "ASC")
	audioID := resolveAudio(meta)
	resolution := resolveResolution(meta)
	payload := uploadPayload{fields: map[string]string{
		"category_id": resolveCategoryID(meta),
		"name":        releaseName,
		"year":        intString(resolveYear(meta)),
		"description": description,
		"anonymous":   anonymousFlag(trackerCfg.Anon),
		"subtitle":    resolveSubtitle(meta, audioID),
		"imdb_id":     resolveIMDbIDText(meta),
		"width":       resolution["width"],
		"height":      resolution["height"],
		"trailer_url": resolveTrailer(meta),
		"mediainfo":   mediaInfo,
	}}
	payload.genreIDs = resolveGenreIDs(resolveGenres(meta, answers))
	payload.qualityID = resolveQualityID(meta)
	payload.containerID = resolveContainer(meta)
	attributes := make([]string, 0, 6+len(payload.genreIDs))
	attributes = append(attributes,
		resolveLanguage(meta),
		payload.qualityID,
		audioID,
		payload.containerID,
		resolveVideoCodecID(meta),
		resolveAudioCodecID(meta),
	)
	attributes = append(attributes, payload.genreIDs...)
	for _, id := range attributes {
		if strings.TrimSpace(id) != "" {
			payload.attributeIDs = append(payload.attributeIDs, id)
		}
	}
	return payload
}

// multipartFields returns the form values in the encoding Laravel expects.
// screenshotPaths are the server paths returned by `/torrents/screenshots`.
func (p uploadPayload) multipartFields(screenshotPaths []string) map[string][]string {
	out := make(map[string][]string, len(p.fields)+2)
	for key, value := range p.fields {
		out[key] = []string{value}
	}
	out["attribute_ids[]"] = append([]string(nil), p.attributeIDs...)
	out["screenshot_paths[]"] = append([]string(nil), screenshotPaths...)
	return out
}

// previewFields flattens the payload for dry-run display. The description is
// shown in its own preview section and the MediaInfo report is omitted for size.
func (p uploadPayload) previewFields() map[string]string {
	out := make(map[string]string, len(p.fields)+1)
	for key, value := range p.fields {
		if key == "description" || key == "mediainfo" {
			continue
		}
		out[key] = value
	}
	out["attribute_ids[]"] = strings.Join(p.attributeIDs, ",")
	return out
}

func anonymousFlag(anon bool) string {
	if anon {
		return "1"
	}
	return "0"
}
