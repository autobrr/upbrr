// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metautil

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCandidateEnrichmentBoundsRequestsAndHonorsCancellation(t *testing.T) {
	for _, cancelSearch := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "canceled"}[cancelSearch], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started, release := make(chan struct{}, 30), make(chan struct{})
			var count atomic.Int32
			candidates := make([]api.ExternalIdentityCandidate, 30)
			finished := make(chan error, 1)
			go func() {
				finished <- EnrichCandidates(ctx, candidates, func(ctx context.Context, _ *api.ExternalIdentityCandidate) error {
					count.Add(1)
					started <- struct{}{}
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				})
			}()
			for range 4 {
				<-started
			}
			if count.Load() != 4 {
				t.Fatalf("more than four details in flight: %d", count.Load())
			}
			if cancelSearch {
				cancel()
			} else {
				close(release)
			}
			err := <-finished
			if cancelSearch {
				if !errors.Is(err, context.Canceled) || count.Load() != 4 {
					t.Fatalf("cancellation=%v calls=%d", err, count.Load())
				}
				return
			}
			if err != nil || count.Load() != 20 || len(candidates) != 30 {
				t.Fatalf("request/list bound: err=%v calls=%d candidates=%d", err, count.Load(), len(candidates))
			}
			if candidates[20].DetailsStatus != api.MetadataEvidenceStatusPartial {
				t.Fatal("unfetched details reported complete")
			}
		})
	}
}
