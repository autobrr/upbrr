// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Package contract provides dependency-safe tracker auth capability and
// requirements constructors.
package contract

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// APIKeyCapability returns the standard API-key-only capability.
func APIKeyCapability(trackerID string) *api.TrackerAuthCapability {
	return capability(trackerID, "api_key", func(value *api.TrackerAuthCapability) {
		value.RequiresAPIKey = true
	})
}

// PasskeyCapability returns the standard passkey-only capability.
func PasskeyCapability(trackerID string) *api.TrackerAuthCapability {
	return capability(trackerID, "passkey", func(value *api.TrackerAuthCapability) {
		value.RequiresPasskey = true
	})
}

// CookieCapability returns the standard stored-cookie capability.
func CookieCapability(trackerID string) *api.TrackerAuthCapability {
	return capability(trackerID, "cookies", func(value *api.TrackerAuthCapability) {
		value.SupportsCookieFile = true
	})
}

func capability(
	trackerID string,
	authKind string,
	configure func(*api.TrackerAuthCapability),
) *api.TrackerAuthCapability {
	trackerID = strings.ToUpper(strings.TrimSpace(trackerID))
	value := &api.TrackerAuthCapability{
		TrackerID:   trackerID,
		DisplayName: trackerID,
		AuthKind:    authKind,
	}
	configure(value)
	return value
}

// StaticRequirements returns a resolver that always returns an independent
// copy of requirements.
func StaticRequirements(requirements trackers.EffectiveAuthRequirements) trackers.AuthRequirementsResolver {
	return func(config.Config, config.TrackerConfig) trackers.EffectiveAuthRequirements {
		return requirements.Clone()
	}
}

// Requirements returns one effective requirements value. Each supplied slice
// is one complete alternative.
func Requirements(mode string, supports2FA bool, alternatives ...[]trackers.AuthRequirement) trackers.EffectiveAuthRequirements {
	result := trackers.EffectiveAuthRequirements{
		Mode:         strings.TrimSpace(mode),
		Alternatives: make([]trackers.AuthRequirementAlternative, len(alternatives)),
		Supports2FA:  supports2FA,
	}
	for idx := range alternatives {
		result.Alternatives[idx].AllOf = append([]trackers.AuthRequirement(nil), alternatives[idx]...)
	}
	return result
}

// RequirementsForCapability derives the standard exact requirements shape
// expressed by capability. Hybrid trackers bind an explicit resolver instead.
func RequirementsForCapability(capability api.TrackerAuthCapability) trackers.AuthRequirementsResolver {
	var requirement trackers.AuthRequirement
	switch {
	case capability.RequiresAPIKey:
		requirement = trackers.AuthRequirementAPIKey
	case capability.RequiresPasskey:
		requirement = trackers.AuthRequirementPasskey
	case capability.SupportsCookieFile:
		requirement = trackers.AuthRequirementStoredCookie
	case capability.SupportsLogin:
		requirement = trackers.AuthRequirementCredentialLogin
	default:
		return nil
	}
	return StaticRequirements(
		Requirements(capability.AuthKind, capability.SupportsTOTP || capability.SupportsManual2FA, []trackers.AuthRequirement{requirement}),
	)
}
