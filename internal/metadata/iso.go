// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"errors"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/discparse"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
)

// validateISOReports requires MVC evidence in every selected playlist, not a
// disc-wide SSIF marker, release filename, or manual presentation correction.
func validateISOReports(reports []preparationstate.DiscReportResource) error {
	if len(reports) == 0 {
		return errors.New("metadata: 3D Blu-ray ISO requires a selected BDInfo report")
	}
	for _, report := range reports {
		parsed := discparse.ParseBDInfoSummary(report.Summary, "", "")
		mvc := false
		for _, track := range parsed.Video {
			if strings.EqualFold(strings.TrimSpace(track.Codec), "MPEG-4 MVC Video") {
				mvc = true
				break
			}
		}
		if !mvc || !strings.EqualFold(discparse.NormalizePlaylistName(parsed.Playlist), report.Playlist.File) {
			return fmt.Errorf("metadata: selected playlist %s has no matching MVC report; only decrypted 3D Blu-ray ISOs are supported", report.Playlist.File)
		}
	}
	return nil
}
