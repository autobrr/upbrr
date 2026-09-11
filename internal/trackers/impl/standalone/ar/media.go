// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ar

import (
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func readBDSummary(meta api.UploadSubject, dbPath string) (string, error) {
	text, err := trackers.ReadBDInfo(dbPath, meta)
	if err != nil {
		return "", fmt.Errorf("trackers: AR read BDInfo: %w", err)
	}
	return text, nil
}

func readTextFile(path string) (string, error) {
	payload, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("trackers: AR read text file: %w", err)
	}
	return string(payload), nil
}
