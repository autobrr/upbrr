// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackerauth"
	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/pkg/api"
)

const dryRunPayloadPreviewLimit = 240

// cliTrackerAuthService is the CLI-facing subset of tracker auth operations
// needed to verify and refresh tracker auth before dupe checking.
type cliTrackerAuthService interface {
	Capabilities(ctx context.Context) ([]api.TrackerAuthCapability, error)
	Validate(ctx context.Context, trackerID string) (api.TrackerAuthStatus, error)
	Submit2FA(ctx context.Context, challengeID string, code string) (api.TrackerAuthStatus, error)
}

func runInteractiveCLIPath(ctx context.Context, coreSvc api.Core, opts cliOptions, visited map[string]bool, sourcePath string, cfg config.Config) error {
	return runInteractiveCLIPathWithInputAndLogger(ctx, coreSvc, nil, opts, visited, sourcePath, 1, cfg, os.Stdin, api.NopLogger{})
}

// runInteractiveCLIPathWithLogger runs one interactive CLI upload path and
// sends CLI-only tracker auth decisions to logger.
func runInteractiveCLIPathWithLogger(ctx context.Context, coreSvc api.Core, baseArgs []string, opts cliOptions, visited map[string]bool, sourcePath string, screens int, cfg config.Config, logger api.Logger) error {
	return runInteractiveCLIPathWithInputAndLogger(ctx, coreSvc, baseArgs, opts, visited, sourcePath, screens, cfg, os.Stdin, logger)
}

func runInteractiveCLIPathWithInput(ctx context.Context, coreSvc api.Core, opts cliOptions, visited map[string]bool, sourcePath string, screens int, cfg config.Config, stdin io.Reader) error {
	return runInteractiveCLIPathWithInputAndLogger(ctx, coreSvc, nil, opts, visited, sourcePath, screens, cfg, stdin, api.NopLogger{})
}

// runInteractiveCLIPathWithInputAndLogger is the injectable form of
// runInteractiveCLIPathWithLogger used when tests need controlled stdin.
func runInteractiveCLIPathWithInputAndLogger(ctx context.Context, coreSvc api.Core, baseArgs []string, opts cliOptions, visited map[string]bool, sourcePath string, screens int, cfg config.Config, stdin io.Reader, logger api.Logger) error {
	logger = cliAuthLogger(logger)
	reader := bufio.NewReader(stdin)
	currentArgs := append([]string(nil), baseArgs...)
	currentOpts := opts
	currentVisited := copyVisited(visited)
	var metadataPreview api.MetadataPreview

	for {
		req, err := buildCLIRequest(currentOpts, currentVisited, []string{sourcePath}, screens)
		if err != nil {
			return err
		}
		preview, err := coreSvc.FetchMetadataPreview(ctx, req)
		if err != nil {
			var rescanErr *api.BDMVRescanRequiredError
			if errors.As(err, &rescanErr) && currentOpts.interactionMode() != api.InteractionModeUnattended {
				confirm, promptErr := promptYesNo(reader, fmt.Sprintf("Cached BDMV summaries exist, but selected playlist(s) %s require a rescan. Rescan now? [Y/n]: ", strings.Join(rescanErr.MissingPlaylists, ", ")), true)
				if promptErr != nil {
					return promptErr
				}
				if !confirm {
					return fmt.Errorf("upbrr: %w", err)
				}
				currentOpts.ConfirmBDMVRescan = true
				continue
			}
			return fmt.Errorf("upbrr: %w", err)
		}
		metadataPreview = preview

		printMetadataPreview(preview, currentOpts.Debug)
		if currentOpts.Unattended && !currentOpts.UnattendedConfirm {
			break
		}
		confirmed, err := promptYesNo(reader, "Metadata correct? [Y/n]: ", true)
		if err != nil {
			return err
		}
		if confirmed {
			break
		}

		editArgs, err := promptLine(reader, "Input args that need correction (e.g. --tag NTb --category tv --tmdb 12345), or 'continue': ")
		if err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(editArgs), "continue") {
			break
		}
		if strings.TrimSpace(editArgs) == "" {
			fmt.Println("No input provided.")
			continue
		}

		editTokens, err := splitInteractiveCLIArgs(editArgs)
		if err != nil {
			fmt.Printf("Invalid override args: %v\n", err)
			continue
		}
		nextArgs := append(append([]string(nil), currentArgs...), editTokens...)
		nextOpts, nextVisited, _, err := parseCLIOptions(nextArgs)
		if err != nil {
			fmt.Printf("Invalid override args: %v\n", err)
			continue
		}
		nextOpts.ConfirmBDMVRescan = currentOpts.ConfirmBDMVRescan
		currentArgs = nextArgs
		currentOpts = nextOpts
		currentVisited = nextVisited
	}

	uploadSourcePath := resolvedCLIMetadataSourcePath(sourcePath, metadataPreview)
	req, err := buildCLIRequest(currentOpts, currentVisited, []string{uploadSourcePath}, screens)
	if err != nil {
		return err
	}

	candidateTrackers, removalBase := resolveCLIUploadTrackers(currentVisited, req, metadataPreview, cfg)
	if len(candidateTrackers) == 0 {
		fmt.Printf("No trackers configured for %s\n", formatPathLabel(sourcePath))
		return nil
	}
	req.Trackers = candidateTrackers
	req.TrackersRemove = appendTrackerRemovals(req.TrackersRemove, unselectedTrackers(removalBase, candidateTrackers)...)

	readyAuthTrackers, err := ensureCLITrackerAuthBeforeDupeCheckWithLogger(ctx, reader, cfg, req, candidateTrackers, metadataPreview, logger)
	if err != nil {
		return err
	}
	candidateTrackers = removeUnreadyCLIAuthTrackers(candidateTrackers, readyAuthTrackers)
	req.TrackersRemove = appendTrackerRemovals(req.TrackersRemove, unselectedTrackers(req.Trackers, candidateTrackers)...)
	req.Trackers = candidateTrackers
	if len(candidateTrackers) == 0 {
		fmt.Printf("No trackers selected for %s\n", formatPathLabel(sourcePath))
		return nil
	}

	dupeSummary, err := runCLIDupeCheck(ctx, coreSvc, req)
	if err != nil {
		return err
	}
	var approved, ignoreDupesFor, ruleOverrides []string
	if req.Options.Debug || req.Options.DryRun {
		resultByTracker := mapDupeResultsByTracker(dupeSummary)
		printUnattendedDupeReviewSummary(resultByTracker, candidateTrackers)
		approved = debugDryRunApprovedTrackers(resultByTracker, candidateTrackers)
		ignoreDupesFor = appendTrackerRemovals(nil, approved...)
	} else {
		approved, ignoreDupesFor, ruleOverrides, err = promptTrackerDupeReview(reader, dupeSummary, req, candidateTrackers, nil)
		if err != nil {
			return err
		}
	}
	if len(approved) == 0 {
		fmt.Printf("No trackers selected for %s\n", formatPathLabel(sourcePath))
		return nil
	}

	req.Trackers = approved
	req.TrackersRemove = appendTrackerRemovals(req.TrackersRemove, unselectedTrackers(candidateTrackers, approved)...)
	req.IgnoreDupesFor = ignoreDupesFor
	req.IgnoreTrackerRuleFailuresFor = ruleOverrides

	if req.DoubleDupeCheck && len(approved) > 0 {
		approved, err = runDoubleDupeCheck(ctx, reader, coreSvc, req, approved)
		if err != nil {
			return err
		}
		req.IgnoreDupesFor = appendTrackerRemovals(req.IgnoreDupesFor, approved...)
		req.Trackers = approved
		req.TrackersRemove = appendTrackerRemovals(req.TrackersRemove, unselectedTrackers(candidateTrackers, approved)...)
	}
	if len(approved) == 0 {
		fmt.Printf("No trackers selected for %s\n", formatPathLabel(sourcePath))
		return nil
	}

	if req.Options.Debug || !req.Options.DryRun {
		if err := runCLIScreenshotHandling(ctx, coreSvc, req); err != nil {
			return err
		}
	}

	review, err := coreSvc.BuildUploadReview(ctx, req)
	if err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}

	// Apply questionnaire answers before description editing and upload. When
	// answers change, rebuild the review so printed dry-run/debug state matches
	// the request that will be uploaded.
	questionnaireAnswers, questionnaireChanged, err := promptTrackerQuestionnaires(reader, review, currentOpts)
	if err != nil {
		return err
	}
	if questionnaireChanged || len(questionnaireAnswers) > 0 {
		req.TrackerQuestionnaireAnswers = questionnaireAnswers
	}
	if questionnaireChanged {
		review, err = coreSvc.BuildUploadReview(ctx, req)
		if err != nil {
			return fmt.Errorf("upbrr: rebuild upload review: %w", err)
		}
	}

	req, review, err = maybeEditCLIDescriptions(ctx, coreSvc, reader, req, review, currentOpts)
	if err != nil {
		return err
	}

	req.Trackers = approved
	if len(questionnaireAnswers) > 0 {
		req.TrackerQuestionnaireAnswers = questionnaireAnswers
	}

	if req.Options.Debug {
		printDebugUploadReview(review)
		_, err = coreSvc.RunUploadPrepared(ctx, req)
		return wrapUpbrrError(err)
	}
	if req.Options.DryRun {
		printDryRunUploadReview(review, req)
		_, err = coreSvc.RunUploadPrepared(ctx, req)
		return wrapUpbrrError(err)
	}

	_, err = coreSvc.RunUploadPrepared(ctx, req)
	return wrapUpbrrError(err)
}

