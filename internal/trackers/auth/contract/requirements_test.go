// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package contract

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
)

func TestStandardCapabilitiesAndRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		tracker     string
		requirement trackers.AuthRequirement
		resolve     trackers.AuthRequirementsResolver
	}{
		{
			name:        "API key",
			tracker:     "ANT",
			requirement: trackers.AuthRequirementAPIKey,
			resolve:     RequirementsForCapability(*APIKeyCapability(" ant ")),
		},
		{
			name:        "passkey",
			tracker:     "CZT",
			requirement: trackers.AuthRequirementPasskey,
			resolve:     RequirementsForCapability(*PasskeyCapability(" czt ")),
		},
		{
			name:        "stored cookie",
			tracker:     "ASC",
			requirement: trackers.AuthRequirementStoredCookie,
			resolve:     RequirementsForCapability(*CookieCapability(" asc ")),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			requirements := test.resolve(
				config.Config{Trackers: config.TrackersConfig{Trackers: map[string]config.TrackerConfig{
					test.tracker: {APIKey: "must-not-leak", Passkey: "must-not-leak"},
				}}},
				config.TrackerConfig{APIKey: "must-not-leak", Passkey: "must-not-leak"},
			)
			if len(requirements.Alternatives) != 1 ||
				!slices.Equal(requirements.Alternatives[0].AllOf, []trackers.AuthRequirement{test.requirement}) {
				t.Fatalf("requirements = %#v", requirements)
			}
		})
	}
}

func TestStaticRequirementsReturnsIndependentValues(t *testing.T) {
	t.Parallel()

	resolve := StaticRequirements(Requirements(
		"hybrid",
		true,
		[]trackers.AuthRequirement{trackers.AuthRequirementAPIKey, trackers.AuthRequirementStoredCookie},
	))
	first := resolve(config.Config{}, config.TrackerConfig{})
	first.Alternatives[0].AllOf[0] = trackers.AuthRequirementPasskey
	second := resolve(config.Config{}, config.TrackerConfig{})
	if second.Alternatives[0].AllOf[0] != trackers.AuthRequirementAPIKey {
		t.Fatalf("resolver leaked mutable requirements: %#v", second)
	}
}
