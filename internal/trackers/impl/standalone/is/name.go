// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveSubject(meta api.UploadSubject) string {
	if meta.Scene && strings.TrimSpace(meta.SceneName) != "" {
		return strings.TrimSpace(meta.SceneName)
	}
	name := strings.TrimSpace(meta.ReleaseName)
	name = strings.ReplaceAll(name, strings.TrimSpace(meta.Release.Alt), "")
	name = strings.ReplaceAll(name, "Dubbed", "")
	name = strings.ReplaceAll(name, "Dual-Audio", "")
	name = strings.Join(strings.Fields(name), ".")
	return strings.Trim(name, ".")
}

func resolveSearchName(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(string(meta.Identity.Category)), string(api.CanonicalCategoryMovie)) {
		return resolveSubject(meta)
	}
	title := strings.TrimSpace(meta.Release.Title)
	if title == "" {
		title = strings.TrimSpace(meta.ReleaseName)
	}
	seasonEpisode := ""
	switch {
	case meta.EpisodeInt > 0:
		seasonEpisode = "S" + twoDigits(meta.SeasonInt) + "E" + twoDigits(meta.EpisodeInt)
	case meta.SeasonInt > 0:
		seasonEpisode = "S" + twoDigits(meta.SeasonInt)
	}
	return strings.TrimSpace(title + " " + seasonEpisode)
}

func twoDigits(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}
