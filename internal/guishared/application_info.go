// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guishared

import (
	"context"
	"strings"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

const applicationInfoCapabilityTimeout = 10 * time.Second

const (
	// DVDMenuCapabilityAvailable reports compatible FFmpeg dvdvideo support.
	DVDMenuCapabilityAvailable = "available"
	// DVDMenuCapabilityIncompatible reports FFmpeg without required dvdvideo support.
	DVDMenuCapabilityIncompatible = "incompatible"
	// DVDMenuCapabilityUnavailable reports that capability could not be inspected.
	DVDMenuCapabilityUnavailable = "unavailable"
)

// CurrentApplicationInfo adds path-free DVD menu capability diagnostics to
// the shared application build/runtime information. It bounds the probe to ten
// seconds and returns unavailable status when ctx or provider is nil.
func CurrentApplicationInfo(ctx context.Context, provider api.DVDMenuCapabilityProvider) api.ApplicationInfo {
	info := api.CurrentApplicationInfo()
	info.DVDMenuCapabilityStatus = DVDMenuCapabilityUnavailable
	info.DVDMenuCapabilityMessage = "DVD menu capture capability could not be checked."
	if provider == nil {
		return info
	}
	if ctx == nil {
		return info
	}
	probeCtx, cancel := context.WithTimeout(ctx, applicationInfoCapabilityTimeout)
	defer cancel()

	capability, err := provider.DVDMenuCapability(probeCtx)
	info.DVDMenuEngine = capability
	if err != nil {
		if len(capability.MissingFFmpegOptions) > 0 {
			info.DVDMenuCapabilityStatus = DVDMenuCapabilityIncompatible
			info.DVDMenuCapabilityMessage = "FFmpeg lacks required dvdvideo menu options: " + strings.Join(capability.MissingFFmpegOptions, ", ")
			return info
		}
		info.DVDMenuCapabilityMessage = "FFmpeg was not found or its dvdvideo menu capability could not be inspected."
		return info
	}
	if !capability.FFmpegDVDVideo {
		info.DVDMenuCapabilityStatus = DVDMenuCapabilityIncompatible
		info.DVDMenuCapabilityMessage = "FFmpeg lacks required dvdvideo menu support."
		return info
	}
	info.DVDMenuCapabilityStatus = DVDMenuCapabilityAvailable
	info.DVDMenuCapabilityMessage = "Compatible FFmpeg dvdvideo menu support detected."
	return info
}
