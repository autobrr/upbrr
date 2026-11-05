// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"

	"github.com/autobrr/upbrr/pkg/api"
)

// SearchProviderCandidates performs read-only provider inspection; selection remains an explicit correction.
func (c *Core) SearchProviderCandidates(ctx context.Context, request api.ProviderSearchRequest) (api.ProviderSearchResult, error) {
	if c.searchProviderCandidates == nil {
		return api.ProviderSearchResult{}, errors.New("metadata: provider search unavailable")
	}
	return c.searchProviderCandidates(ctx, request)
}
