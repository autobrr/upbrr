// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d

import (
	"net/http"
	"time"

	"github.com/autobrr/upbrr/internal/trackers"
	authcontract "github.com/autobrr/upbrr/internal/trackers/auth/contract"
	"github.com/autobrr/upbrr/pkg/api"
)

// apiKeyGuidanceReporter presents advice learned after preflight once per plan.
// Advice already present in the shared CLI/WebUI projection is not logged again.
func apiKeyGuidanceReporter(input trackers.PreparationInput, guidance *trackers.APIKeyGuidance) func(http.Header, int) {
	seen := make(map[string]bool)
	if input.Projection != nil {
		for _, decision := range input.Projection.PolicyDecisions {
			if decision.Code == trackers.APIKeyGuidanceCode {
				seen[decision.Message] = true
			}
		}
	}
	return func(header http.Header, status int) {
		guidance.Observe(input.TrackerConfig.APIKey, header, status)
		message := guidance.Guidance(input.TrackerConfig.APIKey, time.Now())
		if message != "" && !seen[message] {
			seen[message] = true
			if input.Logger != nil {
				input.Logger.Infof("trackers: %s guidance: %s", input.Tracker, message)
			}
		}
	}
}

// AuthCapability declares the API key required by standard Unit3D APIs.
func (d *Definition) AuthCapability() api.TrackerAuthCapability {
	return *authcontract.APIKeyCapability(d.profile.Name)
}

// AuthPolicy returns the family-owned effective API-key requirements.
func (d *Definition) AuthPolicy() *trackers.AuthPolicy {
	return &trackers.AuthPolicy{
		APIKeyGuidance: d.profile.Site.APIKeyGuidance,
		ResolveRequirements: authcontract.StaticRequirements(authcontract.Requirements(
			"api_key",
			false,
			[]trackers.AuthRequirement{trackers.AuthRequirementAPIKey},
		)),
	}
}
