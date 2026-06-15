// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3dmeta

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/autobrr/upbrr/internal/config"
)

var (
	trackerBaseURLs = map[string]string{}
	initOnce        sync.Once
)

func initTrackers() {
	initOnce.Do(func() {
		cfg, err := config.LoadEmbeddedDefaultConfig()
		if err != nil || cfg == nil || len(cfg.Trackers.Trackers) == 0 {
			_, _ = fmt.Fprintf(os.Stderr, "unit3dmeta: error loading embedded default config: %v\n", err)
			return
		}

		unit3DTrackers := []string{
			"A4K", "ACM", "AITHER", "BLU", "CBR", "DP", "EMUW",
			"FRIKI", "HHD", "IHD", "ITT", "LCD", "LDU", "LST", "LT",
			"LUME", "OE", "OTW", "PT", "PTT", "R4E", "RAS", "RF", "RHD",
			"SAM", "SHRI", "SP", "STC", "TIK", "TLZ", "TOS", "TTR",
			"ULCX", "UTP", "YUS",
		}

		for _, name := range unit3DTrackers {
			if trackerCfg, ok := cfg.Trackers.Trackers[name]; ok {
				if strings.TrimSpace(trackerCfg.URL) != "" {
					trackerBaseURLs[name] = trackerCfg.URL
				}
			}
		}
	})
}

func DefaultTracker() string {
	return "AITHER"
}

func Trackers() []string {
	initTrackers()
	trackers := make([]string, 0, len(trackerBaseURLs))
	for tracker := range trackerBaseURLs {
		trackers = append(trackers, tracker)
	}
	sort.Strings(trackers)
	return trackers
}

func BaseURL(tracker string) (string, bool) {
	initTrackers()
	key := strings.ToUpper(strings.TrimSpace(tracker))
	if key == "" {
		return "", false
	}
	baseURL, ok := trackerBaseURLs[key]
	return baseURL, ok
}

func IsKnown(tracker string) bool {
	_, ok := BaseURL(tracker)
	return ok
}
