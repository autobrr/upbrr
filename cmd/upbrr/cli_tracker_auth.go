// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"fmt"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/redaction"
	trackerauth "github.com/autobrr/upbrr/internal/trackers/auth"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliTrackerAuthService interface {
	Capabilities(context.Context) ([]api.TrackerAuthCapability, error)
	ValidateMany(context.Context, []string) ([]api.TrackerAuthStatus, error)
	Submit2FA(context.Context, string, string) (api.TrackerAuthStatus, error)
}

type cliTrackerAuthChecker func(
	context.Context,
	*bufio.Reader,
	config.Config,
	api.InteractionMode,
	[]string,
	api.MetadataPreview,
	api.Logger,
) ([]string, error)

func (s *cliWorkflowSession) ensureTrackerAuthBeforeDupeCheck(
	ctx context.Context,
	reader *bufio.Reader,
	cfg config.Config,
	interaction api.InteractionMode,
	trackerNames []string,
	preview api.MetadataPreview,
	logger api.Logger,
) ([]string, error) {
	if s.trackerAuth != nil {
		return s.trackerAuth(ctx, reader, cfg, interaction, trackerNames, preview, logger)
	}
	return ensureCLITrackerAuthBeforeDupeCheckWithLogger(ctx, reader, cfg, interaction, trackerNames, preview, logger)
}

func ensureCLITrackerAuthBeforeDupeCheckWithLogger(
	ctx context.Context,
	reader *bufio.Reader,
	cfg config.Config,
	interaction api.InteractionMode,
	trackerNames []string,
	preview api.MetadataPreview,
	logger api.Logger,
) ([]string, error) {
	if len(trackerNames) == 0 {
		return trackerNames, nil
	}
	logger = cliAuthLogger(logger)
	return ensureCLITrackerAuthBeforeDupeCheckWithServiceAndLogger(
		ctx,
		reader,
		newCLITrackerAuthService(cfg, logger),
		interaction,
		trackerNames,
		preview,
		logger,
	)
}

func ensureCLITrackerAuthBeforeDupeCheckWithServiceAndLogger(
	ctx context.Context,
	reader *bufio.Reader,
	authSvc cliTrackerAuthService,
	interaction api.InteractionMode,
	trackerNames []string,
	_ api.MetadataPreview,
	logger api.Logger,
) ([]string, error) {
	logger = cliAuthLogger(logger)
	capabilities, err := authSvc.Capabilities(ctx)
	if err != nil {
		logger.Warnf("cli auth: capability load failed error=%s", cliAuthLogError(err))
		return nil, fmt.Errorf("upbrr: tracker auth capabilities: %w", err)
	}
	capabilityByTracker := make(map[string]api.TrackerAuthCapability, len(capabilities))
	for _, capability := range capabilities {
		if name := strings.ToUpper(strings.TrimSpace(capability.TrackerID)); name != "" {
			capabilityByTracker[name] = capability
		}
	}

	readyByTracker := make(map[string]struct{}, len(trackerNames))
	authCheckTrackers := make([]string, 0, len(trackerNames))
	for _, trackerName := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(trackerName))
		capability, managed := capabilityByTracker[name]
		if name == "" {
			continue
		}
		if !managed || !cliTrackerAuthApplies(capability) {
			readyByTracker[name] = struct{}{}
			continue
		}
		authCheckTrackers = append(authCheckTrackers, name)
	}

	statuses, err := authSvc.ValidateMany(ctx, authCheckTrackers)
	if err != nil {
		logger.Warnf("cli auth: concurrent validation failed error=%s", cliAuthLogError(err))
		return nil, fmt.Errorf("upbrr: tracker auth validation: %w", err)
	}
	if len(statuses) != len(authCheckTrackers) {
		return nil, fmt.Errorf("upbrr: tracker auth validation returned %d statuses for %d trackers", len(statuses), len(authCheckTrackers))
	}
	for index, name := range authCheckTrackers {
		status := statuses[index]
		logCLITrackerAuthStatus(logger, "validation result", status)
		status, keep, err := handleCLITrackerAuthStatusWithLogger(
			ctx,
			reader,
			authSvc,
			capabilityByTracker[name],
			status,
			interaction,
			logger,
		)
		if err != nil {
			return nil, err
		}
		if cliTrackerAuthReady(status) || keep {
			readyByTracker[name] = struct{}{}
		}
	}

	ready := make([]string, 0, len(trackerNames))
	for _, trackerName := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(trackerName))
		if _, ok := readyByTracker[name]; ok {
			ready = append(ready, name)
		}
	}
	logger.Debugf("cli auth: pre-dupe check complete ready=%d skipped=%d", len(ready), len(trackerNames)-len(ready))
	return ready, nil
}

func cliTrackerAuthApplies(capability api.TrackerAuthCapability) bool {
	return trackerauth.IsManagedCapability(capability)
}

