// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"strings"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type unit3DSiteProfile struct {
	resolveTypeID          func(meta api.PreparedMetadata) string
	resolveResolutionID    func(meta api.PreparedMetadata) string
	resolveCategoryID      func(meta api.PreparedMetadata) string
	applyAdditionalPayload func(req trackers.UploadRequest, data map[string]string)
}

var unit3DSiteProfiles = map[string]unit3DSiteProfile{
	"A4K":    siteA4KProfile(),
	"ACM":    siteACMProfile(),
	"AITHER": siteAITHERProfile(),
	"BLU":    siteBLUProfile(),
	"CBR":    siteCBRProfile(),
	"EMUW":   siteEMUWProfile(),
	"IHD":    siteIHDProfile(),
	"ITT":    siteITTProfile(),
	"LDU":    siteLDUProfile(),
	"LST":    siteLSTProfile(),
	"LT":     siteLTProfile(),
	"OE":     siteOEProfile(),
	"OTW":    siteOTWProfile(),
	"PT":     sitePTProfile(),
	"R4E":    siteR4EProfile(),
	"RF":     siteRFProfile(),
	"RHD":    siteRHDProfile(),
	"SHRI":   siteSHRIProfile(),
	"STC":    siteSTCProfile(),
	"TIK":    siteTIKProfile(),
	"TLZ":    siteTLZProfile(),
	"TOS":    siteTOSProfile(),
	"UTP":    siteUTPProfile(),
	"YUS":    siteYUSProfile(),
	"ZNTH":   siteZNTHProfile(),
}

func unit3DSiteProfileFor(tracker string) (unit3DSiteProfile, bool) {
	key := strings.ToUpper(strings.TrimSpace(tracker))
	if key == "" {
		return unit3DSiteProfile{}, false
	}
	profile, ok := unit3DSiteProfiles[key]
	return profile, ok
}
