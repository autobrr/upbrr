// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import (
	"context"
	"fmt"

	"golang.org/x/sync/errgroup"

	"github.com/autobrr/upbrr/pkg/api"
)

// EnrichCandidates inspects at most 20 candidates with four requests in flight.
// Uninspected candidates remain selectable with partial detail status.
// A detail failure preserves the selectable search result; cancellation terminates the search.
func EnrichCandidates(
	ctx context.Context,
	candidates []api.ExternalIdentityCandidate,
	fetch func(context.Context, *api.ExternalIdentityCandidate) error,
) error {
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	for i := range candidates {
		if i >= 20 {
			candidates[i].DetailsStatus = api.MetadataEvidenceStatusPartial
			continue
		}
		group.Go(func() error {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("candidate enrichment canceled: %w", err)
			}
			candidates[i].DetailsStatus = api.MetadataEvidenceStatusComplete
			if err := fetch(ctx, &candidates[i]); err != nil {
				candidates[i].DetailsStatus = api.MetadataEvidenceStatusPartial
			}
			return ctx.Err()
		})
	}
	if err := group.Wait(); err != nil {
		return fmt.Errorf("candidate enrichment: %w", err)
	}
	return nil
}
