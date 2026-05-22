// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/autobrr/upbrr/internal/metadata/metautil"

	"github.com/autobrr/upbrr/pkg/api"
)

func runInteractiveCLIPath(ctx context.Context, coreSvc api.Core, baseArgs []string, opts cliOptions, visited map[string]bool, sourcePath string, screens int) error {
	reader := bufio.NewReader(os.Stdin)
	currentOpts := opts
	currentVisited := copyVisited(visited)

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
					return err
				}
				currentOpts.ConfirmBDMVRescan = true
				continue
			}
			return err
		}

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

		nextOpts, nextVisited, _, err := parseCLIOptions(append(baseArgs, strings.Fields(editArgs)...))
		if err != nil {
			fmt.Printf("Invalid override args: %v\n", err)
			continue
		}
		currentOpts = nextOpts
		currentVisited = nextVisited
	}

	req, err := buildCLIRequest(currentOpts, currentVisited, []string{sourcePath}, screens)
	if err != nil {
		return err
	}

	review, err := coreSvc.BuildUploadReview(ctx, req)
	if err != nil {
		return err
	}
	if currentOpts.Debug {
		printDebugUploadReview(review)
	}

	questionnaireAnswers, questionnaireChanged, err := promptTrackerQuestionnaires(reader, review, currentOpts)
	if err != nil {
		return err
	}
	if questionnaireChanged {
		req.TrackerQuestionnaireAnswers = questionnaireAnswers
		review, err = coreSvc.BuildUploadReview(ctx, req)
		if err != nil {
			return err
		}
	}

	approved, ruleOverrides, err := promptTrackerReview(reader, review, req)
	if err != nil {
		return err
	}
	if req.DoubleDupeCheck && len(approved) > 0 {
		approved, err = runDoubleDupeCheck(ctx, reader, coreSvc, req, approved)
		if err != nil {
			return err
		}
	}
	if len(approved) == 0 {
		fmt.Printf("No trackers selected for %s\n", sourcePath)
		return nil
	}

	req.Trackers = approved
	req.IgnoreTrackerRuleFailuresFor = ruleOverrides
	req.TrackerQuestionnaireAnswers = questionnaireAnswers

	_, err = coreSvc.RunUploadPrepared(ctx, req)
	return err
}

func runSiteCheckCLIPath(ctx context.Context, coreSvc api.Core, opts cliOptions, visited map[string]bool, sourcePath string, screens int) error {
	req, err := buildCLIRequest(opts, visited, []string{sourcePath}, screens)
	if err != nil {
		return err
	}

	review, err := coreSvc.BuildUploadReview(ctx, req)
	if err != nil {
		return err
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
					fmt.Printf("%s is required.\n", strings.TrimSpace(field.Label))
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

func runDoubleDupeCheck(ctx context.Context, reader *bufio.Reader, coreSvc api.Core, req api.Request, trackers []string) ([]string, error) {
	recheckReq := req
	recheckReq.Trackers = trackers
	summary, err := coreSvc.CheckDupes(ctx, recheckReq)
	if err != nil {
		return nil, err
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
	label := strings.TrimSpace(field.Label)
	if label == "" {
		label = strings.TrimSpace(field.Key)
	}
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

func promptTrackerReview(reader *bufio.Reader, review api.UploadReview, req api.Request) ([]string, []string, error) {
	approved := make([]string, 0, len(review.Trackers))
	ruleOverrides := make([]string, 0)
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
			if isUnattendedNoConfirm(req) {
				fmt.Printf("Skipping %s due to rule failures.\n", tracker.Tracker)
				continue
			}
			allow, err := promptYesNo(reader, fmt.Sprintf("Upload to %s despite rule failures? [y/N]: ", tracker.Tracker), false)
			if err != nil {
				return nil, nil, err
			}
			if !allow {
				continue
			}
			ruleOverrides = append(ruleOverrides, tracker.Tracker)
		}
		if !req.SkipDupeCheck && tracker.DupeCheck.HasDupes {
			printDupeResult(tracker.DupeCheck)
			if req.SkipDupeAsActual || isUnattendedNoConfirm(req) {
				fmt.Printf("Skipping %s due to dupes.\n", tracker.Tracker)
				continue
			}
			allow, err := promptYesNo(reader, fmt.Sprintf("Upload to %s anyway? [y/N]: ", tracker.Tracker), false)
			if err != nil {
				return nil, nil, err
			}
			if !allow {
				continue
			}
		}
		printDryRunSummary(tracker.DryRun)
		if isUnattendedNoConfirm(req) {
			approved = append(approved, tracker.Tracker)
			continue
		}
		allow, err := promptYesNo(reader, fmt.Sprintf("Upload to %s? [y/N]: ", tracker.Tracker), false)
		if err != nil {
			return nil, nil, err
		}
		if allow {
			approved = append(approved, tracker.Tracker)
		}
	}
	return approved, ruleOverrides, nil
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

func printDryRunDetails(entry api.TrackerDryRunEntry) {
	if strings.TrimSpace(entry.Endpoint) != "" {
		fmt.Printf("Endpoint: %s\n", entry.Endpoint)
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
			fmt.Printf("- %s: %s\n", key, entry.Payload[key])
		}
	}
	if message := strings.TrimSpace(entry.Description); message != "" {
		fmt.Printf("Description:\n%s\n", message)
	}
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
		if err.Error() == "EOF" && line != "" {
			return line, nil
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func copyVisited(input map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}
