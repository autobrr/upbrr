// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestBrowseLimitRetainsCanceledWorkUntilReturn(t *testing.T) {
	t.Parallel()
	var limiter browseRequestLimiter
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	finished := make(chan error, 2)
	defer close(release)
	for range 2 {
		go func() {
			_, err := limiter.run(ctx, func(ctx context.Context) (api.BrowseDirectoryResponse, error) {
				started <- struct{}{}
				<-release // Model a filesystem call that cannot observe cancellation.
				return api.BrowseDirectoryResponse{}, ctx.Err()
			})
			finished <- err
		}()
	}
	for range 2 {
		<-started
	}
	cancel()
	called := false
	browse := func(context.Context) (api.BrowseDirectoryResponse, error) {
		called = true
		return api.BrowseDirectoryResponse{Scope: "roots"}, nil
	}
	if _, err := limiter.run(t.Context(), browse); !errors.Is(err, errBrowseBusy) || called {
		t.Fatalf("canceled but blocked work must retain both slots: called=%v, err=%v", called, err)
	}
	for range 2 {
		release <- struct{}{}
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked request result: %v", err)
		}
	}
	result, err := limiter.run(t.Context(), browse)
	if err != nil || !called || result.Scope != "roots" || limiter.active.Load() != 0 {
		t.Fatalf("slots not reusable after completion: %#v, %v", result, err)
	}
}

func TestBrowseLimitRejectsCanceledAdmissionAndReleasesErrors(t *testing.T) {
	t.Parallel()
	var limiter browseRequestLimiter
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	called := false
	wantErr := errors.New("filesystem unavailable")
	browse := func(context.Context) (api.BrowseDirectoryResponse, error) {
		called = true
		return api.BrowseDirectoryResponse{}, wantErr
	}
	if _, err := limiter.run(ctx, browse); !errors.Is(err, context.Canceled) || called || limiter.active.Load() != 0 {
		t.Fatalf("canceled admission: called=%v, err=%v", called, err)
	}
	if _, err := limiter.run(t.Context(), browse); !errors.Is(err, wantErr) || !called || limiter.active.Load() != 0 {
		t.Fatalf("failed work must release admission: called=%v, err=%v", called, err)
	}
}
