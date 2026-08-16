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

// AuthPolicy returns the family-owned effective API-key requirements.
func (d *Definition) AuthPolicy() *trackers.AuthPolicy {
	return &trackers.AuthPolicy{
		ResolveRequirements: authcontract.StaticRequirements(authcontract.Requirements(
			"api_key",
			false,
			[]trackers.AuthRequirement{trackers.AuthRequirementAPIKey},
		)),
	}
}