func resolvedCLIMetadataSourcePath(input string, preview api.MetadataPreview) string {
	if trimmed := strings.TrimSpace(preview.SourcePath); trimmed != "" {
		return trimmed
	}
	return input
}

func resolveCLIUploadTrackers(visited map[string]bool, req api.Request, preview api.MetadataPreview, cfg config.Config) ([]string, []string) {
	remove := append([]string{}, req.TrackersRemove...)
	if !req.Options.Debug && !req.Options.DryRun {
		remove = append(remove, matchedPreviewTrackers(preview)...)
	}
	removalBase := trackers.ResolveTrackersWithDefaults(cfg, req.Trackers, remove, api.NopLogger{})
	available := removalBase
	if visited["trackers"] || req.Execution.SiteUploadTracker != "" {
		available = trackers.ResolveTrackers(cfg, req.Trackers, remove, api.NopLogger{})
	}
	return available, removalBase
}

// trackerRuleFailuresForPreview returns preview-time rule failures for a
// tracker, accepting mixed-case map keys from older or test-built payloads.
func trackerRuleFailuresForPreview(preview api.MetadataPreview, tracker string) []api.RuleFailure {
	if len(preview.TrackerRuleFailures) == 0 {
		return nil
	}
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if failures, ok := preview.TrackerRuleFailures[name]; ok {
		return failures
	}
	for key, failures := range preview.TrackerRuleFailures {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return failures
		}
	}
	return nil
}

// cliTrackerRuleFailuresIgnored reports whether a preview rule failure should
// be bypassed for a tracker, using the global flag or a per-tracker override.
func cliTrackerRuleFailuresIgnored(req api.Request, tracker string) bool {
	if req.IgnoreTrackerRuleFailures {
		return true
	}
	name := strings.ToUpper(strings.TrimSpace(tracker))
	if name == "" {
		return false
	}
	for _, allowed := range req.IgnoreTrackerRuleFailuresFor {
		if strings.EqualFold(strings.TrimSpace(allowed), name) {
			return true
		}
	}
	return false
}

// removeUnreadyCLIAuthTrackers keeps only trackers marked ready to continue
// after auth filtering, including unmanaged trackers that need no auth call.
// An empty ready list means auth filtering ran and no candidate remained ready.
func removeUnreadyCLIAuthTrackers(candidateTrackers []string, readyTrackers []string) []string {
	if len(candidateTrackers) == 0 {
		return candidateTrackers
	}
	ready := make(map[string]struct{}, len(readyTrackers))
	for _, tracker := range readyTrackers {
		if name := strings.ToUpper(strings.TrimSpace(tracker)); name != "" {
			ready[name] = struct{}{}
		}
	}

	filtered := make([]string, 0, len(candidateTrackers))
	for _, tracker := range candidateTrackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if _, isReady := ready[name]; !isReady {
			continue
		}
		filtered = append(filtered, name)
	}
	return filtered
}

// ensureCLITrackerAuthBeforeDupeCheckWithLogger validates tracker auth using a
// service that shares the CLI logger for service-level and CLI decision logs.
func ensureCLITrackerAuthBeforeDupeCheckWithLogger(ctx context.Context, reader *bufio.Reader, cfg config.Config, req api.Request, trackerNames []string, preview api.MetadataPreview, logger api.Logger) ([]string, error) {
	if len(trackerNames) == 0 {
		return trackerNames, nil
	}
	logger = cliAuthLogger(logger)
	return ensureCLITrackerAuthBeforeDupeCheckWithServiceAndLogger(ctx, reader, trackerauth.NewServiceWithLogger(cfg, logger), req, trackerNames, preview, logger)
}

// ensureCLITrackerAuthBeforeDupeCheckWithService is the injectable form of
// ensureCLITrackerAuthBeforeDupeCheck used by tests.
func ensureCLITrackerAuthBeforeDupeCheckWithService(ctx context.Context, reader *bufio.Reader, authSvc cliTrackerAuthService, req api.Request, trackerNames []string) ([]string, error) {
	return ensureCLITrackerAuthBeforeDupeCheckWithServiceAndLogger(ctx, reader, authSvc, req, trackerNames, api.MetadataPreview{}, api.NopLogger{})
}

