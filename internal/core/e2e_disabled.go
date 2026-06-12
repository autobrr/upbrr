// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !e2e

package core

import (
	"context"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func maybeApplyE2EServices(context.Context, *api.ServiceSet, config.Config, db.MetadataRepository, api.Logger) error {
	return nil
}
