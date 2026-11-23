// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"

	"github.com/autobrr/upbrr/internal/logging"
)

func (m *Module) beginInputLog(ctx context.Context, path, kind string) (context.Context, func(string, string)) {
	root, _ := m.logger.(*logging.Logger)
	var store logging.InputLogRepository
	if repository, ok := m.repository.(*PersistentRepository); ok {
		store, _ = repository.states.(logging.InputLogRepository)
	}
	ctx, _, finish := root.BeginInputLog(ctx, store, path, kind)
	return ctx, finish
}