// ensureCLITrackerAuthBeforeDupeCheckWithServiceAndLogger applies CLI-specific
// keep/skip behavior and logs redacted outcomes for managed-auth trackers.
// Preview rule failures suppress managed-auth checks only after capability
// classification and leave those managed trackers out of the ready result, so
// static API-key/passkey trackers stay quiet here.
func ensureCLITrackerAuthBeforeDupeCheckWithServiceAndLogger(ctx context.Context, reader *bufio.Reader, authSvc cliTrackerAuthService, req api.Request, trackerNames []string, preview api.MetadataPreview, logger api.Logger) ([]string, error) {
	logger = cliAuthLogger(logger)

	capabilities, err := authSvc.Capabilities(ctx)
	if err != nil {
		logger.Warnf("cli auth: capability load failed error=%s", cliAuthLogError(err))
		return nil, fmt.Errorf("upbrr: tracker auth capabilities: %w", err)
	}
	logger.Debugf("cli auth: capabilities loaded count=%d", len(capabilities))
	capabilityByTracker := make(map[string]api.TrackerAuthCapability, len(capabilities))
	for _, capability := range capabilities {
		name := strings.ToUpper(strings.TrimSpace(capability.TrackerID))
		if name != "" {
			capabilityByTracker[name] = capability
		}
	}

	readyByTracker := make(map[string]struct{}, len(trackerNames))
	authCheckTrackers := make([]string, 0, len(trackerNames))
	for _, trackerName := range trackerNames {
		name := strings.ToUpper(strings.TrimSpace(trackerName))
		if name == "" {
			continue
		}
		capability, ok := capabilityByTracker[name]
		if !ok {
			readyByTracker[name] = struct{}{}
			continue
		}
		if !cliTrackerAuthApplies(capability) {
			readyByTracker[name] = struct{}{}
			continue
		}
		if !cliTrackerRuleFailuresIgnored(req, name) && len(trackerRuleFailuresForPreview(preview, name)) > 0 {
			logger.Debugf("cli auth: tracker=%s skipped before auth due to rule failure", name)
			continue
		}
		authCheckTrackers = append(authCheckTrackers, name)
	}

	logger.Debugf("cli auth: pre-dupe auth check start trackers=%d", len(authCheckTrackers))
	for _, name := range authCheckTrackers {
		capability := capabilityByTracker[name]

		logger.Debugf("cli auth: validating tracker=%s auth_kind=%s", name, cliAuthLogField(capability.AuthKind))
		status, err := authSvc.Validate(ctx, name)
		if err != nil {
			logger.Warnf("cli auth: validation failed tracker=%s error=%s", name, cliAuthLogError(err))
			return nil, fmt.Errorf("upbrr: tracker auth %s: %w", name, err)
		}
		logCLITrackerAuthStatus(logger, "validation result", status)
		status, keep, err := handleCLITrackerAuthStatusWithLogger(ctx, reader, authSvc, capability, status, req, logger)
		if err != nil {
			return nil, err
		}
		if cliTrackerAuthReady(status) {
			logger.Debugf("cli auth: tracker=%s decision=ready state=%s", name, cliAuthLogField(status.State))
			readyByTracker[name] = struct{}{}
			continue
		}
		if keep {
			logger.Infof("cli auth: tracker=%s decision=keep state=%s", name, cliAuthLogField(status.State))
			readyByTracker[name] = struct{}{}
			continue
		}
		logger.Warnf("cli auth: tracker=%s decision=skip state=%s reason=%s", name, cliAuthLogField(status.State), cliAuthStatusMessageForLog(status))
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

// cliTrackerAuthApplies reports whether a capability represents managed auth
// work, matching the frontend tracker-auth settings filter instead of static
// API-key/passkey config.
func cliTrackerAuthApplies(capability api.TrackerAuthCapability) bool {
	authKind := strings.ToLower(strings.TrimSpace(capability.AuthKind))
	return capability.SupportsCookieFile ||
		capability.SupportsLogin ||
		capability.SupportsAutoLogin ||
		capability.SupportsTOTP ||
		capability.SupportsManual2FA ||
		strings.Contains(authKind, "refresh") ||
		strings.Contains(authKind, "2fa")
}

// handleCLITrackerAuthStatusWithLogger converts one auth status into a CLI
// decision and logs blocked, prompt, and skip outcomes without secret material.
func handleCLITrackerAuthStatusWithLogger(ctx context.Context, reader *bufio.Reader, authSvc cliTrackerAuthService, capability api.TrackerAuthCapability, status api.TrackerAuthStatus, req api.Request, logger api.Logger) (api.TrackerAuthStatus, bool, error) {
	logger = cliAuthLogger(logger)
	if cliTrackerAuthReady(status) {
		return status, true, nil
	}
	trackerID := strings.ToUpper(strings.TrimSpace(status.TrackerID))
	if trackerID == "" {
		trackerID = strings.ToUpper(strings.TrimSpace(capability.TrackerID))
	}

	if status.Needs2FA && strings.TrimSpace(status.ChallengeID) != "" {
		if isUnattendedNoConfirm(req) {
			logger.Warnf("cli auth: tracker=%s decision=blocked reason=2fa_required unattended=true", trackerID)
			return status, false, fmt.Errorf("upbrr: unattended tracker auth %s requires 2FA before dupe check", trackerID)
		}
		logger.Infof("cli auth: tracker=%s decision=prompt_2fa", trackerID)
		return promptCLITrackerAuth2FAWithLogger(ctx, reader, authSvc, trackerID, status, logger)
	}

	message := cliTrackerAuthStatusMessage(status)
	if isUnattendedNoConfirm(req) {
		logger.Warnf("cli auth: tracker=%s decision=blocked reason=%s unattended=true", trackerID, cliAuthStatusMessageForLog(status))
		return status, false, fmt.Errorf("upbrr: unattended tracker auth %s not ready before dupe check: %s", trackerID, message)
	}
	fmt.Printf("Skipping %s before dupe check: tracker auth not ready (%s).\n", trackerID, message)
	return status, false, nil
}

// promptCLITrackerAuth2FAWithLogger prompts for manual 2FA and logs only the
// submitted outcome; it never logs codes or challenge identifiers.
func promptCLITrackerAuth2FAWithLogger(ctx context.Context, reader *bufio.Reader, authSvc cliTrackerAuthService, trackerID string, status api.TrackerAuthStatus, logger api.Logger) (api.TrackerAuthStatus, bool, error) {
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
			logger.Debugf("cli auth: tracker=%s decision=ready state=%s", trackerID, cliAuthLogField(status.State))
			fmt.Printf("%s auth ready.\n", trackerID)
			return status, true, nil
		}
		if !status.Needs2FA || strings.TrimSpace(status.ChallengeID) == "" {
			logger.Warnf("cli auth: tracker=%s decision=skip state=%s reason=%s", trackerID, cliAuthLogField(status.State), cliAuthStatusMessageForLog(status))
			fmt.Printf("Skipping %s before dupe check: tracker auth not ready (%s).\n", trackerID, cliTrackerAuthStatusMessage(status))
			return status, false, nil
		}
		logger.Infof("cli auth: tracker=%s decision=prompt_2fa_again state=%s", trackerID, cliAuthLogField(status.State))
	}
}

// cliAuthLogger returns a no-op logger when callers do not provide one.
func cliAuthLogger(logger api.Logger) api.Logger {
	if logger == nil {
		return api.NopLogger{}
	}
	return logger
}

// logCLITrackerAuthStatus records status metadata that is safe for logs: state,
// cookie count, encrypted-storage availability, and whether 2FA is still needed.
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

// cliAuthLogTrackerID normalizes tracker IDs for log fields and avoids empty
// tracker values when status construction fails before a tracker is known.
func cliAuthLogTrackerID(trackerID string) string {
	trackerID = strings.ToUpper(strings.TrimSpace(trackerID))
	if trackerID == "" {
		return "unknown"
	}
	return cliAuthLogField(trackerID)
}

// cliAuthStatusMessageForLog returns the same status detail shown to users,
// already passed through the auth redaction policy.
func cliAuthStatusMessageForLog(status api.TrackerAuthStatus) string {
	return cliTrackerAuthStatusMessage(status)
}

// cliAuthLogError formats an error for logs after applying redaction.
func cliAuthLogError(err error) string {
	if err == nil {
		return ""
	}
	return cliAuthLogField(err.Error())
}

// cliAuthLogField trims and redacts a single log field, returning "none" for
// empty values so structured key/value-style messages stay readable.
func cliAuthLogField(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return redaction.RedactValue(value, nil)
}

// cliTrackerAuthReady reports whether a tracker-auth status is usable without
// further user input before CLI dupe checking.
func cliTrackerAuthReady(status api.TrackerAuthStatus) bool {
	if status.Needs2FA {
		return false
	}
	switch strings.TrimSpace(status.State) {
	case trackerauth.StateConfigured, trackerauth.StateHasCookies:
		return true
	default:
		return false
	}
}

// cliTrackerAuthStatusMessage renders the tracker auth status display contract:
// Message is the stable summary and LastError is redacted detail displayed when
// distinct, matching GUI/web auth cards.
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

func matchedPreviewTrackers(preview api.MetadataPreview) []string {
	if len(preview.TrackerData) == 0 {
		return nil
	}
	matched := make([]string, 0, len(preview.TrackerData))
	for _, record := range preview.TrackerData {
		if !record.Matched {
			continue
		}
		name := strings.ToUpper(strings.TrimSpace(record.Tracker))
		if name != "" {
			matched = append(matched, name)
		}
	}
	return matched
}

func unselectedTrackers(available []string, selected []string) []string {
	if len(available) == 0 || len(selected) == 0 {
		return nil
	}
	selectedSet := make(map[string]struct{}, len(selected))
	for _, tracker := range selected {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name != "" {
			selectedSet[name] = struct{}{}
		}
	}
	removed := make([]string, 0)
	for _, tracker := range available {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if _, ok := selectedSet[name]; !ok {
			removed = append(removed, name)
		}
	}
	return removed
}

