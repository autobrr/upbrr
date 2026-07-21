// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

var editCLIDescriptionFile = editDescriptionFile

// cliDescriptionEditorCore owns description preview and persisted overrides.
type cliDescriptionEditorCore interface {
	FetchDescriptionBuilderPreview(context.Context, api.Request) (api.DescriptionBuilderPreview, error)
	SaveDescriptionOverride(context.Context, api.Request, string) (api.DescriptionBuilderGroup, error)
}

func maybeEditCLIDescriptions(
	ctx context.Context,
	coreSvc cliDescriptionEditorCore,
	reader *bufio.Reader,
	req api.Request,
	review api.UploadReview,
	opts cliOptions,
) (api.Request, error) {
	if req.Options.OnlyID || opts.OnlyID {
		return req, nil
	}
	if opts.Unattended && !opts.UnattendedConfirm {
		return req, nil
	}

	preview, err := coreSvc.FetchDescriptionBuilderPreview(ctx, req)
	if err != nil {
		return req, fmt.Errorf("upbrr: description builder: %w", err)
	}
	groups := filterEditableDescriptionGroups(preview.Groups, review)
	if len(groups) == 0 {
		fmt.Println("No generated descriptions available to edit.")
		return req, nil
	}
	printCLIDescriptionPreview(groups)

	changed := false
	for idx := range groups {
		group := groups[idx]
		label := descriptionGroupLabel(group)
		prompt := "Edit generated description? [y/N]: "
		if len(groups) > 1 {
			prompt = fmt.Sprintf("Edit generated description for %s? [y/N]: ", label)
		}
		edit, err := promptYesNo(reader, prompt, false)
		if err != nil {
			return req, err
		}
		if !edit {
			continue
		}

		fmt.Printf("Editing description %s\n", label)
		edited, didChange, err := editCLIDescriptionFile(ctx, group.RawDescription)
		if err != nil {
			return req, fmt.Errorf("upbrr: edit description %s: %w", label, err)
		}
		if !didChange {
			fmt.Printf("Description %s unchanged.\n", label)
			continue
		}

		saveReq := req
		saveReq.DescriptionOverrideGroup = group.GroupKey
		if len(group.Trackers) > 0 {
			saveReq.Trackers = append([]string{}, group.Trackers...)
		}
		updated, err := coreSvc.SaveDescriptionOverride(ctx, saveReq, edited)
		if err != nil {
			return req, fmt.Errorf("upbrr: save description %s: %w", label, err)
		}
		groups[idx] = mergeDescriptionGroupUpdate(group, updated)
		changed = true
		fmt.Printf("Description %s saved.\n", label)
	}
	if !changed {
		return req, nil
	}

	req.DescriptionGroups = replaceDescriptionGroups(preview.Groups, groups)
	return req, nil
}

func filterEditableDescriptionGroups(groups []api.DescriptionBuilderGroup, review api.UploadReview) []api.DescriptionBuilderGroup {
	if len(groups) == 0 {
		return nil
	}
	neededGroups := make(map[string]struct{})
	neededTrackers := make(map[string]struct{})
	for _, tracker := range review.Trackers {
		if tracker.Banned {
			continue
		}
		if group := normalizeCLIDescriptionGroupKey(tracker.DryRun.DescriptionGroup); group != "" {
			neededGroups[group] = struct{}{}
		}
		if name := strings.ToUpper(strings.TrimSpace(tracker.Tracker)); name != "" {
			neededTrackers[name] = struct{}{}
		}
	}
	if len(neededGroups) == 0 && len(neededTrackers) == 0 {
		return append([]api.DescriptionBuilderGroup{}, groups...)
	}

	filtered := make([]api.DescriptionBuilderGroup, 0, len(groups))
	for _, group := range groups {
		if _, ok := neededGroups[normalizeCLIDescriptionGroupKey(group.GroupKey)]; ok {
			filtered = append(filtered, group)
			continue
		}
		for _, tracker := range group.Trackers {
			if _, ok := neededTrackers[strings.ToUpper(strings.TrimSpace(tracker))]; ok {
				filtered = append(filtered, group)
				break
			}
		}
	}
	return filtered
}

func printCLIDescriptionPreview(groups []api.DescriptionBuilderGroup) {
	for _, group := range groups {
		preview := limitedDescriptionPreview(group.RawDescription)
		if preview == "" {
			continue
		}
		fmt.Printf("\n[%s Description Preview]\n%s\n", descriptionGroupLabel(group), preview)
	}
}

