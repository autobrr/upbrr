// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/externalidentity"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestIdentityResetAuthoritySurvivesAcceptanceBeforePreparationRestart(t *testing.T) {
	t.Parallel()
	source := writePreparedTestFile(t, "Example.2026.mkv", "media")
	store := newMemoryStore()
	module := newTestModule(t, store, &recordingCollector{})
	accepted, err := module.ResolveInput(t.Context(), api.PrepareInput{SourcePath: source}, api.ReleaseCorrectionUpdate{
		Mode: api.ReleaseCorrectionUpdatePatch,
		Patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{
			{Field: api.CorrectionFieldIdentityTMDB}, {Field: api.CorrectionFieldReleaseNameCategory},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	observed := &identityResetRecorder{}
	restarted := newTestModule(t, store, observed)
	restarted.identity = observed
	prepared, err := restarted.Prepare(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	want := accepted.Corrections.Corrections.IdentityResetFields
	if len(want) != 2 || !slices.Equal(observed.collected, want) || !slices.Equal(observed.resolved, want) || !slices.Equal(prepared.Corrections.Corrections.IdentityResetFields, want) {
		t.Fatal("restart lost reset authority before collection, identity resolution, or commit")
	}
}

type identityResetRecorder struct {
	recordingCollector
	collected []api.CorrectionField
	resolved  []api.CorrectionField
}

func (r *identityResetRecorder) Collect(ctx context.Context, request preparationstate.Request) (CollectedFacts, error) {
	r.collected = slices.Clone(request.IdentityResetFields)
	return r.recordingCollector.Collect(ctx, request)
}

func (r *identityResetRecorder) Resolve(ctx context.Context, request externalidentity.Request) (externalidentity.Result, error) {
	r.resolved = slices.Clone(request.Intent.IdentityResetFields)
	return (staticIdentityResolver{}).Resolve(ctx, request)
}