func appendTrackerRemovals(existing []string, extra ...string) []string {
	if len(extra) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(extra))
	merged := make([]string, 0, len(existing)+len(extra))
	for _, tracker := range existing {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, name)
	}
	for _, tracker := range extra {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		merged = append(merged, name)
	}
	return merged
}

func runCLIDupeCheck(ctx context.Context, coreSvc api.Core, req api.Request) (api.DupeCheckSummary, error) {
	if req.SkipDupeCheck {
		results := make([]api.DupeCheckResult, 0, len(req.Trackers))
		for _, tracker := range req.Trackers {
			name := strings.ToUpper(strings.TrimSpace(tracker))
			if name == "" {
				continue
			}
			results = append(results, api.DupeCheckResult{
				Tracker:    name,
				Skipped:    true,
				Status:     "skipped",
				SkipReason: "dupe check skipped",
			})
		}
		return api.DupeCheckSummary{Results: results}, nil
	}
	summary, err := coreSvc.CheckDupes(ctx, req)
	if err != nil {
		return api.DupeCheckSummary{}, fmt.Errorf("upbrr: %w", err)
	}
	return summary, nil
}

func promptTrackerDupeReview(reader *bufio.Reader, summary api.DupeCheckSummary, req api.Request, trackers []string, namePreview map[string]api.TrackerDryRunEntry) ([]string, []string, []string, error) {
	resultByTracker := mapDupeResultsByTracker(summary)
	approved := make([]string, 0, len(trackers))
	ignoreDupesFor := make([]string, 0)
	ruleOverrides := make([]string, 0)
	if isUnattendedNoConfirm(req) {
		approved = append(approved, printUnattendedDupeReviewSummary(resultByTracker, trackers)...)
		return approved, ignoreDupesFor, ruleOverrides, nil
	}

	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}

		result, hasResult := resultByTracker[name]
		if hasResult && dupeResultSkipsPrompt(result) {
			continue
		}

		fmt.Printf("\n[%s]\n", name)
		if hasResult {
			printDupeResult(result)
		} else {
			fmt.Println("Dupe check status: not found")
		}

		blocked := dupeResultNeedsConfirmation(result, hasResult)
		prompt := buildTrackerUploadPrompt(name, false, namePreview[name])
		if blocked {
			prompt = buildTrackerUploadPrompt(name, true, namePreview[name])
		}
		allow, err := promptYesNo(reader, prompt, false)
		if err != nil {
			return nil, nil, nil, err
		}
		if !allow {
			continue
		}
		approved = append(approved, name)
		if blocked {
			ignoreDupesFor = append(ignoreDupesFor, name)
			if result.Skipped || strings.Contains(strings.ToLower(result.SkipReason), "rule") || strings.Contains(strings.ToLower(result.Error), "rule") {
				ruleOverrides = append(ruleOverrides, name)
			}
		}
	}
	return approved, ignoreDupesFor, ruleOverrides, nil
}

type unattendedDupeReviewBlock struct {
	tracker string
	reason  string
}

// printUnattendedDupeReviewSummary emits the compact no-prompt dupe summary and
// returns only trackers that passed checks without requiring user confirmation.
func printUnattendedDupeReviewSummary(resultByTracker map[string]api.DupeCheckResult, trackers []string) []string {
	approved := make([]string, 0, len(trackers))
	dupeBlocked := make([]unattendedDupeReviewBlock, 0)
	skipped := make([]unattendedDupeReviewBlock, 0)

	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		result, hasResult := resultByTracker[name]
		if hasResult && dupeResultSkipsPrompt(result) {
			continue
		}
		if !dupeResultNeedsConfirmation(result, hasResult) {
			approved = append(approved, name)
			continue
		}
		block := unattendedDupeReviewBlock{
			tracker: name,
			reason:  dupeReviewBlockReason(result),
		}
		if result.HasDupes {
			dupeBlocked = append(dupeBlocked, block)
			continue
		}
		skipped = append(skipped, block)
	}

	fmt.Println()
	fmt.Println("Dupe check summary:")
	if len(skipped) > 0 {
		for _, line := range unattendedDupeSkipReasonLines(skipped) {
			fmt.Println(line)
		}
	}
	if len(dupeBlocked) > 0 {
		fmt.Printf("Found potential dupes on: %s\n", strings.Join(unattendedDupeBlockTrackers(dupeBlocked), ", "))
	}
	if len(approved) > 0 {
		fmt.Printf("Trackers passed all checks: %s\n", strings.Join(approved, ", "))
	}
	return approved
}

// debugDryRunApprovedTrackers keeps duplicate-hit trackers eligible for
// debug/dry-run processing while preserving rule failures as terminal skips.
func debugDryRunApprovedTrackers(resultByTracker map[string]api.DupeCheckResult, trackers []string) []string {
	approved := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		name := strings.ToUpper(strings.TrimSpace(tracker))
		if name == "" {
			continue
		}
		if result, ok := resultByTracker[name]; ok && isRuleSkippedDupeResult(result) {
			continue
		}
		approved = append(approved, name)
	}
	return approved
}

// isRuleSkippedDupeResult reports whether a skipped dupe result came from
// tracker rule validation. These skips remain terminal even in debug/dry-run.
func isRuleSkippedDupeResult(result api.DupeCheckResult) bool {
	if !result.Skipped {
		return false
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(result.SkipReason)), "rule check failed")
}

// dupeReviewBlockReason returns the operator-facing reason for a blocked dupe
// review result, preferring structured skip/error fields over display notes.
func dupeReviewBlockReason(result api.DupeCheckResult) string {
	if reason := strings.TrimSpace(result.SkipReason); reason != "" {
		return reason
	}
	if reason := strings.TrimSpace(result.Error); reason != "" {
		return reason
	}
	if len(result.Notes) > 0 {
		for _, note := range result.Notes {
			note = strings.TrimSpace(note)
			if note != "" {
				return strings.TrimPrefix(note, "skip: ")
			}
		}
	}
	return "skipped"
}

// unattendedDupeSkipReasonLines groups skipped tracker names by exact reason so
// unattended/debug output distinguishes rule skips from config or handler skips.
func unattendedDupeSkipReasonLines(blocks []unattendedDupeReviewBlock) []string {
	grouped := make(map[string][]string)
	order := make([]string, 0)
	for _, block := range blocks {
		reason := strings.TrimSpace(block.reason)
		if reason == "" {
			reason = "skipped"
		}
		if _, ok := grouped[reason]; !ok {
			order = append(order, reason)
		}
		grouped[reason] = append(grouped[reason], block.tracker)
	}

	lines := make([]string, 0, len(order))
	for _, reason := range order {
		lines = append(lines, fmt.Sprintf("Skipped: %s (%s)", strings.Join(grouped[reason], ", "), reason))
	}
	return lines
}

// unattendedDupeBlockTrackers extracts tracker codes for grouped unattended
// output; detailed rule and dupe lines stay out of no-prompt summaries.
func unattendedDupeBlockTrackers(blocks []unattendedDupeReviewBlock) []string {
	trackers := make([]string, 0, len(blocks))
	for _, block := range blocks {
		trackers = append(trackers, block.tracker)
	}
	return trackers
}

func buildTrackerUploadPrompt(tracker string, blocked bool, dryRun api.TrackerDryRunEntry) string {
	action := "Upload to " + tracker
	if blocked {
		action += " anyway"
	}
	if change := trackerReleaseNameChangeLine(dryRun); change != "" {
		return fmt.Sprintf("%s %s\n%s? [y/N]: ", tracker, change, action)
	}
	return action + "? [y/N]: "
}

func mapDupeResultsByTracker(summary api.DupeCheckSummary) map[string]api.DupeCheckResult {
	mapped := make(map[string]api.DupeCheckResult, len(summary.Results))
	for _, result := range summary.Results {
		trackers := splitCSV(strings.ReplaceAll(result.Tracker, ", ", ","))
		if len(trackers) == 0 {
			trackers = []string{result.Tracker}
		}
		for _, tracker := range trackers {
			name := strings.ToUpper(strings.TrimSpace(tracker))
			if name == "" {
				continue
			}
			copyResult := result
			copyResult.Tracker = name
			mapped[name] = copyResult
		}
	}
	return mapped
}

