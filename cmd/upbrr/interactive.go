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
	"github.com/autobrr/upbrr/internal/metadata/metautil"
	"github.com/autobrr/upbrr/internal/redaction"
	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/pkg/api"
)

const dryRunPayloadPreviewLimit = 240

func runInteractiveCLIPath(ctx context.Context, coreSvc api.Core, baseArgs []string, opts cliOptions, visited map[string]bool, sourcePath string, screens int, cfg config.Config) error {
	return runInteractiveCLIPathWithInput(ctx, coreSvc, baseArgs, opts, visited, sourcePath, screens, cfg, os.Stdin)
}

func runInteractiveCLIPathWithInput(ctx context.Context, coreSvc api.Core, baseArgs []string, opts cliOptions, visited map[string]bool, sourcePath string, screens int, cfg config.Config, stdin io.Reader) error {
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

		printMetadataPreview(preview)
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
		fmt.Printf("No trackers configured for %s\n", sourcePath)
		return nil
	}
	req.Trackers = candidateTrackers
	req.TrackersRemove = appendTrackerRemovals(req.TrackersRemove, unselectedTrackers(removalBase, candidateTrackers)...)

	dupeSummary, err := runCLIDupeCheck(ctx, coreSvc, req)
	if err != nil {
		return err
	}
	approved, ignoreDupesFor, ruleOverrides, err := promptTrackerDupeReview(reader, dupeSummary, req, candidateTrackers, nil)
	if err != nil {
		return err
	}
	if len(approved) == 0 {
		fmt.Printf("No trackers selected for %s\n", sourcePath)
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
		fmt.Printf("No trackers selected for %s\n", sourcePath)
		return nil
	}

	if !req.Options.Debug && !req.Options.DryRun {
		if err := runCLIScreenshotHandling(ctx, coreSvc, req); err != nil {
			return err
		}
	}

	review, err := coreSvc.BuildUploadReview(ctx, req)
	if err != nil {
		return fmt.Errorf("upbrr: %w", err)
	}

	questionnaireAnswers, questionnaireChanged, err := promptTrackerQuestionnaires(reader, review, currentOpts)
	if err != nil {
		return err
	}
	if questionnaireChanged {
		req.TrackerQuestionnaireAnswers = questionnaireAnswers
		review, err = coreSvc.BuildUploadReview(ctx, req)
		if err != nil {
			return fmt.Errorf("upbrr: %w", err)
		}
	}

	req, review, err = maybeEditCLIDescriptions(ctx, coreSvc, reader, req, review, currentOpts)
	if err != nil {
		return err
	}

	req.Trackers = approved
	req.TrackerQuestionnaireAnswers = questionnaireAnswers

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
	remove = append(remove, matchedPreviewTrackers(preview)...)
	removalBase := trackers.ResolveTrackersWithDefaults(cfg, req.Trackers, remove, api.NopLogger{})
	available := removalBase
	if visited["trackers"] || req.Execution.SiteUploadTracker != "" {
		available = trackers.ResolveTrackers(cfg, req.Trackers, remove, api.NopLogger{})
	}
	return available, removalBase
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
		if isUnattendedNoConfirm(req) {
			if blocked {
				fmt.Printf("Skipping %s due to dupe/rule check result.\n", name)
				continue
			}
			approved = append(approved, name)
			continue
		}

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

