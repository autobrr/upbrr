// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/pkg/api"
)

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
	if height == 0 {
		height = parseResolutionHeight(meta.ReleaseName)
	}
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