func dupeResultNeedsConfirmation(result api.DupeCheckResult, hasResult bool) bool {
	if !hasResult {
		return false
	}
	if isUserRequestedDupeSkipResult(result) {
		return false
	}
	if result.HasDupes || result.Skipped {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(result.Status), "failed") {
		return true
	}
	return strings.TrimSpace(result.Error) != ""
}

func dupeResultSkipsPrompt(result api.DupeCheckResult) bool {
	return isPathedTorrentDupeResult(result)
}

func isPathedTorrentDupeResult(result api.DupeCheckResult) bool {
	for _, note := range result.Notes {
		if strings.EqualFold(strings.TrimSpace(note), "pathed torrent match found; skipping dupe search") {
			return true
		}
	}
	return false
}

func isUserRequestedDupeSkipResult(result api.DupeCheckResult) bool {
	return result.Skipped && strings.EqualFold(strings.TrimSpace(result.SkipReason), "dupe check skipped")
}

func runCLIScreenshotHandling(ctx context.Context, coreSvc api.Core, req api.Request) error {
	plan, err := coreSvc.FetchScreenshotPlan(ctx, req)
	if err != nil {
		return fmt.Errorf("upbrr: screenshot plan: %w", err)
	}
	if plan.RequiresManualFrames {
		return errors.New("upbrr: screenshot handling requires manual frames; use --manual_frames")
	}

	finalImages := mergeScreenshotImages(nil, plan.FinalSelections)
	if len(plan.SuggestedSelections) == 0 {
		if len(finalImages) > 0 {
			return nil
		}
		existing := mergeScreenshotImages(nil, plan.ExistingScreenshots)
		if len(existing) == 0 {
			return nil
		}
		if err := coreSvc.SaveFinalScreenshotSelections(ctx, req, existing); err != nil {
			return fmt.Errorf("upbrr: save screenshot selections: %w", err)
		}
		return nil
	}

	result, err := coreSvc.GenerateScreenshots(ctx, req, plan.SuggestedSelections, api.ScreenshotPurposeFinal)
	if err != nil {
		return fmt.Errorf("upbrr: generate screenshots: %w", err)
	}
	if len(result.Errors) > 0 {
		return fmt.Errorf("upbrr: generate screenshots: %s", formatScreenshotErrors(result.Errors))
	}

	finalImages = mergeScreenshotImages(finalImages, plan.ExistingScreenshots)
	finalImages = mergeScreenshotImages(finalImages, result.Images)
	if len(finalImages) == 0 {
		return nil
	}
	if err := coreSvc.SaveFinalScreenshotSelections(ctx, req, finalImages); err != nil {
		return fmt.Errorf("upbrr: save screenshot selections: %w", err)
	}
	fmt.Printf("Screenshots ready: %d\n", len(finalImages))
	return nil
}

func mergeScreenshotImages(base []api.ScreenshotImage, extra []api.ScreenshotImage) []api.ScreenshotImage {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	merged := make([]api.ScreenshotImage, 0, len(base)+len(extra))
	for _, image := range base {
		key := screenshotImageKey(image)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, image)
	}
	for _, image := range extra {
		key := screenshotImageKey(image)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, image)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Index != merged[j].Index {
			return merged[i].Index < merged[j].Index
		}
		return merged[i].Path < merged[j].Path
	})
	return merged
}

func screenshotImageKey(image api.ScreenshotImage) string {
	if value := strings.TrimSpace(image.Path); value != "" {
		return value
	}
	if value := strings.TrimSpace(image.ImgURL); value != "" {
		return value
	}
	if value := strings.TrimSpace(image.RawURL); value != "" {
		return value
	}
	return strings.TrimSpace(image.WebURL)
}

func formatScreenshotErrors(errorsList []api.ScreenshotError) string {
	parts := make([]string, 0, len(errorsList))
	for _, item := range errorsList {
		message := strings.TrimSpace(item.Message)
		if message == "" {
			message = "capture failed"
		}
		if item.Index > 0 {
			parts = append(parts, fmt.Sprintf("screen %d: %s", item.Index, message))
			continue
		}
		parts = append(parts, message)
	}
	return strings.Join(parts, "; ")
}

func runSiteCheckCLIPath(ctx context.Context, coreSvc api.Core, opts cliOptions, visited map[string]bool, sourcePath string, screens int) error {
	req, err := buildCLIRequest(opts, visited, []string{sourcePath}, screens)
	if err != nil {
		return err
	}

	preview, err := coreSvc.FetchMetadataPreview(ctx, req)
	if err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}
	req.Paths = []string{resolvedCLIMetadataSourcePath(sourcePath, preview)}
	review, err := coreSvc.BuildUploadReview(ctx, req)
	if err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}
	if opts.Debug {
		printDebugUploadReview(review)
	}

	fmt.Printf("\n[Site Check] %s\n", formatPathLabel(sourcePath))
	for _, tracker := range review.Trackers {
		fmt.Printf("\n[%s]\n", tracker.Tracker)
		if tracker.Banned && !tracker.DryRun.Banned {
			fmt.Printf("Banned group: %s\n", tracker.BannedReason)
		}
		if len(tracker.RuleFailures) > 0 {
			fmt.Println("Rule failures:")
			for _, failure := range tracker.RuleFailures {
				fmt.Printf("- %s: %s\n", failure.Rule, failure.Reason)
			}
		}
		if !req.SkipDupeCheck && tracker.DupeCheck.HasDupes {
			printDupeResult(tracker.DupeCheck)
		}
		printDryRunSummary(tracker.DryRun)
	}

	return nil
}

// promptTrackerQuestionnaires collects tracker questionnaire answers. Rule-failed
// and banned trackers are skipped because they are not upload/dry-run candidates
// in any mode.
func promptTrackerQuestionnaires(reader *bufio.Reader, review api.UploadReview, opts cliOptions) (map[string]map[string]string, bool, error) {
	answers := make(map[string]map[string]string)
	changed := false
	for _, tracker := range review.Trackers {
		if tracker.Banned || len(tracker.RuleFailures) > 0 || tracker.Questionnaire == nil || len(tracker.Questionnaire.Fields) == 0 {
			continue
		}
		trackerKey := strings.ToUpper(strings.TrimSpace(tracker.Tracker))
		if trackerKey == "" {
			continue
		}
		values := make(map[string]string)
		fmt.Printf("\n[%s Questionnaire]\n", tracker.Tracker)
		for _, field := range tracker.Questionnaire.Fields {
			defaultValue := strings.TrimSpace(field.Value)
			if opts.Unattended && !opts.UnattendedConfirm {
				if field.Required && defaultValue == "" {
					if opts.Debug {
						fmt.Printf("Debug mode: %s questionnaire value missing for %s; continuing without prompt.\n", questionnaireFieldLabel(field), tracker.Tracker)
						values[field.Key] = ""
						continue
					}
					return nil, false, fmt.Errorf("upbrr: unattended upload requires %s questionnaire value for %s", questionnaireFieldLabel(field), tracker.Tracker)
				}
				values[field.Key] = defaultValue
				continue
			}
			for {
				prompt := buildQuestionnairePrompt(field)
				value, err := promptLine(reader, prompt)
				if err != nil {
					return nil, false, err
				}
				if strings.TrimSpace(value) == "" {
					value = defaultValue
				}
				value = strings.TrimSpace(value)
				if field.Required && value == "" {
					fmt.Printf("%s is required.\n", questionnaireFieldLabel(field))
					continue
				}
				values[field.Key] = value
				if value != defaultValue {
					changed = true
				}
				break
			}
		}
		answers[trackerKey] = values
	}
	if len(answers) == 0 {
		return nil, false, nil
	}
	return answers, changed, nil
}

