// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"errors"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

// classifyOperationError maps internal causes to stable frontend-safe recovery
// contracts while retaining the original error as the wrapped cause.
func classifyOperationError(operation api.OperationKind, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := api.AsOperationFailure(err); ok {
		return err
	}
	failure := api.OperationFailure{
		Code:      api.OperationFailureInternal,
		Operation: operation,
		Message:   "The operation could not be completed.",
		Recovery:  api.OperationRecoveryRetry,
	}
	var stale *preparedrelease.StalePreparationError
	var incompatible *preparedrelease.IncompatiblePreparationError
	var missing *api.MissingRequirementError
	var rescan *api.BDMVRescanRequiredError
	var playlistRequired *api.PlaylistSelectionRequiredError
	var invalidPlaylist *api.InvalidPlaylistSelectionError
	var authRequired *trackerauth.AuthRequiredError
	var needs2FA *trackerauth.Needs2FAError
	switch {
	case errors.Is(err, api.ErrPreparationSourceRequired):
		failure.Code = api.OperationFailureInvalidSource
		failure.Message = "A source path is required."
		failure.Recovery = api.OperationRecoveryEditInput
	case errors.Is(err, sourcelayout.ErrSourceNotFound):
		failure.Code = api.OperationFailureInvalidSource
		failure.Message = "The source path is unavailable."
		failure.Recovery = api.OperationRecoveryEditInput
	case errors.As(err, &playlistRequired):
		failure.Code = api.OperationFailureMissingPrerequisite
		failure.Message = "Select at least one Blu-ray playlist before preparing the release."
		failure.Recovery = api.OperationRecoveryCompletePrerequisite
	case errors.As(err, &invalidPlaylist):
		failure.Code = api.OperationFailureInvalidInput
		failure.Message = "The Blu-ray playlist selection is invalid."
		failure.Recovery = api.OperationRecoveryEditInput
	case errors.As(err, &rescan):
		failure.Code = api.OperationFailureConfirmationRequired
		failure.Message = "Blu-ray playlist changes require confirmation before rescanning."
		failure.Recovery = api.OperationRecoveryConfirm
	case errors.As(err, &stale):
		failure.Code = api.OperationFailureStaleGeneration
		failure.Message = "The prepared release changed. Refresh it before continuing."
		failure.Recovery = api.OperationRecoveryRefreshRelease
	case errors.As(err, &incompatible):
		failure.Code = api.OperationFailureIncompatibleGeneration
		failure.Message = "The prepared release is incompatible. Prepare it again."
		failure.Recovery = api.OperationRecoveryRefreshRelease
	case errors.As(err, &missing):
		failure.Code = api.OperationFailureMissingPrerequisite
		failure.Message = missing.Error()
		failure.Recovery = api.OperationRecoveryCompletePrerequisite
	case errors.As(err, &authRequired), errors.As(err, &needs2FA):
		failure.Code = api.OperationFailureTrackerAuthRequired
		failure.Message = "Tracker authentication is required."
		failure.Recovery = api.OperationRecoveryAuthenticateTrackers
	case errors.Is(err, internalerrors.ErrInvalidInput):
		failure.Code = api.OperationFailureInvalidInput
		failure.Message = "The operation input is invalid."
		failure.Recovery = api.OperationRecoveryEditInput
	}
	return api.NewOperationError(failure, err)
}

func classifyOperationResult[T any](operation api.OperationKind, value T, err error) (T, error) {
	return value, classifyOperationError(operation, err)
}
