// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

// Profile returns AITHER's Unit3D site manifest.
func Profile() unit3d.Profile {
	return unit3d.Profile{
		Name:    "AITHER",
		BaseURL: "https://aither.cc",
		Rules:   Rules(),
		Site: unit3d.SiteProfile{
			BuildName:              buildName,
			ApplyAdditionalPayload: additionalPayload,
		},
		DupePolicy: &trackers.DupePolicy{
			TrackTrumpableID:      true,
			MatchDVDReleaseGroup:  true,
			SDMatchesHD:           true,
			AllowSizeVariance1080: true,
		},
		BannedPolicy: &trackers.BannedGroupPolicy{
			EndpointPath:  "/api/blacklists/releasegroups",
			RequireAPIKey: true,
		},
		ClaimPolicy: &trackers.ClaimPolicy{
			APIBacked: true,
		},
	}
}

func additionalPayload(req trackers.PreparationInput, data map[string]string) {
	hdr := strings.ToUpper(strings.TrimSpace(req.Meta.HDR))
	data["dv"] = boolString(strings.Contains(hdr, "DV"))
	if strings.Contains(hdr, "HDR10+") {
		data["hdr10p"] = "1"
		return
	}
	if strings.Contains(hdr, "HDR") || strings.Contains(hdr, "HLG") {
		data["hdr"] = "1"
	}
}

func boolString(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func buildName(meta api.UploadSubject, _ config.TrackerConfig) string {
	name := strings.TrimSpace(meta.ReleaseName)
	if name == "" {
		name = strings.TrimSpace(meta.ReleaseNameNoTag)
	}
	if name == "" {
		return ""
	}
	resolution, videoCodec, videoEncode := unit3d.Resolution(meta), strings.TrimSpace(meta.VideoCodec), strings.TrimSpace(meta.VideoEncode)
	nameType, source, audio := strings.ToUpper(strings.TrimSpace(meta.Type)), strings.TrimSpace(meta.Source), strings.TrimSpace(meta.Audio)
	languages := append([]string{}, meta.Release.Language...)
	if len(languages) > 0 && !unit3d.HasEnglishLanguage(languages) {
		foreign := strings.ToUpper(strings.TrimSpace(languages[0]))
		if nameType == "REMUX" && isDVDSource(source) && meta.Release.Year > 0 {
			year := strconv.Itoa(meta.Release.Year)
			name = strings.Replace(name, year, year+" "+foreign, 1)
		} else if !strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") && resolution != "" {
			name = strings.Replace(name, resolution, foreign+" "+resolution, 1)
		}
	}
	if nameType == "DVDRIP" {
		source = "DVDRip"
		if meta.Source != "" {
			name = strings.Replace(name, meta.Source+" ", "", 1)
		}
		if videoEncode != "" {
			name = strings.Replace(name, videoEncode, "", 1)
		}
		if resolution != "" {
			name = strings.Replace(name, source, resolution+" "+source, 1)
		}
		if audio != "" && videoEncode != "" {
			name = strings.Replace(name, audio, audio+videoEncode, 1)
		}
	} else if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") || (nameType == "REMUX" && isDVDSource(source)) {
		if resolution != "" && source != "" {
			name = strings.Replace(name, source, resolution+" "+source, 1)
		}
		if audio != "" && videoCodec != "" {
			name = strings.Replace(name, audio, videoCodec+" "+audio, 1)
		}
	}
	return strings.TrimSpace(strings.Join(strings.Fields(name), " "))
}

func isDVDSource(source string) bool {
	switch strings.ToUpper(strings.TrimSpace(source)) {
	case "PAL DVD", "NTSC DVD", "DVD":
		return true
	default:
		return false
	}
}
