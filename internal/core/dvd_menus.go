// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"

	"github.com/autobrr/upbrr/pkg/api"
)

// DVDMenuCapability reports the pure-Go engine version and the external
// FFmpeg dvdvideo menu capability without returning executable paths.
func (c *Core) DVDMenuCapability(ctx context.Context) (api.DVDMenuEngineInfo, error) {
	return c.media.dvdMenuCapability(ctx)
}
