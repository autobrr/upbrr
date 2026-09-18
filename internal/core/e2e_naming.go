// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build e2e

package core

import (
	"errors"
	"fmt"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/pkg/api"
)

func e2eFixtureGeneratedName(
	category api.Category,
	title, resolution string,
	season, episode int,
	episodeTitle string,
) (api.ReleaseNameResult, bool, error) {
	_, enabled, err := e2eNamingMode()
	if err != nil || !enabled {
		return api.ReleaseNameResult{}, false, err
	}
	request := api.ReleaseNameRequest{
		Category:     string(category),
		Type:         "WEBDL",
		Title:        title,
		Year:         2026,
		Resolution:   resolution,
		Audio:        "DD 5.1",
		Tag:          "-UPBRR",
		Source:       "WEB-DL",
		VideoCodec:   "AVC",
		VideoEncode:  "H264",
		Edition:      "Uncut",
		SearchYear:   "2026",
		EpisodeTitle: episodeTitle,
	}
	if season > 0 {
		request.Season = fmt.Sprintf("S%02d", season)
	}
	if episode > 0 {
		request.Episode = fmt.Sprintf("E%02d", episode)
	}
	generated := metadata.BuildReleaseName(request, api.NopLogger{})
	if generated.GeneratedName == nil {
		return api.ReleaseNameResult{}, false, errors.New("core: e2e naming fixture produced no generated name")
	}
	return generated, true, nil
}
