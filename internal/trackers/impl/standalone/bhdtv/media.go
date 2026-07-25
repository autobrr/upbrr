// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaDump(meta api.UploadSubject) (string, error) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		text := readBDInfoNoErr("", meta)
		if text == "" {
			return "", errors.New("trackers: BHDTV missing BD summary")
		}
		return text, nil
	}

	text := metautil.FirstNonEmptyTrimmed(strings.TrimSpace(meta.MediaInfoTextPath), strings.TrimSpace(meta.DVDVOBMediaInfoText))
	if strings.EqualFold(text, strings.TrimSpace(meta.MediaInfoTextPath)) {
		payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			return "", fmt.Errorf("trackers: BHDTV read mediainfo: %w", err)
		}
		return string(payload), nil
	}
	if strings.TrimSpace(text) != "" {
		return text, nil
	}
	return "", errors.New("trackers: BHDTV missing mediainfo text")
}

func readBDInfoNoErr(_ string, meta api.UploadSubject) string {
	if summary := strings.TrimSpace(meta.Disc.Summary); summary != "" {
		return summary
	}
	if infoPath := guessBDInfoPath(meta); infoPath != "" {
		payload, err := os.ReadFile(infoPath)
		if err == nil {
			return strings.TrimSpace(string(payload))
		}
	}
	return ""
}

func guessBDInfoPath(meta api.UploadSubject) string {
	if len(meta.SelectedBDMVPlaylists) == 0 {
		return ""
	}
	base := strings.TrimSpace(meta.MediaInfoTextPath)
	if base != "" {
		return filepath.Join(filepath.Dir(base), paths.BDMVSummaryFilename(paths.PrimaryBDMVPlaylistFor(meta.SelectedBDMVPlaylists)))
	}
	return ""
}
