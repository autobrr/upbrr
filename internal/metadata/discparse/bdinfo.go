// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package discparse

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var playlistReportHeaderPattern = regexp.MustCompile(`(?m)^\*{20}\r?\nPLAYLIST:\s+([^\r\n]+)\r?\n\*{20}`)

// PlaylistReport preserves one raw BDInfo playlist block together with its
// extracted quick-summary, files, and extended-summary sections.
type PlaylistReport struct {
	Playlist   string
	Raw        string
	Summary    string
	Files      string
	ExtSummary string
}

// NormalizePlaylistName reduces slash- or backslash-delimited input to an
// uppercase MPLS basename, adding the extension when absent.
func NormalizePlaylistName(name string) string {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return ""
	}
	if !strings.HasSuffix(strings.ToUpper(trimmed), ".MPLS") {
		trimmed += ".MPLS"
	}
	return strings.ToUpper(filepath.Base(trimmed))
}

// SplitBDInfoReport extracts summary and files sections from a BDInfo report.
func SplitBDInfoReport(text string) (summary string, files string, extSummary string) {
	parts := strings.SplitN(text, "QUICK SUMMARY:", 2)
	if len(parts) == 2 {
		filesSection := ""
		fileParts := strings.SplitN(parts[0], "FILES:", 2)
		if len(fileParts) == 2 {
			filesSection = fileParts[1]
			filesSection = strings.SplitN(filesSection, "CHAPTERS:", 2)[0]
			filesBlocks := strings.SplitN(filesSection, "-------------", 2)
			if len(filesBlocks) == 2 {
				filesSection = filesBlocks[1]
			}
		}
		files = strings.TrimSpace(filesSection)

		remaining := strings.TrimRight(parts[1], " \n")
		summary = strings.TrimRight(strings.SplitN(remaining, "********************", 2)[0], " \n")
		summary = normalizeSummarySpaces(summary)
	}

	// Legacy Python logic selects the segment after the second [code] marker.
	// Using SplitN with max 4 keeps parts[2] aligned for 2/3/4-marker inputs.
	codeParts := strings.SplitN(text, "[code]", 4)
	if len(codeParts) >= 3 {
		codeSection := strings.TrimRight(codeParts[2], " \n")
		extSummary = strings.TrimRight(strings.SplitN(codeSection, "FILES:", 2)[0], " \n")
		extSummary = normalizeSummarySpaces(extSummary)
	}

	return summary, files, extSummary
}

