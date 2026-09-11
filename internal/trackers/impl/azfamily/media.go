// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaInfoText(meta api.UploadSubject, dbPath string) (string, error) {
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		if text, err := trackers.ReadBDInfo(dbPath, meta); err != nil {
			return "", fmt.Errorf("trackers: azfamily read BDInfo: %w", err)
		} else if strings.TrimSpace(text) != "" {
			return text, nil
		}
	case "DVD":
		if text := trackers.ReadDVDVOBMediaInfo(meta); strings.TrimSpace(text) != "" {
			return text, nil
		}
	}
	if path := strings.TrimSpace(meta.MediaInfoTextPath); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return string(data), nil
		}
	}
	return "", errors.New("trackers: missing MediaInfo/BDInfo text")
}
