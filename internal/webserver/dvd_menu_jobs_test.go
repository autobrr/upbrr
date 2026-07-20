// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/pkg/api"
)

type acceptedDVDCaptureFunc func(context.Context, api.MediaPlanInput) (api.DVDMenuCaptureResult, error)

func (fn acceptedDVDCaptureFunc) CaptureAcceptedDVDMenus(ctx context.Context, input api.MediaPlanInput) (api.DVDMenuCaptureResult, error) {
	return fn(ctx, input)
}

type dvdGenerationTransfer struct{ release api.ReleaseRef }

func (transfer dvdGenerationTransfer) ExportReleaseSeed(context.Context, api.ReleaseRef) (preparedrelease.Seed, error) {
	return preparedrelease.Seed{}, nil
}

func (transfer dvdGenerationTransfer) ImportReleaseSeed(context.Context, preparedrelease.Seed) (api.ReleaseRef, error) {
	return transfer.release, nil
}

func TestWebDVDMenuRunnerPropagatesRequestCancellation(t *testing.T) {
	t.Parallel()
	release := api.ReleaseRef{SourcePath: `C:\Example\Example.Release.2026.1080p-GRP`, Generation: 1}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := webDVDRunner{
		core: acceptedDVDCaptureFunc(func(ctx context.Context, _ api.MediaPlanInput) (api.DVDMenuCaptureResult, error) {
			return api.DVDMenuCaptureResult{}, ctx.Err()
		}),
		target: dvdGenerationTransfer{release: release},
		expected: release,
	}
	_, err := runner.CaptureAcceptedDVDMenus(ctx, api.MediaPlanInput{Release: release})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("capture error = %v", err)
	}
}
