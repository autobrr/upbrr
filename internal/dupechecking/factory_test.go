// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupechecking

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

func TestBuildHandlersCoversKnownTrackers(t *testing.T) {
	t.Parallel()
	handlers := buildHandlers(handlerDeps{cfg: config.Config{}})

	known := []string{
		"A4K", "ACM", "AITHER", "ANT", "AR", "ASC", "AZ", "BHD", "BHDTV", "BJS", "BLU", "BT", "BTN", "CBR", "CZ", "DC", "DP", "EMUW", "FF", "FL", "FNP", "FRIKI", "GPW", "HDB", "HDS", "HDT", "HHD", "HUNO", "IHD", "IS", "ITT", "LCD", "LDU", "LST", "LT", "LUME", "MTV", "NBL", "OE", "OTW", "PHD", "PT", "PTP", "PTS", "PTT", "R4E", "RAS", "RF", "RTF", "SAM", "SHRI", "SP", "SPD", "STC", "THR", "TIK", "TL", "TLZ", "TOS", "TTR", "TVC", "ULCX", "UTP", "YUS",
	}
	for _, tracker := range known {
		if _, ok := handlers[tracker]; !ok {
			t.Fatalf("expected handler for %s", tracker)
		}
	}
}
