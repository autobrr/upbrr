// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"

	"errors"

	"github.com/autobrr/upbrr/internal/trackers"
)

func readTextFile(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", nil
	}
	payload, err := os.ReadFile(trimmed)
	if err != nil {
		return "", fmt.Errorf("trackers: UNIT3D read text file: %w", err)
	}
	return string(payload), nil
}

func resolveNFOPath(meta api.UploadSubject, dbPath string) string {
	if path := strings.TrimSpace(meta.SceneNFOPath); path != "" && existsFile(path) {
		return path
	}

	if strings.TrimSpace(dbPath) == "" {
		return ""
	}
	tmpRoot, err := db.Subdir(dbPath, "tmp")
	if err != nil {
		return ""
	}
	tmpDir, _, err := paths.ReleaseTempDirFor(tmpRoot, meta.SourcePath, meta.Release)
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if strings.HasSuffix(name, ".nfo") {
			return filepath.Join(tmpDir, entry.Name())
		}
	}
	return ""
}

func resolveTVDBID(meta api.UploadSubject) int {
	if strings.EqualFold(resolveUnit3DCategory(meta), "TV") {
		if meta.Identity.TVDBID != 0 {
			return meta.Identity.TVDBID
		}
	}
	return 0
}

// resolveSeason returns the Unit3D payload season value from SeasonInt only.
func resolveSeason(meta api.UploadSubject) string {
	if meta.SeasonInt <= 0 {
		return "0"
	}
	return formatOptionalInt(meta.SeasonInt)
}

// resolveEpisode returns the Unit3D payload episode value from EpisodeInt only.
func resolveEpisode(meta api.UploadSubject) string {
	if meta.EpisodeInt <= 0 {
		return "0"
	}
	return formatOptionalInt(meta.EpisodeInt)
}

// validateUnit3DTVPayloadMetadata returns the shared Unit3D TV metadata block
// reason used by live upload and dry-run when canonical season or episode data
// is missing from payload fields that would otherwise be submitted as zero.
func validateUnit3DTVPayloadMetadata(trackerName string, meta api.UploadSubject, data map[string]string) (string, error) {
	message := unit3DTVPayloadMetadataMessage(meta, data)
	if message == "" {
		return "", nil
	}
	return message, fmt.Errorf("trackers: %s %s", trackerName, message)
}

// unit3DTVPayloadMetadataMessage explains when Unit3D TV fields are present but
// canonical season or episode metadata is missing. Parsed release and manual
// naming values are reported only as ignored signals, and the message includes
// the operator action required by blocked dry-run entries.
func unit3DTVPayloadMetadataMessage(meta api.UploadSubject, data map[string]string) string {
	if _, hasSeason := data["season_number"]; !hasSeason {
		return ""
	}
	if _, hasEpisode := data["episode_number"]; !hasEpisode {
		return ""
	}

	missing := make([]string, 0, 2)
	ignored := make([]string, 0, 2)
	if meta.SeasonInt <= 0 {
		missing = append(missing, "season")
		if hasParsedSeasonSignal(meta) {
			ignored = append(ignored, "season")
		}
	}
	if meta.EpisodeInt <= 0 && !meta.TVPack {
		missing = append(missing, "episode")
		if hasParsedEpisodeSignal(meta) {
			ignored = append(ignored, "episode")
		}
	}
	if len(missing) == 0 {
		return ""
	}
	message := "canonical TV " + strings.Join(missing, "/") + " missing; tracker payload uses 0"
	if len(ignored) > 0 {
		message += " and ignores parsed " + strings.Join(ignored, "/") + " fallback"
	}
	message += "; refresh metadata or correct canonical season/episode before upload"
	return message
}

var seasonEpisodePattern = regexp.MustCompile(`(?i)S(\d{1,2})(?:E(\d{1,2}))?`)

func parseSeasonEpisode(name string) (int, int) {
	matches := seasonEpisodePattern.FindStringSubmatch(name)
	if len(matches) < 2 {
		return 0, 0
	}
	season := atoi(matches[1])
	episode := 0
	if len(matches) > 2 {
		episode = atoi(matches[2])
	}
	return season, episode
}

func parseSeasonEpisodeToken(value string, prefix string) int {
	trimmed := strings.TrimSpace(strings.ToUpper(value))
	if trimmed == "" {
		return 0
	}
	trimmed = strings.TrimPrefix(trimmed, strings.ToUpper(prefix))
	return atoi(trimmed)
}

func loadUnit3DMedia(meta api.UploadSubject, dbPath string, logger api.Logger) (string, string, error) {
	bdinfo := ""
	mediainfo := ""

	if isDiscType(meta.DiscType) {
		logger.Debugf("trackers: loading BDInfo for disc type: %s", meta.DiscType)
		text, err := trackers.ReadBDInfo(dbPath, meta)
		if err != nil {
			logger.Warnf("trackers: unit3d bdinfo read failed: %v", err)
		} else if text != "" {
			logger.Tracef("trackers: loaded BDInfo (%d bytes)", len(text))
		}
		bdinfo = text
	}

	if bdinfo == "" {
		logger.Debugf("trackers: loading MediaInfo from: %s", filepath.Base(meta.MediaInfoTextPath))
		text, err := readTextFile(meta.MediaInfoTextPath)
		if err != nil {
			logger.Errorf("trackers: failed to read MediaInfo: %v", err)
			return "", "", fmt.Errorf("trackers: unit3d mediainfo: %w", err)
		}
		if text == "" {
			err := errors.New("trackers: MediaInfo is empty")
			logger.Errorf("trackers: unit3d mediainfo load failed: %v", err)
			return "", "", err
		}
		logger.Tracef("trackers: loaded MediaInfo (%d bytes)", len(text))
		mediainfo = text
	}

	return mediainfo, bdinfo, nil
}

func hasParsedSeasonSignal(meta api.UploadSubject) bool {
	if meta.Release.Season > 0 {
		return true
	}
	if meta.ReleaseNameOverrides.Season != nil && parseSeasonEpisodeToken(*meta.ReleaseNameOverrides.Season, "S") > 0 {
		return true
	}
	season, _ := parseSeasonEpisode(meta.ReleaseName)
	return season > 0
}

func hasParsedEpisodeSignal(meta api.UploadSubject) bool {
	if meta.Release.Episode > 0 {
		return true
	}
	if meta.ReleaseNameOverrides.Episode != nil && parseSeasonEpisodeToken(*meta.ReleaseNameOverrides.Episode, "E") > 0 {
		return true
	}
	_, episode := parseSeasonEpisode(meta.ReleaseName)
	return episode > 0
}

func existsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func formatOptionalInt(value int) string {
	if value <= 0 {
		return "0"
	}
	return strconv.Itoa(value)
}

func atoi(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	out := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		out = out*10 + int(r-'0')
	}
	return out
}
