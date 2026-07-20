// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

// CollectPreparationEvidence owns the complete ordered metadata collection
// sequence. Canonical preparation sees one deep collection port; intermediate
// mutable evidence and step ordering do not escape this package.
func (s *Service) CollectPreparationEvidence(ctx context.Context, request preparationstate.Request) (preparationstate.State, error) {
	state, err := collectPreparationStage(ctx, api.PreparationPhaseSourceEvidence, func() (preparationstate.State, error) {
		return s.collectSourceEvidence(ctx, request)
	})
	if err != nil {
		return preparationstate.State{}, err
	}
	state, err = collectPreparationStage(ctx, api.PreparationPhaseClientDiscovery, func() (preparationstate.State, error) {
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
