// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"fmt"

	"github.com/autobrr/upbrr/internal/logging"

	"github.com/autobrr/upbrr/internal/metadata/evidence"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

// CollectPreparationEvidence owns the complete ordered metadata collection
// sequence. Canonical preparation sees one deep collection port; intermediate
// mutable evidence and step ordering do not escape this package.
func (s *Service) CollectPreparationEvidence(ctx context.Context, request preparationstate.Request) (result preparationstate.State, resultErr error) {
	logger := logging.FromContext(ctx, s.logger)

	store, _ := s.repo.(api.MetadataEvidenceRepository)
	fingerprint := request.SourceFingerprint
	if fingerprint == "" || request.Manifest.SourcePath == "" {
		store = nil
	}
	if request.Input.VerifiedSource != nil {
		fingerprint += ":" + request.Input.VerifiedSource.Identity.Digest
	}
	ctx, scope := evidence.WithScope(ctx, store, request.Manifest.SourcePath, fingerprint, request.Input.ExternalFreshness, logger)
	defer func() {
		if err := scope.Err(); err != nil {
			result = preparationstate.State{}
			resultErr = fmt.Errorf("metadata: evidence persistence: %w", err)
		}
	}()
	state, err := collectPreparationStage(ctx, api.PreparationPhaseSourceEvidence, func() (preparationstate.State, error) {
		return s.collectSourceEvidence(ctx, request)
	})
	if err != nil {
		return preparationstate.State{}, err
	}
	capturedDiscs := state.Discs
	defer func() {
		if resultErr != nil || scope.Err() != nil {
			for _, disc := range capturedDiscs {
				preparationstate.ReleaseHDRCaptures(disc.HDRCaptures)
			}
		}
	}()
	state, err = collectPreparationStage(ctx, api.PreparationPhaseClientDiscovery, func() (preparationstate.State, error) {
		if request.RetainedClientEvidence != nil {
			applyClientEvidenceSnapshot(&state, *request.RetainedClientEvidence)
			return state, nil
		}
		return s.collectClientEvidence(ctx, request.Input, state)
	})
	if err != nil {
		return preparationstate.State{}, err
	}
	state, err = collectPreparationStage(ctx, api.PreparationPhaseTrackerEvidence, func() (preparationstate.State, error) {
		return s.collectTrackerEvidence(ctx, state)
	})
	if err != nil {
		return preparationstate.State{}, err
	}
	state, err = collectPreparationStage(ctx, api.PreparationPhaseMediaInfoIdentity, func() (preparationstate.State, error) {
		return s.collectMediaInfoIdentityEvidence(ctx, state)
	})
	if err != nil {
		return preparationstate.State{}, err
	}
	state, err = collectPreparationStage(ctx, api.PreparationPhaseArrIdentity, func() (preparationstate.State, error) {
		return s.collectArrIdentityEvidence(ctx, state)
	})
	if err != nil {
		return preparationstate.State{}, err
	}
	state, err = collectPreparationStage(ctx, api.PreparationPhaseExternalIdentity, func() (preparationstate.State, error) {
		return s.collectExternalIdentityEvidence(ctx, state)
	})
	if err != nil {
		return preparationstate.State{}, err
	}
	return collectPreparationStage(ctx, api.PreparationPhaseMediaFacts, func() (preparationstate.State, error) {
		return s.deriveMediaFacts(ctx, state)
	})
}

// HydratePrivateResources reconstructs restart-only local artifacts and client
// evidence without rerunning tracker, provider, or canonical identity stages.
func (s *Service) HydratePrivateResources(
	ctx context.Context,
	request preparationstate.Request,
) (result preparationstate.State, resultErr error) {
	state, err := s.collectSourceEvidence(ctx, request)
	if err != nil {
		return preparationstate.State{}, err
	}
	capturedDiscs := state.Discs
	defer func() {
		if resultErr != nil {
			for _, disc := range capturedDiscs {
				preparationstate.ReleaseHDRCaptures(disc.HDRCaptures)
			}
		}
	}()
	return s.collectClientEvidence(ctx, request.Input, state)
}

// collectPreparationStage brackets one collection step with advisory progress
// while preserving the collector's state and error result unchanged.
func collectPreparationStage(
	ctx context.Context,
	phase api.PreparationProgressPhase,
	collect func() (preparationstate.State, error),
) (state preparationstate.State, err error) {
	finish := api.BeginPreparationProgress(ctx, phase, "Stage started.")
	defer func() { finish(err) }()
	return collect()
}
