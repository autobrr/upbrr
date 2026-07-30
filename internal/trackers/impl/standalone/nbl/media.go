// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package nbl

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaInfo(meta api.UploadSubject) (string, error) {
	if strings.TrimSpace(meta.MediaInfoTextPath) == "" {
		return "", errors.New("trackers: NBL missing mediainfo text")
	}
	payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
	if err != nil {
		return "", fmt.Errorf("trackers: NBL read mediainfo: %w", err)
	}
	return string(payload), nil
}
