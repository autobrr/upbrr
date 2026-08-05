// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package czt implements uploads to CZTeam (CZT) via its dedicated JSON
// endpoint takeupload_api.php.
//
// Unlike most impls in this repo CZTeam is not a UNIT3D site and does not need a
// cookie jar: the user's passkey authenticates the multipart POST. The endpoint
// returns the registered .torrent inline as base64, already personalized with
// the uploader's announce passkey and source=CzT.
package czt

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// categoryQuestionnaire offers a (non-blocking) category dropdown pre-filled
// with the auto-detected category, so the user can override it for content
// upbrr can't classify from video metadata.
func categoryQuestionnaire(meta api.UploadSubject) *api.TrackerQuestionnaire {
	auto := autoCategory(meta)
	return &api.TrackerQuestionnaire{
		Tracker: trackerName,
		Fields: []api.TrackerQuestionnaireField{{
			Key:      "category",
			Label:    "Category",
			Kind:     "select",
			Options:  categoryNames(),
			Value:    categoryNameForID(auto),
			Help:     "Auto-detected from video metadata. Override for software, games, music, XXX, etc.",
			Required: auto == "",
		}},
	}
}

func resolveQuestionnaireCategory(value string) string {
	if id := categoryIDForName(value); id != "" {
		return id
	}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, c := range cztCategories {
		if c.id == trimmed {
			return c.id
		}
	}
	return ""
}