func normalizeSummarySpaces(text string) string {
	rawLines := strings.Split(text, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, line := range rawLines {
		if strings.Contains(line, "/") && !strings.Contains(line, "[") && !strings.Contains(line, "]") && !strings.Contains(line, "**") {
			parts := strings.Split(line, "/")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			line = strings.Join(parts, " / ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// SplitBDInfoPlaylistReports extracts playlist blocks keyed by normalized MPLS
// basename. Reports without playlist headers return nil; duplicate normalized
// names fail the entire parse.
func SplitBDInfoPlaylistReports(text string) (map[string]string, error) {
	matches := playlistReportHeaderPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	reports := make(map[string]string, len(matches))
	for idx, match := range matches {
		start := match[0]
		end := len(text)
		if idx+1 < len(matches) {
			end = matches[idx+1][0]
		}
		name := NormalizePlaylistName(text[match[2]:match[3]])
		if name == "" {
			continue
		}
		if _, exists := reports[name]; exists {
			return nil, fmt.Errorf("duplicate playlist block %s found in BDInfo report", name)
		}
		reports[name] = strings.TrimSpace(text[start:end])
	}

	if len(reports) == 0 {
		return nil, nil
	}
	return reports, nil
}

// ExtractPlaylistReports returns selected playlist reports in selection order.
// Missing blocks or quick summaries fail the entire extraction without a
// partial result.
func ExtractPlaylistReports(text string, selected []string) ([]PlaylistReport, error) {
	if len(selected) == 0 {
		return nil, nil
	}

	blocks, err := SplitBDInfoPlaylistReports(text)
	if err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, errors.New("no playlist blocks found in BDInfo report")
	}

	reports := make([]PlaylistReport, 0, len(selected))
	for _, selectedName := range selected {
		normalized := NormalizePlaylistName(selectedName)
		block, ok := blocks[normalized]
		if !ok {
			return nil, fmt.Errorf("playlist block %s not found in BDInfo report", normalized)
		}

		summary, files, extSummary := SplitBDInfoReport(block)
		if strings.TrimSpace(summary) == "" {
			return nil, fmt.Errorf("playlist block %s did not contain a quick summary", normalized)
		}

		reports = append(reports, PlaylistReport{
			Playlist:   normalized,
			Raw:        block,
			Summary:    summary,
			Files:      files,
			ExtSummary: extSummary,
		})
	}

	return reports, nil
}

// ParseBDInfoFiles parses the FILES section of a BDInfo report.
func ParseBDInfoFiles(files string) []BDFile {
	var result []BDFile
	for _, line := range strings.Split(files, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.Fields(trimmed)
		if len(parts) < 3 {
			continue
		}
		if strings.HasPrefix(parts[1], "(") && strings.Contains(parts[1], ")") {
			fileName := fmt.Sprintf("%s %s", parts[0], parts[1])
			parts = append([]string{fileName}, parts[2:]...)
			if len(parts) < 3 {
				continue
			}
		}

		result = append(result, BDFile{
			File:   parts[0],
			Length: parts[1],
		})
	}

	return result
}

// ParseBDInfoSummary parses a BDInfo summary and files section.
func ParseBDInfoSummary(summary string, files string, path string) *BDInfo {
	info := &BDInfo{Path: path}
	for _, raw := range strings.Split(summary, "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "*") {
			lower = strings.TrimSpace(strings.TrimPrefix(lower, "*"))
			line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		}

		switch {
		case strings.HasPrefix(lower, "playlist:"):
			value := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			info.Playlist = strings.TrimSpace(strings.SplitN(value, ".", 2)[0])
		case strings.HasPrefix(lower, "disc size:"):
			value := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			value = strings.TrimSpace(strings.SplitN(value, "bytes", 2)[0])
			value = strings.ReplaceAll(value, ",", "")
			if bytesValue, err := strconv.ParseFloat(value, 64); err == nil {
				info.SizeGB = bytesValue / float64(1<<30)
			}
		case strings.HasPrefix(lower, "length:"):
			value := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			info.Length = strings.TrimSpace(strings.SplitN(value, ".", 2)[0])
		case strings.HasPrefix(lower, "disc title:"):
			info.Title = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		case strings.HasPrefix(lower, "disc label:"):
			info.Label = strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
		case strings.HasPrefix(lower, "video:"):
			info.Video = append(info.Video, parseVideoLine(line))
		case strings.HasPrefix(lower, "audio:"):
			info.Audio = append(info.Audio, parseAudioLine(line))
		case strings.HasPrefix(lower, "subtitle:"):
			value := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			parts := strings.Split(value, "/")
			if len(parts) > 0 {
				info.Subtitles = append(info.Subtitles, strings.TrimSpace(parts[0]))
			}
		}
	}

	info.Files = ParseBDInfoFiles(files)
	return info
}

func parseVideoLine(line string) BDVideo {
	value := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
	parts := strings.SplitN(value, "/", 12)
	for len(parts) < 9 {
		parts = append(parts, "")
	}
	index := 0
	threeD := ""
	if strings.Contains(parts[2], "Eye") {
		index = 1
		threeD = strings.TrimSpace(parts[2])
	}

	bitDepth := safeTrim(parts, index+6)
	hdrDV := safeTrim(parts, index+7)
	color := safeTrim(parts, index+8)

	return BDVideo{
		Codec:       strings.TrimSpace(parts[0]),
		Bitrate:     strings.TrimSpace(parts[1]),
		Resolution:  safeTrim(parts, index+2),
		FPS:         safeTrim(parts, index+3),
		AspectRatio: safeTrim(parts, index+4),
		Profile:     safeTrim(parts, index+5),
		BitDepth:    bitDepth,
		HDRDV:       hdrDV,
		Color:       color,
		ThreeD:      threeD,
	}
}

func parseAudioLine(line string) BDAudio {
	value := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
	if strings.Contains(value, "(") {
		value = strings.SplitN(value, "(", 2)[0]
	}
	parts := strings.Split(value, "/")
	index := 0
	atmos := ""
	if len(parts) > 2 && strings.Contains(parts[2], "Atmos") {
		index = 1
		atmos = strings.TrimSpace(parts[2])
	}

	return BDAudio{
		Language:   safeTrim(parts, 0),
		Codec:      safeTrim(parts, 1),
		Channels:   safeTrim(parts, index+2),
		SampleRate: safeTrim(parts, index+3),
		Bitrate:    safeTrim(parts, index+4),
		BitDepth:   safeTrim(parts, index+5),
		Atmos:      atmos,
	}
}

func safeTrim(parts []string, idx int) string {
	if idx < 0 || idx >= len(parts) {
		return ""
	}
	return strings.TrimSpace(parts[idx])
}
