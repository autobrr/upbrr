// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !e2e

package core

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// E2EFakesEnabled reports whether this build is using fake E2E services.
func E2EFakesEnabled() bool { return false }

func maybeApplyE2EServices(context.Context, *api.ServiceSet, config.Config, api.RepositoryCapabilities, *trackers.Registry, api.Logger) error {
	return nil
}

func e2eReleaseWorkflowOptions(api.RepositoryOwner) []releaseworkflow.Option {
	return nil
}

func maybeApplyE2ENamingRegistry(registry *trackers.Registry) (*trackers.Registry, error) {
	return registry, nil
}
