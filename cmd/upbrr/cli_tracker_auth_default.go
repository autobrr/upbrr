// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !e2e

package main

import (
	"github.com/autobrr/upbrr/internal/config"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

// newCLITrackerAuthService returns the production tracker-auth implementation.
func newCLITrackerAuthService(cfg config.Config, logger api.Logger) cliTrackerAuthService {
	return trackerauth.NewServiceWithRegistryAndLogger(cfg, trackerimpl.MustNewRegistry(), logger)
}
