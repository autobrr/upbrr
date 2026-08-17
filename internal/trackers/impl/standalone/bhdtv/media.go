// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bhdtv

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaDump(meta api.UploadSubject) (string, error) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		text, err := trackers.ReadBDInfo("", meta)
		if err != nil {
			return "", fmt.Errorf("trackers: BHDTV BDInfo: %w", err)
		}
		if text == "" {
			return "", errors.New("trackers: BHDTV missing BD summary")
		}
		return text, nil
	}

	text := ""
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "DVD") {
		text = trackers.ReadDVDVOBMediaInfo(meta)
	}
	text = metautil.FirstNonEmptyTrimmed(text, strings.TrimSpace(meta.MediaInfoTextPath))
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
