// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !e2e

package main

import (
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/redaction"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

// newCLITrackerAuthService returns the production tracker-auth implementation.
func newCLITrackerAuthService(cfg config.Config, logger api.Logger) cliTrackerAuthService {
	registry, err := trackerimpl.NewRegistryWithConfig(cfg)
	if err != nil {
		if logger != nil {
			logger.Warnf("tracker auth: registry construction failed err=%s", redaction.RedactValue(err.Error(), nil))
		}
		return trackerauth.NewServiceWithLogger(cfg, logger)
	}
	return trackerauth.NewServiceWithRegistryAndLogger(cfg, registry, logger)
}