func buildTrackerUploadPrompt(tracker string, blocked bool, dryRun api.TrackerDryRunEntry) string {
	action := "Upload to " + tracker
	if blocked {
		action += " anyway"
	}
	if dryRun.ReleaseNameChanged {
		uploadName := strings.TrimSpace(dryRun.UploadReleaseName)
		if uploadName == "" {
			uploadName = strings.TrimSpace(dryRun.ReleaseName)
		}
		if uploadName != "" {
			return fmt.Sprintf("%s changes name to %s\n%s? [y/N]: ", tracker, uploadName, action)
		}
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

	fmt.Printf("\n[Site Check] %s\n", sourcePath)
	for _, tracker := range review.Trackers {
		fmt.Printf("\n[%s]\n", tracker.Tracker)
		if tracker.Banned {
			fmt.Printf("Banned group: %s\n", tracker.BannedReason)
			continue
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

func promptTrackerQuestionnaires(reader *bufio.Reader, review api.UploadReview, opts cliOptions) (map[string]map[string]string, bool, error) {
	answers := make(map[string]map[string]string)
	changed := false
	for _, tracker := range review.Trackers {
		if tracker.Banned || tracker.Questionnaire == nil || len(tracker.Questionnaire.Fields) == 0 {
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

func printMetadataPreview(preview api.MetadataPreview) {
	fmt.Printf("\nSource: %s\n", preview.SourcePath)
	fmt.Printf("Release: %s\n", preview.ReleaseName)
	if preview.TrackerName != "" {
		fmt.Printf("Tracker data from: %s\n", preview.TrackerName)
	}
	if preview.ExternalIDs.TMDBID != 0 {
		fmt.Printf("TMDB: %d\n", preview.ExternalIDs.TMDBID)
	}
	if preview.ExternalIDs.IMDBID != 0 {
		fmt.Printf("IMDb: tt%07d\n", preview.ExternalIDs.IMDBID)
	}
	if preview.ExternalIDs.TVDBID != 0 {
		fmt.Printf("TVDB: %d\n", preview.ExternalIDs.TVDBID)
	}
	if preview.ExternalIDs.TVmazeID != 0 {
		fmt.Printf("TVmaze: %d\n", preview.ExternalIDs.TVmazeID)
	}
	if len(preview.Warnings) > 0 {
		fmt.Println("Warnings:")
		for _, warning := range preview.Warnings {
			fmt.Printf("- %s\n", warning)
		}
	}
	if len(preview.ExternalIDCandidates.TMDB) > 0 || len(preview.ExternalIDCandidates.IMDB) > 0 {
		fmt.Println("Candidate IDs available; use override args if needed.")
	}
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
	if strings.TrimSpace(entry.Tracker) == "" {
		return
	}
	fmt.Printf("Dry run: %s", entry.Status)
	if entry.Message != "" {
		fmt.Printf(" (%s)", entry.Message)
	}
	fmt.Println()
	if entry.ReleaseName != "" {
		fmt.Printf("Tracker release name: %s\n", entry.ReleaseName)
	}
	if len(entry.Payload) > 0 {
		keys := make([]string, 0, len(entry.Payload))
		for key := range entry.Payload {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Printf("Payload fields: %s\n", strings.Join(keys, ", "))
	}
	if imageMessage := strings.TrimSpace(entry.ImageHost.Message); imageMessage != "" && (entry.ImageHost.Reuploaded || strings.EqualFold(entry.ImageHost.Status, "warning")) {
		fmt.Printf("Images: %s\n", imageMessage)
	}
	for _, warning := range entry.ImageHost.Warnings {
		host := strings.TrimSpace(warning.Host)
		warningMessage := strings.TrimSpace(warning.Message)
		if host == "" && warningMessage == "" {
			continue
		}
		if host == "" {
			fmt.Printf("Image host warning: %s\n", warningMessage)
			continue
		}
		if warningMessage == "" {
			fmt.Printf("Image host warning: %s failed\n", host)
			continue
		}
		fmt.Printf("Image host warning: %s failed: %s\n", host, warningMessage)
	}
}

func printDebugUploadReview(review api.UploadReview) {
	fmt.Printf("\n[Debug Dry Run] %s\n", review.SourcePath)
	for _, tracker := range review.Trackers {
		fmt.Printf("\n[%s Debug Payload]\n", tracker.Tracker)
		if tracker.Banned {
			fmt.Printf("Banned group: %s\n", tracker.BannedReason)
			continue
		}
		printDryRunSummary(tracker.DryRun)
		printDryRunDetails(tracker.DryRun)
	}
}

func printDryRunUploadReview(review api.UploadReview, req api.Request) {
	fmt.Printf("\n[Dry Run] %s\n", review.SourcePath)
	for _, tracker := range review.Trackers {
		fmt.Printf("\n[%s]\n", tracker.Tracker)
		if tracker.Banned {
			fmt.Printf("Banned group: %s\n", tracker.BannedReason)
			continue
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
	if strings.TrimSpace(entry.Endpoint) != "" {
		fmt.Printf("Endpoint: %s\n", safeDryRunEndpoint(entry.Endpoint))
	}
	if len(entry.Files) > 0 {
		fmt.Println("Files:")
		for _, file := range entry.Files {
			status := "missing"
			if file.Present {
				status = "present"
			}
			fmt.Printf("- %s [%s]: %s\n", file.Field, status, metautil.FirstNonEmptyTrimmed(strings.TrimSpace(file.Path), "(none)"))
		}
	}
	if len(entry.Payload) > 0 {
		fmt.Println("Payload:")
		keys := make([]string, 0, len(entry.Payload))
		for key := range entry.Payload {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Printf("- %s: %s\n", key, formatDryRunPayloadValue(key, entry.Payload[key]))
		}
	}
	if message := strings.TrimSpace(entry.Description); message != "" && !payloadIncludesDescription(entry.Payload) {
		fmt.Printf("Description: %s\n", summarizeDryRunBody(message))
	}
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

// safeDryRunEndpoint returns a dry-run endpoint suitable for CLI output,
// preserving the URL shape while redacting credential-like path/query values.
func safeDryRunEndpoint(value string) string {
	return redaction.RedactValue(strings.TrimSpace(value), nil)
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
	normalized := normalizedDryRunPayloadKey(key)
	for sensitive := range redaction.DefaultSensitiveKeys {
		if strings.Contains(normalized, sensitive) {
			return true
		}
	}
	return false
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
