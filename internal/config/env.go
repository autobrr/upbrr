// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"os"
	"strconv"
	"strings"
)

const envPrefix = "UA_"

// ApplyEnvOverrides mutates cfg with supported non-blank UA_ environment
// values. Invalid boolean and integer values are ignored, leaving the current
// field unchanged.
func ApplyEnvOverrides(cfg *Config) {
	if cfg == nil {
		return
	}

	if value := os.Getenv(envPrefix + "DEFAULT_TMDB_API"); value != "" {
		cfg.MainSettings.TMDBAPI = value
	}

	if value := os.Getenv(envPrefix + "DEFAULT_SCREENS"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.ScreenshotHandling.Screens = parsed
		}
	}

	if value := os.Getenv(envPrefix + "DEFAULT_ONLY_ID"); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Metadata.OnlyID = parsed
		}
	}

	if value := os.Getenv(envPrefix + "DEFAULT_KEEP_IMAGES"); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Metadata.KeepImages = parsed
		}
	}

	if value := os.Getenv(envPrefix + "TRACKERS_DEFAULT"); value != "" {
		cfg.Trackers.DefaultTrackers = CSVList(splitCSV(value))
	}

	if value := os.Getenv(envPrefix + "TRACKERS_PREFERRED"); value != "" {
		cfg.Trackers.PreferredTracker = strings.TrimSpace(value)
	}

	if value := os.Getenv(envPrefix + "DEFAULT_DB_PATH"); value != "" {
		cfg.MainSettings.DBPath = strings.TrimSpace(value)
	}
	if value := os.Getenv(envPrefix + "DEFAULT_TORRENT_CLIENT"); value != "" {
		cfg.ClientSetup.DefaultClient = strings.TrimSpace(value)
	}

	if value := os.Getenv(envPrefix + "DEFAULT_SEARCHING_CLIENT_LIST"); value != "" {
		cfg.ClientSetup.SearchClients = CSVList(splitCSV(value))
	}

	if value := os.Getenv(envPrefix + "DEFAULT_PREFER_MAX_16_TORRENT"); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.TorrentCreation.PreferMax16 = parsed
		}
	}

	if value := os.Getenv(envPrefix + "SONARR_USE"); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.ArrIntegration.UseSonarr = parsed
		}
	}
	if value := os.Getenv(envPrefix + "SONARR_URL"); value != "" {
		cfg.ArrIntegration.SonarrURL = strings.TrimSpace(value)
	}
	if value := os.Getenv(envPrefix + "SONARR_API_KEY"); value != "" {
		cfg.ArrIntegration.SonarrAPIKey = value
	}
	if value := os.Getenv(envPrefix + "SONARR_URL_1"); value != "" {
		cfg.ArrIntegration.SonarrURL1 = strings.TrimSpace(value)
	}
	if value := os.Getenv(envPrefix + "SONARR_API_KEY_1"); value != "" {
		cfg.ArrIntegration.SonarrAPIKey1 = value
	}
	if value := os.Getenv(envPrefix + "SONARR_URL_2"); value != "" {
		cfg.ArrIntegration.SonarrURL2 = strings.TrimSpace(value)
	}
	if value := os.Getenv(envPrefix + "SONARR_API_KEY_2"); value != "" {
		cfg.ArrIntegration.SonarrAPIKey2 = value
	}
	if value := os.Getenv(envPrefix + "SONARR_URL_3"); value != "" {
		cfg.ArrIntegration.SonarrURL3 = strings.TrimSpace(value)
	}
	if value := os.Getenv(envPrefix + "SONARR_API_KEY_3"); value != "" {
		cfg.ArrIntegration.SonarrAPIKey3 = value
	}
	if value := os.Getenv(envPrefix + "RADARR_USE"); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.ArrIntegration.UseRadarr = parsed
		}
	}
	if value := os.Getenv(envPrefix + "RADARR_URL"); value != "" {
		cfg.ArrIntegration.RadarrURL = strings.TrimSpace(value)
	}
	if value := os.Getenv(envPrefix + "RADARR_API_KEY"); value != "" {
		cfg.ArrIntegration.RadarrAPIKey = value
	}
	if value := os.Getenv(envPrefix + "RADARR_URL_1"); value != "" {
		cfg.ArrIntegration.RadarrURL1 = strings.TrimSpace(value)
	}
	if value := os.Getenv(envPrefix + "RADARR_API_KEY_1"); value != "" {
		cfg.ArrIntegration.RadarrAPIKey1 = value
	}
	if value := os.Getenv(envPrefix + "RADARR_URL_2"); value != "" {
		cfg.ArrIntegration.RadarrURL2 = strings.TrimSpace(value)
	}
	if value := os.Getenv(envPrefix + "RADARR_API_KEY_2"); value != "" {
		cfg.ArrIntegration.RadarrAPIKey2 = value
	}
	if value := os.Getenv(envPrefix + "RADARR_URL_3"); value != "" {
		cfg.ArrIntegration.RadarrURL3 = strings.TrimSpace(value)
	}
	if value := os.Getenv(envPrefix + "RADARR_API_KEY_3"); value != "" {
		cfg.ArrIntegration.RadarrAPIKey3 = value
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
