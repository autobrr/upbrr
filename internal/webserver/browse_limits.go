// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/autobrr/upbrr/pkg/api"
)

var errBrowseBusy = errors.New("host browser is busy; retry when another browse request finishes")

// browseRequestLimiter bounds active filesystem work per server without queuing
// requests. A canceled request retains its slot until its filesystem call returns.
type browseRequestLimiter struct {
	active atomic.Int32
}

func (l *browseRequestLimiter) run(
	ctx context.Context,
	browse func(context.Context) (api.BrowseDirectoryResponse, error),
) (api.BrowseDirectoryResponse, error) {
	if err := ctx.Err(); err != nil {
		return api.BrowseDirectoryResponse{}, fmt.Errorf("browse admission: %w", err)
	}
	if l.active.Add(1) > 2 {
		l.active.Add(-1)
		return api.BrowseDirectoryResponse{}, errBrowseBusy
	}
	defer l.active.Add(-1)
	if err := ctx.Err(); err != nil {
		return api.BrowseDirectoryResponse{}, fmt.Errorf("browse admission: %w", err)
	}
	return browse(ctx)
}
