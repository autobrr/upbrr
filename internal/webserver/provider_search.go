// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/autobrr/upbrr/pkg/api"
)

// SearchProviderCandidates borrows the active configuration for one cancelable, read-only search.
func (b *Backend) SearchProviderCandidates(ctx context.Context, request api.ProviderSearchRequest) (api.ProviderSearchResult, error) {
	rt, err := b.borrowRuntime()
	if err != nil {
		return api.ProviderSearchResult{}, err
	}
	defer rt.release()
	if !CapabilityAvailable(rt.capabilities.ProviderSearch) {
		return api.ProviderSearchResult{}, errors.New("provider search unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, err := rt.capabilities.ProviderSearch.SearchProviderCandidates(ctx, request)
	if err != nil {
		return api.ProviderSearchResult{}, fmt.Errorf("webserver: provider search: %w", err)
	}
	return result, nil
}
