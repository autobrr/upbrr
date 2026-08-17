// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func resolveMediaFields(meta api.UploadSubject, dbPath string) (map[string]string, error) {
	if strings.EqualFold(strings.TrimSpace(meta.DiscType), "BDMV") {
		bdinfo, err := resolveBDInfo(meta, dbPath)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(bdinfo) == "" {
			return nil, errors.New("trackers: ANT missing BDInfo text")
		}
		return map[string]string{
			"bdinfo":         bdinfo,
			"container_type": "m2ts",
		}, nil
	}

	if strings.TrimSpace(meta.MediaInfoTextPath) == "" {
		return nil, errors.New("trackers: ANT missing mediainfo text")
	}
	payload, err := os.ReadFile(strings.TrimSpace(meta.MediaInfoTextPath))
	if err != nil {
		return nil, fmt.Errorf("trackers: ANT read mediainfo: %w", err)
	}
	return map[string]string{"mediainfo": string(payload)}, nil
}

func resolveBDInfo(meta api.UploadSubject, dbPath string) (string, error) {
	text, err := trackers.ReadBDInfo(dbPath, meta)
	if err != nil {
		return "", fmt.Errorf("trackers: ANT read BDInfo: %w", err)
	}
	return text, nil
}
