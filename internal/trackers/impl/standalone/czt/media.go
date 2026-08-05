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

	"github.com/autobrr/upbrr/internal/trackers"
)

// buildMediaInfo returns the raw MediaInfo/BDInfo text for the CZTeam `descr`
// field.
func buildMediaInfo(req trackers.PreparationInput) string {
	return strings.TrimSpace(trackers.ReadBDinfoOrMediaInfo(req.Runtime.DBPath, req.Meta))
}
