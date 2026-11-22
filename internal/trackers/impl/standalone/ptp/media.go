// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ptp

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
		return "", fmt.Errorf("trackers: PTP read BDInfo: %w", err)
	}
	return text, nil
}

func readTextFile(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("trackers: PTP read text file: %w", err)
	}
	return string(payload), nil
}

func buildMediaSection(meta api.UploadSubject, dbPath string) (string, error) {
	discType := strings.ToUpper(strings.TrimSpace(meta.DiscType))
	switch discType {
	case "BDMV":
		text, err := readBDSummary(meta, dbPath)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(text) == "" {
			return "", nil
		}
		if len(meta.Disc.Items) < 2 {
			return "[mediainfo]" + strings.TrimSpace(text) + "[/mediainfo]", nil
		}
		sections := make([]string, 0, len(meta.Disc.Items))
		for _, disc := range meta.Disc.Items {
			sections = append(sections, bdDiscMediaBlock(disc))
		}
		return strings.Join(sections, "\n\n"), nil
	default:
		text, err := readTextFile(strings.TrimSpace(meta.MediaInfoTextPath))
		if err != nil {
			return "", err
		}
		text = strings.TrimSpace(text)
		var sections []string
		if text != "" {
			sections = append(sections, "[mediainfo]"+text+"[/mediainfo]")
		}
		if vobText := trackers.ReadDVDVOBMediaInfo(meta); discType == "DVD" && strings.TrimSpace(vobText) != "" {
			sections = append(sections, "[mediainfo]"+vobText+"[/mediainfo]")
		}
		return strings.Join(sections, "\n\n"), nil
	}
}

// ptpDiscMediaBlocks binds each report to its prepared disc rather than
// parsing headings in a concatenated report or relying on screenshot order.
func ptpDiscMediaBlocks(meta api.UploadSubject, dbPath string) (map[string]string, error) {
	blocks := make(map[string]string, len(meta.Discs))
	switch strings.ToUpper(strings.TrimSpace(meta.DiscType)) {
	case "BDMV":
		if _, err := readBDSummary(meta, dbPath); err != nil {
			return nil, err
		}
		for _, disc := range meta.Disc.Items {
			blocks[disc.ID] = bdDiscMediaBlock(disc)
		}
		for i, disc := range meta.Discs {
			if blocks[disc.ID] == "" {
				return nil, fmt.Errorf("trackers: PTP prepared BDInfo evidence is missing for disc %d", i+1)
			}
		}
	case "DVD":
		for _, disc := range meta.Discs {
			ifo, _ := readTextFile(disc.MediaInfoTextPath)
			ifo, vob := strings.TrimSpace(ifo), strings.TrimSpace(disc.DVDVOBMediaInfoText)
			if ifo != "" && vob != "" {
				ifo += "\n\n"
			}
			if report := ifo + vob; report != "" {
				blocks[disc.ID] = "[mediainfo]" + report + "[/mediainfo]"
			}
		}
	}
	return blocks, nil
}

func bdDiscMediaBlock(disc api.DiscItemFacts) string {
	summary := (api.DiscFacts{Items: []api.DiscItemFacts{disc}}).AggregateSummary()
	return "[mediainfo]" + summary + "[/mediainfo]"
}
