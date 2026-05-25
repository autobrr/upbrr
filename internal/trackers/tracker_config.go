// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
)

func trackerConfigFor(cfg config.Config, tracker string) config.TrackerConfig {
	if cfg.Trackers.Trackers == nil {
		return config.TrackerConfig{}
	}
	key := strings.TrimSpace(tracker)
	if key == "" {
		return config.TrackerConfig{}
	}
	if value, ok := cfg.Trackers.Trackers[key]; ok {
		return value
	}
	lower := strings.ToLower(key)
	upper := strings.ToUpper(key)
	if value, ok := cfg.Trackers.Trackers[lower]; ok {
		return value
	}
	if value, ok := cfg.Trackers.Trackers[upper]; ok {
		return value
	}
	for name, value := range cfg.Trackers.Trackers {
		if strings.EqualFold(name, key) {
			return value
		}
	}
	return config.TrackerConfig{}
}
