// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package tvc

import (
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"

	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	"github.com/autobrr/upbrr/internal/metadata/metautil"
)

func buildQuestionnaire(req trackers.PreparationInput) *api.TrackerQuestionnaire {
	releaseName, _ := req.ReviewedUploadName()
	return &api.TrackerQuestionnaire{
		Tracker: "TVC",
		Fields: []api.TrackerQuestionnaireField{{
			Key:      "name_override",
			Label:    "Upload Name",
			Kind:     "text",
			Value:    releaseName,
			Required: true,
		}},
	}
}

func validateUpload(cfg config.TrackerConfig, assets trackers.DescriptionAssets) string {
	required := maxInt(cfg.ImageCount, 2)
	if len(assets.Screenshots) < required {
		return fmt.Sprintf("TVC requires at least %d screenshots", required)
	}
	for _, image := range assets.Screenshots {
		host := strings.ToLower(strings.TrimSpace(imagehost.ExtractHost(metautil.FirstNonEmptyTrimmed(image.WebURL, image.ImgURL, image.RawURL))))
		switch host {
		case "imgbb", "imgbox", "pixhost", "bam", "onlyimage":
		default:
			return "TVC screenshots must use an approved image host"
		}
	}
	return ""
}