func handleCLITrackerAuthStatusWithLogger(
	ctx context.Context,
	reader *bufio.Reader,
	authSvc cliTrackerAuthService,
	capability api.TrackerAuthCapability,
	status api.TrackerAuthStatus,
	interaction api.InteractionMode,
	logger api.Logger,
) (api.TrackerAuthStatus, bool, error) {
	logger = cliAuthLogger(logger)
	if cliTrackerAuthReady(status) {
		return status, true, nil
	}
	trackerID := strings.ToUpper(strings.TrimSpace(status.TrackerID))
	if trackerID == "" {
		trackerID = strings.ToUpper(strings.TrimSpace(capability.TrackerID))
	}
	if status.Needs2FA && strings.TrimSpace(status.ChallengeID) != "" {
		if isUnattendedNoConfirm(interaction) {
			logger.Warnf("cli auth: tracker=%s decision=skip reason=2fa_required unattended=true", trackerID)
			return status, false, nil
		}
		logger.Infof("cli auth: tracker=%s decision=prompt_2fa", trackerID)
		return promptCLITrackerAuth2FAWithLogger(ctx, reader, authSvc, trackerID, status, logger)
	}
	message := cliTrackerAuthStatusMessage(status)
	if isUnattendedNoConfirm(interaction) {
		logger.Warnf("cli auth: tracker=%s decision=skip reason=%s unattended=true", trackerID, cliAuthStatusMessageForLog(status))
		return status, false, nil
	}
	fmt.Printf("Skipping %s before dupe check: tracker auth not ready (%s).\n", trackerID, message)
	return status, false, nil
}

func promptCLITrackerAuth2FAWithLogger(
	ctx context.Context,
	reader *bufio.Reader,
	authSvc cliTrackerAuthService,
	trackerID string,
	status api.TrackerAuthStatus,
	logger api.Logger,
) (api.TrackerAuthStatus, bool, error) {
	logger = cliAuthLogger(logger)
	for {
		fmt.Printf("\n[%s Auth]\n%s\n", trackerID, cliTrackerAuthStatusMessage(status))
		code, err := promptLine(reader, trackerID+" 2FA code (blank to skip tracker): ")
		if err != nil {
			logger.Warnf("cli auth: tracker=%s 2fa prompt failed error=%s", trackerID, cliAuthLogError(err))
			return status, false, err
		}
		if strings.TrimSpace(code) == "" {
			logger.Warnf("cli auth: tracker=%s decision=skip reason=2fa_code_not_provided", trackerID)
			fmt.Printf("Skipping %s before dupe check: 2FA code not provided.\n", trackerID)
			return status, false, nil
		}
		nextStatus, err := authSvc.Submit2FA(ctx, status.ChallengeID, code)
		if err != nil {
			logger.Warnf("cli auth: tracker=%s 2fa submit failed error=%s", trackerID, cliAuthLogError(err))
			return status, false, fmt.Errorf("upbrr: tracker auth %s 2FA: %w", trackerID, err)
		}
		status = nextStatus
		logCLITrackerAuthStatus(logger, "2fa result", status)
		if cliTrackerAuthReady(status) {
			fmt.Printf("%s auth ready.\n", trackerID)
			return status, true, nil
		}
		if !status.Needs2FA || strings.TrimSpace(status.ChallengeID) == "" {
			fmt.Printf("Skipping %s before dupe check: tracker auth not ready (%s).\n", trackerID, cliTrackerAuthStatusMessage(status))
			return status, false, nil
		}
	}
}

func cliAuthLogger(logger api.Logger) api.Logger {
	if logger == nil {
		return api.NopLogger{}
	}
	return logger
}

func logCLITrackerAuthStatus(logger api.Logger, operation string, status api.TrackerAuthStatus) {
	logger = cliAuthLogger(logger)
	logger.Debugf(
		"cli auth: %s tracker=%s state=%s cookies=%d encrypted_storage=%t needs_2fa=%t",
		cliAuthLogField(operation),
		cliAuthLogTrackerID(status.TrackerID),
		cliAuthLogField(status.State),
		status.CookieCount,
		status.EncryptedStorage,
		status.Needs2FA,
	)
}

func cliAuthLogTrackerID(trackerID string) string {
	trackerID = strings.ToUpper(strings.TrimSpace(trackerID))
	if trackerID == "" {
		return "unknown"
	}
	return cliAuthLogField(trackerID)
}

func cliAuthStatusMessageForLog(status api.TrackerAuthStatus) string {
	return cliTrackerAuthStatusMessage(status)
}

func cliAuthLogError(err error) string {
	if err == nil {
		return ""
	}
	return cliAuthLogField(err.Error())
}

func cliAuthLogField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return redaction.RedactValue(value, nil)
}

func cliTrackerAuthReady(status api.TrackerAuthStatus) bool {
	return trackerauth.IsReadyStatus(status)
}

func cliTrackerAuthStatusMessage(status api.TrackerAuthStatus) string {
	message := cliTrackerAuthDisplayField(status.Message)
	detail := cliTrackerAuthDisplayField(status.LastError)
	if message != "" && detail != "" && !strings.EqualFold(message, detail) {
		return message + ": " + detail
	}
	if message != "" {
		return message
	}
	if detail != "" {
		return detail
	}
	if state := cliTrackerAuthDisplayField(status.State); state != "" {
		return state
	}
	return "auth not ready"
}

func cliTrackerAuthDisplayField(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return redaction.RedactValue(trimmed, nil)
}

func isUnattendedNoConfirm(interaction api.InteractionMode) bool {
	return interaction == api.InteractionModeUnattended
}
