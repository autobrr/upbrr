// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// IsInternalGroup reports whether meta's release group is configured as internal for tracker.
func IsInternalGroup(cfg config.Config, tracker string, meta api.UploadSubject) bool {
	if strings.TrimSpace(meta.Tag) == "" {
		return false
	}

	trackerKey := strings.ToUpper(strings.TrimSpace(tracker))
	if trackerKey == "" {
		return false
	}

	trackerCfg, ok := cfg.Trackers.Trackers[trackerKey]
	if !ok || !trackerCfg.Internal {
		return false
	}

	if len(trackerCfg.InternalGroups) == 0 {
		return false
	}

	group := strings.ToLower(strings.TrimPrefix(meta.Tag, "-"))
	for _, value := range trackerCfg.InternalGroups {
		if strings.ToLower(strings.TrimSpace(value)) == group {
			return true
		}
	}

	return false
}
