// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func namePolicy() trackers.ReleaseNamePolicyBinding {
	return trackers.StructuredReleaseNamePolicy("standalone/is/v2", trackers.StructuredNamePolicy{
		Defaults:  omitNameComponents,
		Separator: ".",
		Search:    func(meta api.UploadSubject, _ config.TrackerConfig) string { return resolveSearchName(meta) },
	})
}

func omitNameComponents(editor *trackers.NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
	for _, role := range []api.ReleaseNameRole{api.NameRoleAlternateTitle, api.NameRoleDubbed, api.NameRoleDualAudio} {
		if err := editor.Omit(role); err != nil {
			return fmt.Errorf("omit IS name component %s: %w", role, err)
		}
	}
	return nil
}

func resolveSearchName(meta api.UploadSubject) string {
	if strings.EqualFold(strings.TrimSpace(string(meta.Identity.Category)), string(api.CanonicalCategoryMovie)) {
		return ""
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