func questionnaireFieldLabel(field api.TrackerQuestionnaireField) string {
	label := strings.TrimSpace(field.Label)
	if label != "" {
		return label
	}
	return strings.TrimSpace(field.Key)
}

func runDoubleDupeCheck(ctx context.Context, reader *bufio.Reader, coreSvc api.Core, req api.Request, trackers []string) ([]string, error) {
	recheckReq := req
	recheckReq.Trackers = trackers
	summary, err := coreSvc.CheckDupes(ctx, recheckReq)
	if err != nil {
		return nil, fmt.Errorf("upbrr: %w", err)
	}

	resultByTracker := make(map[string]api.DupeCheckResult, len(summary.Results))
	for _, result := range summary.Results {
		for _, tracker := range splitCSV(strings.ReplaceAll(result.Tracker, ", ", ",")) {
			copyResult := result
			copyResult.Tracker = tracker
			resultByTracker[strings.ToUpper(tracker)] = copyResult
		}
	}

	filtered := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		result, ok := resultByTracker[strings.ToUpper(tracker)]
		if !ok || !result.HasDupes {
			filtered = append(filtered, tracker)
			continue
		}
		fmt.Printf("\nDouble dupe check flagged %s:\n", tracker)
		printDupeResult(result)
		if req.Options.Debug {
			fmt.Printf("Debug mode: keeping %s despite second dupe check.\n", tracker)
			filtered = append(filtered, tracker)
			continue
		}
		if req.SkipDupeAsActual || isUnattendedNoConfirm(req) {
			fmt.Printf("Skipping %s due to second dupe check.\n", tracker)
			continue
		}
		upload, err := promptYesNo(reader, fmt.Sprintf("Upload to %s anyway after second dupe check? [y/N]: ", tracker), false)
		if err != nil {
			return nil, err
		}
		if upload {
			filtered = append(filtered, tracker)
		}
	}
	return filtered, nil
}

func buildQuestionnairePrompt(field api.TrackerQuestionnaireField) string {
	label := questionnaireFieldLabel(field)
	parts := []string{label}
	if field.Help != "" {
		parts = append(parts, field.Help)
	}
	if strings.TrimSpace(field.Value) != "" {
		parts = append(parts, "default: "+strings.TrimSpace(field.Value))
	}
	if field.Required {
		parts = append(parts, "required")
	}
	return strings.Join(parts, " | ") + ": "
}

func isUnattendedNoConfirm(req api.Request) bool {
	return req.Options.InteractionMode == api.InteractionModeUnattended
}

// printMetadataPreview writes the pre-upload confirmation details shown by the
// CLI, including a debug-mode notice when tracker uploads will not run.
func printMetadataPreview(preview api.MetadataPreview, debug bool) {
	fmt.Println()
	fmt.Println("Release details")
	if debug {
		fmt.Println("Debug mode: no actual tracker uploads will be processed.")
	}
	fmt.Printf("Source: %s\n", formatPathLabel(preview.SourcePath))
	fmt.Printf("Upload name: %s\n", preview.ReleaseName)
	if external := primaryMetadataPreview(preview); external != nil {
		printMetadataDatabaseInfo(*external, preview)
	}
	if preview.TrackerName != "" {
		fmt.Printf("Tracker data from: %s\n", preview.TrackerName)
	}
	printMetadataExternalIDs(preview)
	if len(preview.ExternalIDCandidates.TMDB) > 0 || len(preview.ExternalIDCandidates.IMDB) > 0 {
		fmt.Println("Candidate IDs available; use override args if needed.")
	}
	if len(preview.Warnings) > 0 {
		fmt.Println("Warnings:")
		for _, warning := range preview.Warnings {
			fmt.Printf("- %s\n", warning)
		}
	}
}

func printMetadataDatabaseInfo(external api.ExternalPreview, preview api.MetadataPreview) {
	fmt.Println()
	fmt.Println("Database info")
	title := strings.TrimSpace(external.Title)
	if title == "" {
		title = strings.TrimSpace(preview.ReleaseName)
	}
	if title != "" && external.Year != 0 {
		fmt.Printf("Title: %s (%d)\n", title, external.Year)
	} else if title != "" {
		fmt.Printf("Title: %s\n", title)
	}
	if overview := summarizeMetadataText(external.Overview, 260); overview != "" {
		fmt.Printf("Overview: %s\n", overview)
	}
	if genres := strings.TrimSpace(external.Genres); genres != "" {
		fmt.Printf("Genres: %s\n", genres)
	}
	if category := metadataPreviewCategory(external, preview); category != "" {
		fmt.Printf("Category: %s\n", category)
	}
	if date := metadataPreviewDate(external); date != "" {
		fmt.Printf("Date: %s\n", date)
	}
	if runtime := metadataPreviewRuntime(external); runtime != "" {
		fmt.Printf("Runtime: %s\n", runtime)
	}
	if external.Rating != 0 {
		if external.RatingCount != 0 {
			fmt.Printf("Rating: %.1f (%d votes)\n", external.Rating, external.RatingCount)
		} else {
			fmt.Printf("Rating: %.1f\n", external.Rating)
		}
	}
}

func printMetadataExternalIDs(preview api.MetadataPreview) {
	printedHeader := false
	printHeader := func() {
		if printedHeader {
			return
		}
		fmt.Println()
		fmt.Println("External IDs")
		printedHeader = true
	}
	if preview.ExternalIDs.TMDBID != 0 {
		printHeader()
		fmt.Printf("TMDB: %s\n", tmdbURL(preview.ExternalIDs.TMDBID, metadataPreviewTMDBCategory(preview)))
	}
	if preview.ExternalIDs.IMDBID != 0 {
		printHeader()
		fmt.Printf("IMDb: https://www.imdb.com/title/tt%07d\n", preview.ExternalIDs.IMDBID)
	}
	if preview.ExternalIDs.TVDBID != 0 {
		printHeader()
		fmt.Printf("TVDB: https://www.thetvdb.com/?id=%d&tab=series\n", preview.ExternalIDs.TVDBID)
	}
	if preview.ExternalIDs.TVmazeID != 0 {
		printHeader()
		fmt.Printf("TVmaze: https://www.tvmaze.com/shows/%d\n", preview.ExternalIDs.TVmazeID)
	}
}

func primaryMetadataPreview(preview api.MetadataPreview) *api.ExternalPreview {
	preferred := []string{"tmdb", "tvdb", "imdb", "tvmaze"}
	for _, provider := range preferred {
		for i := range preview.ExternalPreview {
			external := &preview.ExternalPreview[i]
			if strings.EqualFold(strings.TrimSpace(external.Provider), provider) && metadataPreviewHasDetails(*external) {
				return external
			}
		}
	}
	for i := range preview.ExternalPreview {
		if metadataPreviewHasDetails(preview.ExternalPreview[i]) {
			return &preview.ExternalPreview[i]
		}
	}
	return nil
}

func metadataPreviewHasDetails(external api.ExternalPreview) bool {
	return strings.TrimSpace(external.Title) != "" ||
		strings.TrimSpace(external.Overview) != "" ||
		strings.TrimSpace(external.Genres) != "" ||
		external.Year != 0 ||
		external.Rating != 0 ||
		external.Runtime != 0 ||
		external.RuntimeMinutes != 0
}

func metadataPreviewCategory(external api.ExternalPreview, preview api.MetadataPreview) string {
	if category := strings.TrimSpace(external.Category); category != "" {
		return strings.ToUpper(category)
	}
	if category := strings.TrimSpace(preview.ExternalIDs.Category); category != "" {
		return strings.ToUpper(category)
	}
	if tmdbType := strings.TrimSpace(external.TMDBType); tmdbType != "" {
		return strings.ToUpper(tmdbType)
	}
	if imdbType := strings.TrimSpace(external.IMDBType); imdbType != "" {
		return strings.ToUpper(imdbType)
	}
	return ""
}

