// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"errors"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// DataLookupConfigured reports whether the BTN API token is available.
func (d *Definition) DataLookupConfigured(cfg config.Config) bool {
	return len(config.ResolveBTNAPIToken(cfg)) >= 25
}

func bannedGroups() []string {
	return []string{
		"3LTON", "4yEo", "7VFr33104D", "AFG", "AniHLS", "AnimeRG", "AniURL", "DeadFish", "ELiTE", "eSc",
		"EVO", "FGT", "FUM", "GalaxyTV", "GRANiTEN", "HAiKU", "Hi10", "ION10", "JFF", "JIVE", "LOAD", "MeGusta",
		"mSD", "NhaNc3", "NOIVTC", "PHOENiX", "PlaySD", "playXD", "Pr1M371M3", "RAPiDCOWS", "REsuRRecTioN", "RMTeam",
		"ROBOTS", "RUBiK", "SPASM", "Telly", "TM", "URANiME", "ViSiON", "W45Ps", "xRed", "XS", "ZKBL", "ZmN", "ZMNT", "[Oj]",
	}
}

func validateBTNRequest(req trackers.PreparationInput) error {
	category, err := req.Meta.Identity.RequireCategory()
	if err != nil || category != api.CanonicalCategoryTV {
		return errors.New("trackers: BTN only supports TV uploads")
	}
	if strings.TrimSpace(req.Runtime.BTNAPIToken) == "" {
		return errors.New("trackers: BTN requires trackers.BTN.api_key")
	}
	return nil
}

var btnInternalGroups = []string{
	"BTW",
	"ESPNtb",
	"HiSD",
	"HRiP",
	"iPRiP",
	"iT00NZ",
	"JJ",
	"LoTV",
	"NTb",
	"PreBS",
	"RAWR",
	"TTVa",
	"TVSmash",
}

func isBTNInternalGroup(meta api.UploadSubject) bool {
	if strings.TrimSpace(meta.Tag) == "" {
		return false
	}

	group := strings.ToLower(strings.TrimPrefix(meta.Tag, "-"))
	for _, value := range btnInternalGroups {
		if strings.ToLower(value) == group {
			return true
		}
	}
	return false
}
