// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/pkg/api"
)

// AuthCapability returns the stored-cookie authentication contract for this
// AZ-family profile.
func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return *authcontract.CookieCapability(d.Name())
}

// AuthPolicy returns the family-owned effective stored-cookie requirements.
func (d *Definition) AuthPolicy() *trackers.AuthPolicy {
	return &trackers.AuthPolicy{
		ResolveRequirements: authcontract.StaticRequirements(authcontract.Requirements(
			"form",
			false,
			[]trackers.AuthRequirement{trackers.AuthRequirementStoredCookie},
		)),
	}
}
