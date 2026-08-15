// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/pkg/api"
)

// AuthCapability declares the API key required by standard Unit3D APIs.
func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return *authcontract.APIKeyCapability(d.profile.Name)
}

// AuthPolicy returns the family-owned effective API-key and registered torrent requirements.
func (d *Definition) AuthPolicy() *trackers.AuthPolicy {
	mode := "api_key"
	requirements := []trackers.AuthRequirement{trackers.AuthRequirementAPIKey}
	if d.profile.RegisteredTorrent != nil && d.profile.RegisteredTorrent.RequiresRSSKey {
		mode = "api_key_rss_key"
		requirements = append(requirements, trackers.AuthRequirementRSSKey)
	}
	return &trackers.AuthPolicy{
		RequirementsDetermineReadiness: d.profile.RegisteredTorrent != nil && d.profile.RegisteredTorrent.RequiresRSSKey,
		ResolveRequirements: authcontract.StaticRequirements(authcontract.Requirements(
			mode,
			false,
			requirements,
		)),
	}
}