func limitedDescriptionPreview(raw string) string {
	const maxLines = 8
	const maxChars = 800
	const maxLineChars = 140

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	selected := make([]string, 0, maxLines)
	usedChars := 0
	truncated := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if runeCount(line) > maxLineChars {
			line = truncateRunes(line, maxLineChars) + "..."
			truncated = true
		}
		lineChars := runeCount(line)
		if usedChars+lineChars > maxChars {
			remaining := maxChars - usedChars
			if remaining > 0 {
				selected = append(selected, truncateRunes(line, remaining)+"...")
			}
			truncated = true
			break
		}
		selected = append(selected, line)
		usedChars += lineChars
		if len(selected) >= maxLines {
			truncated = true
			break
		}
	}
	if len(selected) == 0 {
		return ""
	}
	if truncated {
		selected = append(selected, "...")
	}
	return strings.Join(selected, "\n")
}

func runeCount(value string) int {
	return len([]rune(value))
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func descriptionGroupLabel(group api.DescriptionBuilderGroup) string {
	groupKey := strings.TrimSpace(group.GroupKey)
	trackers := make([]string, 0, len(group.Trackers))
	for _, tracker := range group.Trackers {
		if name := strings.ToUpper(strings.TrimSpace(tracker)); name != "" && !slices.Contains(trackers, name) {
			trackers = append(trackers, name)
		}
	}
	if len(trackers) == 0 {
		if groupKey == "" {
			return "(default)"
		}
		return groupKey
	}
	if groupKey == "" {
		return "(" + strings.Join(trackers, ", ") + ")"
	}
	return fmt.Sprintf("%s (%s)", groupKey, strings.Join(trackers, ", "))
}

func normalizeCLIDescriptionGroupKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func mergeDescriptionGroupUpdate(original api.DescriptionBuilderGroup, updated api.DescriptionBuilderGroup) api.DescriptionBuilderGroup {
	if len(updated.Trackers) == 0 {
		updated.Trackers = append([]string{}, original.Trackers...)
	}
	if strings.TrimSpace(updated.GroupKey) == "" {
		updated.GroupKey = original.GroupKey
	}
	if strings.TrimSpace(updated.Description) == "" {
		updated.Description = updated.RawDescription
	}
	if strings.TrimSpace(updated.DescriptionHTML) == "" {
		updated.DescriptionHTML = updated.RawDescriptionHTML
	}
	updated.ImageHost = original.ImageHost
	return updated
}

func replaceDescriptionGroups(existing []api.DescriptionBuilderGroup, edited []api.DescriptionBuilderGroup) []api.DescriptionBuilderGroup {
	result := append([]api.DescriptionBuilderGroup{}, existing...)
	for _, updated := range edited {
		replaced := false
		updatedKey := normalizeCLIDescriptionGroupKey(updated.GroupKey)
		for idx := range result {
			if normalizeCLIDescriptionGroupKey(result[idx].GroupKey) == updatedKey {
				result[idx] = updated
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, updated)
		}
	}
	return result
}

func editDescriptionFile(ctx context.Context, initial string) (string, bool, error) {
	file, err := os.CreateTemp("", "upbrr-description-*.bbcode")
	if err != nil {
		return "", false, fmt.Errorf("create temp description: %w", err)
	}
	path := file.Name()
	defer os.Remove(path)

	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return "", false, fmt.Errorf("chmod temp description: %w", err)
	}
	if _, err := file.WriteString(initial); err != nil {
		_ = file.Close()
		return "", false, fmt.Errorf("write temp description: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", false, fmt.Errorf("close temp description: %w", err)
	}

	editor, err := descriptionEditorCommand()
	if err != nil {
		return "", false, err
	}
	args := append(append([]string{}, editor[1:]...), path)
	//nolint:gosec // VISUAL/EDITOR intentionally controls the local editor command.
	cmd := exec.CommandContext(ctx, editor[0], args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", false, fmt.Errorf("run editor %q: %w", editor[0], err)
	}

	edited, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read edited description: %w", err)
	}
	editedText := string(edited)
	return editedText, strings.TrimSpace(editedText) != strings.TrimSpace(initial), nil
}

func descriptionEditorCommand() ([]string, error) {
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			parts, err := splitEditorCommand(value)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", key, err)
			}
			if len(parts) == 0 {
				continue
			}
			return parts, nil
		}
	}
	return defaultDescriptionEditorCommand()
}

func defaultDescriptionEditorCommand() ([]string, error) {
	if runtime.GOOS == "windows" {
		return []string{"notepad.exe"}, nil
	}
	if runtime.GOOS == "darwin" {
		for _, editor := range []string{"nano", "vi"} {
			if _, err := exec.LookPath(editor); err == nil {
				return []string{editor}, nil
			}
		}
		return []string{"open", "-W", "-t"}, nil
	}
	for _, editor := range []string{"nano", "vi", "vim"} {
		if _, err := exec.LookPath(editor); err == nil {
			return []string{editor}, nil
		}
	}
	return nil, errors.New("no editor found; set VISUAL or EDITOR")
}

func splitEditorCommand(value string) ([]string, error) {
	var parts []string
	var current strings.Builder
	var quote rune
	for _, r := range value {
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if quote != 0 {
		return nil, errors.New("unterminated quote")
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts, nil
}
