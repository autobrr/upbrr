// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// AudioAccountScope fingerprints the configured host account without persisting
// its credentials. Changes to hosting configuration conservatively prevent reuse.
func AudioAccountScope(cfg config.Config, registry *trackers.Registry, host string) (string, error) {
	fingerprint, err := api.CanonicalWorkflowFingerprint(struct {
		Version string
		Host    string
		Hosting config.ImageHostingConfig
		Owner   config.TrackerConfig
	}{"audio-host-account-v1", strings.ToLower(strings.TrimSpace(host)), cfg.ImageHosting, ownedHostTrackerConfig(cfg, registry, host)})
	return string(fingerprint), err
}
