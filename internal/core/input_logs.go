// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"

	"github.com/autobrr/upbrr/internal/logging"
)

// BeginInputLog scopes an entire CLI input, including its background operations.
func (c *Core) BeginInputLog(ctx context.Context, source string) (context.Context, string, func(string, string)) {
	owner, _ := c.repoOwner.(logging.InputLogRepository)
	logger, _ := c.logger.(*logging.Logger)
	return logger.BeginInputLog(ctx, owner, source, "CLI input")
}