func metadataPreviewDate(external api.ExternalPreview) string {
	for _, value := range []string{external.ReleaseDate, external.FirstAirDate, external.Premiered} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func metadataPreviewRuntime(external api.ExternalPreview) string {
	runtime := external.Runtime
	if runtime == 0 {
		runtime = external.RuntimeMinutes
	}
	if runtime == 0 {
		return ""
	}
	return fmt.Sprintf("%d min", runtime)
}

func metadataPreviewTMDBCategory(preview api.MetadataPreview) string {
	for _, external := range preview.ExternalPreview {
		if !strings.EqualFold(strings.TrimSpace(external.Provider), "tmdb") {
			continue
		}
		if category := strings.TrimSpace(external.Category); category != "" {
			return category
		}
		if tmdbType := strings.TrimSpace(external.TMDBType); tmdbType != "" {
			return tmdbType
		}
	}
	if category := strings.TrimSpace(preview.ExternalIDs.Category); category != "" {
		return category
	}
	return "movie"
}

func tmdbURL(id int, category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "tv", "series", "show":
		return fmt.Sprintf("https://www.themoviedb.org/tv/%d", id)
	default:
		return fmt.Sprintf("https://www.themoviedb.org/movie/%d", id)
	}
}

func summarizeMetadataText(value string, limit int) string {
	compact := strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if compact == "" || limit <= 0 {
		return compact
	}
	runes := []rune(compact)
	if len(runes) <= limit {
		return compact
	}
	return string(runes[:limit]) + "..."
}

func printDupeResult(result api.DupeCheckResult) {
	fmt.Printf("Dupe check status: %s\n", result.Status)
	for _, note := range result.Notes {
		fmt.Printf("- %s\n", note)
	}
	entries := result.Filtered
	if len(entries) == 0 {
		entries = result.Raw
	}
	for _, entry := range entries {
		if entry.Name == "" {
			continue
		}
		line := entry.Name
		if entry.Link != "" {
			line += " - " + entry.Link
		}
		fmt.Printf("- %s\n", line)
	}
}

func printDryRunSummary(entry api.TrackerDryRunEntry) {
	writeDryRunSummary(os.Stdout, entry)
}

// writeDryRunSummary writes the same safe dry-run summary as printDryRunSummary
// to an arbitrary writer so debug payload sections can be grouped by content.
func writeDryRunSummary(w io.Writer, entry api.TrackerDryRunEntry) {
	if strings.TrimSpace(entry.Tracker) == "" {
		return
	}
	fmt.Fprintf(w, "Dry run: %s", entry.Status)
	if entry.Message != "" {
		fmt.Fprintf(w, " (%s)", entry.Message)
	}
	fmt.Fprintln(w)
	if entry.Banned {
		reason := strings.TrimSpace(entry.BannedReason)
		if reason == "" {
			reason = "matched"
		}
		fmt.Fprintf(w, "Banned group: %s\n", reason)
	}
	if bannedCheckError := strings.TrimSpace(entry.BannedCheckError); bannedCheckError != "" {
		fmt.Fprintf(w, "Banned group check: %s\n", bannedCheckError)
	}
	if change := trackerReleaseNameChangeLine(entry); change != "" {
		fmt.Fprintf(w, "Tracker %s\n", change)
	} else if entry.ReleaseName != "" {
		fmt.Fprintf(w, "Tracker release name: %s\n", entry.ReleaseName)
	}
	if imageMessage := strings.TrimSpace(entry.ImageHost.Message); imageMessage != "" && (entry.ImageHost.Reuploaded || strings.EqualFold(entry.ImageHost.Status, "warning")) {
		fmt.Fprintf(w, "Images: %s\n", imageMessage)
	}
	for _, warning := range entry.ImageHost.Warnings {
		host := strings.TrimSpace(warning.Host)
		warningMessage := strings.TrimSpace(warning.Message)
		if host == "" && warningMessage == "" {
			continue
		}
		if host == "" {
			fmt.Fprintf(w, "Image host warning: %s\n", warningMessage)
			continue
		}
		if warningMessage == "" {
			fmt.Fprintf(w, "Image host warning: %s failed\n", host)
			continue
		}
		fmt.Fprintf(w, "Image host warning: %s failed: %s\n", host, warningMessage)
	}
}

func printDebugUploadReview(review api.UploadReview) {
	fmt.Printf("\n[Debug Dry Run] %s\n", formatPathLabel(review.SourcePath))
	for _, group := range groupDebugPayloads(review.Trackers) {
		fmt.Printf("\n[%s Debug Payload]\n", strings.Join(group.trackers, ", "))
		fmt.Print(group.body)
	}
}

// debugPayloadGroup represents one rendered debug payload body and the trackers
// that share it.
type debugPayloadGroup struct {
	trackers []string
	body     string
}

// groupDebugPayloads groups trackers by rendered debug payload text. Release
// name changes are part of that text, so tracker-specific naming changes remain
// in separate sections.
func groupDebugPayloads(trackers []api.TrackerReview) []debugPayloadGroup {
	groups := make([]debugPayloadGroup, 0, len(trackers))
	groupByBody := make(map[string]int, len(trackers))
	for _, tracker := range trackers {
		body := renderDebugPayloadBody(tracker)
		if index, ok := groupByBody[body]; ok {
			groups[index].trackers = append(groups[index].trackers, debugPayloadTrackerLabel(tracker))
			continue
		}
		groupByBody[body] = len(groups)
		groups = append(groups, debugPayloadGroup{
			trackers: []string{debugPayloadTrackerLabel(tracker)},
			body:     body,
		})
	}
	return groups
}

// renderDebugPayloadBody returns the exact body printed below a debug payload
// header, excluding the tracker list header used for grouping.
func renderDebugPayloadBody(tracker api.TrackerReview) string {
	var builder strings.Builder
	if tracker.Banned && !tracker.DryRun.Banned {
		fmt.Fprintf(&builder, "Banned group: %s\n", tracker.BannedReason)
	}
	writeDryRunSummary(&builder, tracker.DryRun)
	writeDryRunDetails(&builder, tracker.DryRun)
	return builder.String()
}

// debugPayloadTrackerLabel normalizes the tracker label used in grouped debug
// payload headers while preserving a visible placeholder for malformed entries.
func debugPayloadTrackerLabel(tracker api.TrackerReview) string {
	label := strings.TrimSpace(tracker.Tracker)
	if label == "" {
		return "(unknown)"
	}
	return label
}

func printDryRunUploadReview(review api.UploadReview, req api.Request) {
	fmt.Printf("\n[Dry Run] %s\n", formatPathLabel(review.SourcePath))
	for _, tracker := range review.Trackers {
		fmt.Printf("\n[%s]\n", tracker.Tracker)
		if tracker.Banned && !tracker.DryRun.Banned {
			fmt.Printf("Banned group: %s\n", tracker.BannedReason)
		}
		if len(tracker.RuleFailures) > 0 {
			fmt.Println("Rule failures:")
			for _, failure := range tracker.RuleFailures {
				fmt.Printf("- %s: %s\n", failure.Rule, failure.Reason)
			}
		}
		if !req.SkipDupeCheck && tracker.DupeCheck.HasDupes {
			printDupeResult(tracker.DupeCheck)
		}
		printDryRunSummary(tracker.DryRun)
	}
}

func printDryRunDetails(entry api.TrackerDryRunEntry) {
	writeDryRunDetails(os.Stdout, entry)
}

// writeDryRunDetails writes redacted dry-run files, payload fields, and
// description summaries to an arbitrary writer.
func writeDryRunDetails(w io.Writer, entry api.TrackerDryRunEntry) {
	if len(entry.DebugSections) > 0 {
		writeDryRunDebugSections(w, entry.DebugSections)
	} else {
		if len(entry.Files) > 0 {
			writeDryRunFiles(w, entry.Files)
		}
		if len(entry.Payload) > 0 {
			writeDryRunPayload(w, entry.Payload)
		}
	}
	if message := strings.TrimSpace(entry.Description); message != "" && !payloadIncludesDescription(entry.Payload) {
		fmt.Fprintf(w, "Description: %s\n", summarizeDryRunBody(message))
	}
}

