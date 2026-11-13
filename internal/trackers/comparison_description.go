// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode/comparison"
	"github.com/autobrr/upbrr/internal/comparisons"
	"github.com/autobrr/upbrr/internal/config"
	descriptiontext "github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/pkg/api"
)

var sourceComparisonBlockPattern = regexp.MustCompile(`(?is)\[spoiler=source_comparisons\].*?\[/spoiler\]`)

// exactComparisonDescriptionBlock emits only complete, reviewed rows, in source
// order. A missing hosted cell fails the whole block unless hosting was skipped,
// in which case the entire unavailable row is omitted.
func exactComparisonDescriptionBlock(tracker string, exact *api.ExactMediaAssets) (string, error) {
	if exact == nil || exact.Comparisons == nil {
		return "", nil
	}
	plan := exact.Comparisons
	if problems := comparisons.Validate(*plan); len(problems) > 0 {
		return "", errors.New("comparison rows require correction before description generation")
	}
	fingerprint, err := comparisons.ReviewFingerprint(*plan)
	if err != nil || plan.Fingerprint != fingerprint || plan.ApprovedFingerprint != fingerprint {
		return "", errors.New("comparison images require current review approval before description generation")
	}
	labels := make([]string, 0, len(plan.Sources))
	for _, source := range plan.Sources {
		labels = append(labels, strings.TrimSpace(source.Label))
	}
	var images []string
	for _, row := range plan.Rows {
		if row.Excluded {
			continue
		}
		urls := make([]string, 0, len(plan.Sources))
		for _, source := range plan.Sources {
			cellIndex := slices.IndexFunc(row.Cells, func(cell api.ComparisonCell) bool { return cell.SourceID == source.ID })
			cell := row.Cells[cellIndex] // Complete-row validation above establishes every column.
			image := exact.ComparisonImages[cell.ArtifactID]
			upload, ok := exactUploadedVariant(tracker, image.Path, exact.ComparisonUploads[cell.ArtifactID], "")
			imageURL := strings.TrimSpace(upload.RawURL)
			if imageURL == "" {
				imageURL = strings.TrimSpace(upload.ImgURL)
			}
			if !ok || imageURL == "" || upload.Purpose != api.ScreenshotPurposeComparison ||
				upload.ContentSHA256 != cell.ContentSHA256 || image.ContentSHA256 != cell.ContentSHA256 {
				if !exact.ImageHostUploadSkipped {
					return "", fmt.Errorf("hosted comparison row %s is incomplete for %s", row.ID, tracker)
				}
				urls = nil
				break
			}
			urls = append(urls, "[img]"+imageURL+"[/img]")
		}
		images = append(images, urls...)
	}
	if len(images) == 0 {
		return "", nil
	}
	return "[spoiler=source_comparisons]\n[comparison=" + strings.Join(labels, ", ") + "]\n" +
		strings.Join(images, "\n") + "\n[/comparison]\n[/spoiler]", nil
}

func replaceComparisonDescriptionBlock(description, block string) string {
	description = descriptiontext.MapOutsideLiteralBlocks(description, func(value string) string {
		protected := comparison.BlockRanges(value)
		var result strings.Builder
		last := 0
		for _, section := range sourceComparisonBlockPattern.FindAllStringIndex(value, -1) {
			if slices.ContainsFunc(protected, func(span [2]int) bool {
				return span[0] <= section[0] && span[1] >= section[1]
			}) {
				continue
			}
			result.WriteString(value[last:section[0]])
			last = section[1]
		}
		result.WriteString(value[last:])
		return result.String()
	})
	return strings.TrimSpace(strings.Join([]string{strings.TrimSpace(description), block}, "\n\n"))
}

// comparisonMediaForTrackerHost applies the existing screenshot host policy to
// comparison variants without flattening the retained row/column associations.
func comparisonMediaForTrackerHost(
	tracker string, meta api.UploadSubject, exact *api.ExactMediaAssets, appCfg config.Config,
	trackerCfg config.TrackerConfig, registry *Registry, preferredHosts []string,
) error {
	view := &api.ExactMediaAssets{}
	for _, row := range exact.Comparisons.Rows {
		if row.Excluded {
			continue
		}
		for _, cell := range row.Cells {
			view.Screenshots = append(view.Screenshots, exact.ComparisonImages[cell.ArtifactID])
			view.ScreenshotUploads = append(view.ScreenshotUploads, exact.ComparisonUploads[cell.ArtifactID]...)
		}
	}
	meta.ExactMedia = view // No comparison plan: the policy pass cannot recurse.
	selected, err := exactMediaForTrackerHost(tracker, meta, appCfg, trackerCfg, registry, preferredHosts...)
	if err != nil {
		return err
	}
	for id, uploads := range exact.ComparisonUploads {
		exact.ComparisonUploads[id] = slices.DeleteFunc(uploads, func(upload api.UploadedImageLink) bool {
			return !slices.Contains(selected.ScreenshotUploads, upload)
		})
	}
	return nil
}
