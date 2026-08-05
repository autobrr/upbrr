// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"errors"
	"os"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaInfoText(meta api.UploadSubject) (string, error) {
	if path := strings.TrimSpace(meta.MediaInfoTextPath); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return string(data), nil
		}
	}
	return "", errors.New("trackers: missing MediaInfo/BDInfo text")
}