// writeDryRunDebugSections renders staged tracker diagnostics instead of the
// top-level dry-run payload so debug output can show intermediate request data
// without duplicating the final payload.
func writeDryRunDebugSections(w io.Writer, sections []api.TrackerDryRunDebugSection) {
	if len(sections) == 0 {
		return
	}
	fmt.Fprintln(w, "Debug sections:")
	for _, section := range sections {
		title := strings.TrimSpace(section.Title)
		if title == "" {
			title = "debug section"
		}
		fmt.Fprintf(w, "%s:\n", title)
		if endpoint := strings.TrimSpace(section.Endpoint); endpoint != "" {
			fmt.Fprintf(w, "Endpoint: %s\n", formatDryRunPayloadValue("endpoint", endpoint))
		}
		if len(section.Files) > 0 {
			writeDryRunFiles(w, section.Files)
		}
		if len(section.Payload) > 0 {
			writeDryRunPayload(w, section.Payload)
		}
	}
}

func writeDryRunFiles(w io.Writer, files []api.TrackerDryRunFile) {
	fmt.Fprintln(w, "Files:")
	for _, file := range files {
		status := "missing"
		if file.Present {
			status = "present"
		}
		fmt.Fprintf(w, "- %s [%s]: %s\n", file.Field, status, formatDryRunFilePath(file.Path))
	}
}

func writeDryRunPayload(w io.Writer, payload map[string]string) {
	fmt.Fprintln(w, "Payload:")
	keys := make([]string, 0, len(payload))
	for key := range payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "- %s: %s\n", key, formatDryRunPayloadValue(key, payload[key]))
	}
}

func formatDryRunFilePath(value string) string {
	return formatPathLabel(value)
}

// formatPathLabel keeps CLI output shareable by hiding ordinary local paths
// while preserving labels accepted by [logging.DBRelativePathLabel].
func formatPathLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "(none)"
	}
	if label, ok := logging.DBRelativePathLabel(trimmed); ok {
		return label
	}
	return "[local path]"
}

// trackerReleaseNameChangeLine returns the CLI-facing release-name change text,
// or an empty string when the tracker did not apply a different upload name.
func trackerReleaseNameChangeLine(entry api.TrackerDryRunEntry) string {
	if !entry.ReleaseNameChanged {
		return ""
	}
	originalName := strings.TrimSpace(entry.OriginalReleaseName)
	uploadName := strings.TrimSpace(entry.UploadReleaseName)
	if uploadName == "" {
		uploadName = strings.TrimSpace(entry.ReleaseName)
	}
	if originalName == "" {
		originalName = strings.TrimSpace(entry.ReleaseName)
	}
	if originalName == "" && uploadName == "" {
		return ""
	}
	if originalName == "" {
		originalName = "(unknown)"
	}
	if uploadName == "" {
		uploadName = "(unknown)"
	}
	line := fmt.Sprintf("release name changed: %s -> %s", originalName, uploadName)
	if reason := strings.TrimSpace(entry.ReleaseNameChangeReason); reason != "" {
		line += fmt.Sprintf(" (reason: %s)", reason)
	}
	return line
}

// formatDryRunPayloadValue returns a log-safe preview for a dry-run payload
// field, redacting sensitive keys before applying body summarization/truncation.
func formatDryRunPayloadValue(key string, value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if isSensitiveDryRunPayloadField(key) {
		return "[REDACTED]"
	}
	trimmed = redaction.RedactValue(trimmed, nil)
	if isDryRunBodyPayloadField(key) {
		return summarizeDryRunBody(trimmed)
	}
	compact := strings.Join(strings.Fields(trimmed), " ")
	compactRunes := []rune(compact)
	if len(compactRunes) <= dryRunPayloadPreviewLimit {
		return compact
	}
	return fmt.Sprintf("%s... [%d bytes total]", string(compactRunes[:dryRunPayloadPreviewLimit]), len(trimmed))
}

func summarizeDryRunBody(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	lines := strings.Count(trimmed, "\n") + 1
	return fmt.Sprintf("[%d bytes, %d lines omitted]", len(trimmed), lines)
}

func payloadIncludesDescription(payload map[string]string) bool {
	for key := range payload {
		if isDryRunDescriptionPayloadField(key) {
			return true
		}
	}
	return false
}

func isDryRunBodyPayloadField(key string) bool {
	switch normalizedDryRunPayloadKey(key) {
	case "description", "desc", "descr", "release_desc", "album_desc", "mediainfo", "mediainfo[]", "media_info", "bdinfo", "bd_info", "techinfo", "technical_info", "technicaldetails":
		return true
	default:
		return false
	}
}

func isDryRunDescriptionPayloadField(key string) bool {
	switch normalizedDryRunPayloadKey(key) {
	case "description", "desc", "descr", "release_desc", "album_desc":
		return true
	default:
		return false
	}
}

func normalizedDryRunPayloadKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}

// isSensitiveDryRunPayloadField reports whether a dry-run payload key should
// suppress its value entirely instead of showing a redacted preview.
func isSensitiveDryRunPayloadField(key string) bool {
	_, sensitive := sensitiveDryRunPayloadKeys[normalizedSensitiveDryRunPayloadKey(key)]
	return sensitive
}

// normalizedSensitiveDryRunPayloadKey removes separators before exact matching
// so aliases like api_key and apiKey redact without treating keywords as key.
func normalizedSensitiveDryRunPayloadKey(key string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(key)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// sensitiveDryRunPayloadKeys lists exact normalized dry-run payload field names
// whose values must be suppressed instead of printed as previews.
var sensitiveDryRunPayloadKeys = map[string]struct{}{
	"anticsrftoken":        {},
	"accesstoken":          {},
	"apikey":               {},
	"apitoken":             {},
	"auth":                 {},
	"authorization":        {},
	"authkey":              {},
	"authtoken":            {},
	"cookie":               {},
	"csrf":                 {},
	"email":                {},
	"infohash":             {},
	"key":                  {},
	"passkey":              {},
	"password":             {},
	"passwordconfirm":      {},
	"passwordconfirmation": {},
	"popcron":              {},
	"refreshtoken":         {},
	"rsskey":               {},
	"secret":               {},
	"secretkey":            {},
	"sessionkey":           {},
	"sessiontoken":         {},
	"token":                {},
	"torrentpass":          {},
	"torrentpasskey":       {},
	"uid":                  {},
	"user":                 {},
	"userid":               {},
	"username":             {},
}

func promptYesNo(reader *bufio.Reader, prompt string, defaultYes bool) (bool, error) {
	line, err := promptLine(reader, prompt)
	if err != nil {
		return false, err
	}
	trimmed := strings.ToLower(strings.TrimSpace(line))
	if trimmed == "" {
		return defaultYes, nil
	}
	return trimmed == "y" || trimmed == "yes", nil
}

func promptLine(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := reader.ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) && line != "" {
			return line, nil
		}
		return "", fmt.Errorf("read prompt line: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func splitInteractiveCLIArgs(input string) ([]string, error) {
	args := make([]string, 0, len(strings.Fields(input)))
	var current strings.Builder
	quote := rune(0)
	tokenStarted := false
	quoteBoundary := true

	for _, r := range input {
		if quote == 0 {
			switch {
			case unicode.IsSpace(r):
				if tokenStarted {
					args = append(args, current.String())
					current.Reset()
					tokenStarted = false
				}
				quoteBoundary = true
				continue
			case quoteBoundary && (r == '"' || r == '\''):
				quote = r
				tokenStarted = true
				quoteBoundary = false
				continue
			}
		} else if r == quote {
			quote = 0
			quoteBoundary = false
			continue
		}

		current.WriteRune(r)
		tokenStarted = true
		quoteBoundary = r == '='
	}

	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	if tokenStarted {
		args = append(args, current.String())
	}
	return args, nil
}

func copyVisited(input map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(input))
	maps.Copy(cloned, input)
	return cloned
}
