// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone"
	"github.com/autobrr/upbrr/pkg/api"
)

const screenshotJPEGQuality = 90

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
	audioID := resolveAudioID(meta)
	resolution := resolveResolution(meta)
	payload := uploadPayload{fields: map[string]string{
		"category_id": resolveCategoryID(meta),
		"name":        releaseName,
		"year":        yearText(resolveYear(meta)),
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
	payload.containerID = resolveContainerID(meta)
	attributes := make([]string, 0, 6+len(payload.genreIDs))
	attributes = append(attributes,
		resolveLanguageID(meta),
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

// selectScreenshots returns the local paths of up to maxScreenshots non-menu
// screenshots with extensions the site accepts.
func selectScreenshots(assets trackers.DescriptionAssets) []string {
	paths := make([]string, 0, maxScreenshots)
	for _, image := range assets.Screenshots {
		if len(paths) == maxScreenshots {
			break
		}
		path := strings.TrimSpace(image.Path)
		if path == "" || image.Purpose == api.ScreenshotPurposeMenu || !isUploadableImage(path) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

// loadScreenshotFiles reads screenshots as `/torrents/screenshots` "image"
// fields. Files over maxImageBytes are re-encoded as JPEG (PNG or JPEG input
// only) and rejected if still too large.
func loadScreenshotFiles(paths []string) ([]commonhttp.FileField, error) {
	files := make([]commonhttp.FileField, 0, len(paths))
	for idx, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("trackers: ASC read screenshot %d: %w", idx+1, err)
		}
		name := filepath.Base(path)
		if len(data) > maxImageBytes {
			data, err = reencodeAsJPEG(data)
			if err != nil {
				return nil, fmt.Errorf("trackers: ASC shrink screenshot %d: %w", idx+1, err)
			}
			name = strings.TrimSuffix(name, filepath.Ext(name)) + ".jpg"
		}
		if len(data) > maxImageBytes {
			return nil, fmt.Errorf("trackers: ASC screenshot %d exceeds %d bytes", idx+1, maxImageBytes)
		}
		files = append(files, commonhttp.FileField{
			FieldName: "image",
			FileName:  name,
			Content:   data,
		})
	}
	return files, nil
}

func reencodeAsJPEG(data []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		img, _, err = image.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("decode image: %w", err)
		}
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, img, &jpeg.Options{Quality: screenshotJPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return out.Bytes(), nil
}

// isUploadableImage reports whether the site accepts an image by extension.
func isUploadableImage(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

func anonymousFlag(anon bool) string {
	if anon {
		return "1"
	}
	return "0"
}

func yearText(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}
