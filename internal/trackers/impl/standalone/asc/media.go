// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/commonhttp"
	"github.com/autobrr/upbrr/pkg/api"
)

var ascReportedName = regexp.MustCompile(`(?m)^Complete name[ \t]*:[ \t]*(.+)$`)

func ascUploadMediaInfo(report string) string {
	return strings.TrimSpace(ascReportedName.ReplaceAllStringFunc(report, func(line string) string {
		name := strings.TrimSpace(ascReportedName.FindStringSubmatch(line)[1])
		return "Complete name : " + name[strings.LastIndexAny(name, `/\`)+1:]
	}))
}

func resolveResolution(meta api.UploadSubject) map[string]string {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		heightStr := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(meta.Release.Resolution), "p"), "i")
		heightNum, err := strconv.Atoi(heightStr)
		if err == nil && heightNum > 0 {
			widthNum := int(math.Round((16.0 / 9.0) * float64(heightNum)))
			return map[string]string{
				"width":  strconv.Itoa(widthNum),
				"height": strconv.Itoa(heightNum),
			}
		}
	}

	if meta.MediaInfoJSONPath != "" {
		if payload, err := os.ReadFile(meta.MediaInfoJSONPath); err == nil {
			type mediaInfoDoc struct {
				Media struct {
					Track []map[string]any `json:"track"`
				} `json:"media"`
			}
			var doc mediaInfoDoc
			if err := json.Unmarshal(payload, &doc); err == nil {
				for _, track := range doc.Media.Track {
					trackType, _ := track["@type"].(string)
					if strings.ToLower(trackType) == "video" {
						widthVal := track["Width"]
						heightVal := track["Height"]

						widthStr := parseDimensionStr(widthVal)
						heightStr := parseDimensionStr(heightVal)

						if widthStr != "" && heightStr != "" {
							return map[string]string{
								"width":  widthStr,
								"height": heightStr,
							}
						}
					}
				}
			}
		}
	}

	height := parseResolutionHeight(meta.Release.Resolution)
	width := 0
	if height > 0 {
		width = int(float64(height) * (16.0 / 9.0))
	}
	return map[string]string{
		"width":  intString(width),
		"height": intString(height),
	}
}

func parseResolutionHeight(value string) int {
	re := regexp.MustCompile(`(?i)(\d{3,4})(?:p|i)`)
	matches := re.FindStringSubmatch(value)
	if len(matches) != 2 {
		return 0
	}
	height, _ := strconv.Atoi(matches[1])
	return height
}

func intString(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func readTextFile(path string) (string, error) {
	payload, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("trackers: ASC read text file: %w", err)
	}
	return strings.ReplaceAll(string(payload), "\r", ""), nil
}

func readTextFileNoErr(path string) string {
	value, _ := readTextFile(path)
	return value
}

func parseDimensionStr(val any) string {
	return metautil.ParseDimensionStr(val)
}

func screenshotPaths(assets trackers.DescriptionAssets) []string {
	paths := make([]string, 0, maxScreenshots)
	seen := make(map[string]bool, maxScreenshots)
	for _, image := range assets.Screenshots {
		path := strings.TrimSpace(image.Path)
		if path == "" || image.Purpose == api.ScreenshotPurposeMenu || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
		if len(paths) == maxScreenshots {
			break
		}
	}
	return paths
}

func readScreenshot(path string) (commonhttp.FileField, error) {
	f, err := os.Open(path)
	if err != nil {
		return commonhttp.FileField{}, errors.New("cannot open local image")
	}
	defer f.Close()
	contents, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil {
		return commonhttp.FileField{}, errors.New("cannot read local image")
	}
	ext := imageExtension(contents)
	if len(contents) > maxImageBytes || ext == "" {
		return commonhttp.FileField{}, errors.New("use JPEG, PNG or WebP images up to 5 MiB")
	}
	return commonhttp.FileField{
		FieldName: "image",
		FileName:  "screenshot" + ext,
		Content:   contents,
	}, nil
}

func validCoverURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "image.tmdb.org", "m.media-amazon.com", "artworks.thetvdb.com", "static.tvmaze.com":
		return true
	default:
		return false
	}
}

func fetchCover(ctx context.Context, client *http.Client, raw string) (commonhttp.FileField, error) {
	if !validCoverURL(raw) {
		return commonhttp.FileField{}, errors.New("trackers: ASC invalid cover URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return commonhttp.FileField{}, errors.New("trackers: ASC invalid cover request")
	}
	coverClient := *client
	coverClient.Jar = nil
	coverClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := coverClient.Do(req)
	if err != nil {
		return commonhttp.FileField{}, errors.New("trackers: ASC cover download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return commonhttp.FileField{}, fmt.Errorf("trackers: ASC cover download returned HTTP %d", resp.StatusCode)
	}
	contents, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	ext := imageExtension(contents)
	if err != nil || len(contents) > maxImageBytes || ext == "" {
		return commonhttp.FileField{}, errors.New("trackers: ASC cover must be a JPEG, PNG or WebP image up to 5 MiB")
	}
	return commonhttp.FileField{
		FieldName: "cover",
		FileName:  "cover" + ext,
		Content:   contents,
	}, nil
}

func imageExtension(contents []byte) string {
	switch http.DetectContentType(contents) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}
