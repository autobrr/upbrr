// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package oe

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/config"
	descriptionunit3d "github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

var oeEvidenceBlockPattern = regexp.MustCompile(`(?is)\[b\](?:Encoding Settings|Source Notes)\[/b\]\s*\[code\].*?\[/code\]`)

const (
	oeEncodingSettingsLabel = "Encoding Settings"
	oeSourceNotesLabel      = "Source Notes"
	oeMinimumScreenshots    = 3
)

// buildDescription preserves OE-supported BBCode while applying the tracker
// markup conversions and required description evidence.
func buildDescription(
	ctx context.Context,
	meta api.UploadSubject,
	appConfig config.Config,
	_ config.TrackerConfig,
	logger api.Logger,
	keptDescription string,
	menuImages []api.ScreenshotImage,
	screenshots []api.ScreenshotImage,
) (string, error) {
	preparedScreenshots, err := oeRenderableScreenshots(screenshots)
	if err != nil {
		return "", err
	}
	evidence, err := oeDescriptionEvidence(meta)
	if err != nil {
		return "", err
	}

	base := prepareOEText(descriptionunit3d.StripScreenshotBlocks(keptDescription))
	meta.DescriptionTemplate = oeEvidenceBlockPattern.ReplaceAllString(prepareOEText(descriptionunit3d.StripScreenshotBlocks(meta.DescriptionTemplate)), "")
	base = appendOEDescriptionEvidence(base, evidence)

	cfg := appConfig
	cfg.Description.AddLogo = false
	cfg.Description.ThumbnailSize = 350
	description, err := descriptionunit3d.ComposeDescription(
		ctx,
		api.NewDescriptionSubject(meta),
		cfg,
		logger,
		base,
		menuImages,
		preparedScreenshots,
	)
	if err != nil {
		return "", fmt.Errorf("trackers: %w", err)
	}
	return oeAppendMissingScreenshotLinks(description, preparedScreenshots, cfg.Description.ThumbnailSize), nil
}

func prepareOEText(value string) string {
	value = bbcode.NormalizeNewlines(trackers.StripDescriptionSignatures(value))
	value = bbcode.ConvertPreToCode(value)
	value = bbcode.ConvertHideToSpoiler(value)
	value = bbcode.ConvertComparisonToCollapse(value, 1000)
	return strings.ReplaceAll(value, "[img]", "[img=300]")
}

func oeRenderableScreenshots(screenshots []api.ScreenshotImage) ([]api.ScreenshotImage, error) {
	prepared := make([]api.ScreenshotImage, 0, len(screenshots))
	seen := make(map[string]struct{}, len(screenshots))
	for _, screenshot := range screenshots {
		key := cmp.Or(strings.TrimSpace(screenshot.RawURL), strings.TrimSpace(screenshot.ImgURL))
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(screenshot.WebURL) == "" {
			screenshot.WebURL = key
		}
		if strings.TrimSpace(screenshot.RawURL) == "" {
			screenshot.RawURL = key
		}
		screenshot.ImgURL = key
		prepared = append(prepared, screenshot)
	}
	if len(prepared) < oeMinimumScreenshots {
		return nil, fmt.Errorf("OE requires at least %d unique renderable screenshots", oeMinimumScreenshots)
	}
	return prepared, nil
}

func oeAppendMissingScreenshotLinks(description string, screenshots []api.ScreenshotImage, thumbnailSize int) string {
	missing := make([]api.ScreenshotImage, 0, len(screenshots))
	for _, screenshot := range screenshots {
		if !oeHasLinkedScreenshot(description, screenshot) {
			missing = append(missing, screenshot)
		}
	}
	if len(missing) == 0 {
		return description
	}
	if thumbnailSize <= 0 {
		thumbnailSize = 350
	}
	links := make([]string, 0, len(missing))
	for _, screenshot := range missing {
		links = append(links, fmt.Sprintf(
			"[url=%s][img=%d]%s[/img][/url]",
			screenshot.WebURL,
			thumbnailSize,
			screenshot.RawURL,
		))
	}
	return strings.TrimSpace(description) + "\n\n[center]" + strings.Join(links, "") + "[/center]"
}

func oeHasLinkedScreenshot(description string, screenshot api.ScreenshotImage) bool {
	linkPrefix := "[url=" + screenshot.WebURL + "][img"
	for remaining := description; ; {
		start := strings.Index(remaining, linkPrefix)
		if start < 0 {
			return false
		}
		remaining = remaining[start+len(linkPrefix):]
		closeTag := strings.Index(remaining, "]")
		if closeTag < 0 {
			return false
		}
		content := remaining[closeTag+1:]
		if strings.HasPrefix(content, screenshot.RawURL+"[/img][/url]") {
			return true
		}
		remaining = content
	}
}

func oeDescriptionEvidence(meta api.UploadSubject) ([]string, error) {
	answers := oeQuestionnaireAnswers(meta)
	blocks := make([]string, 0, 2)
	if oeRequiresEncodingSettings(meta.VideoCodec, meta.Type, meta.HasEncodeSettings) {
		settings := strings.TrimSpace(answers[oeEncodingSettingsKey])
		if settings == "" {
			return nil, errors.New("OE requires AV1 encoding settings when MediaInfo does not provide them")
		}
		blocks = append(blocks, oeDescriptionEvidenceBlock(oeEncodingSettingsLabel, settings))
	}
	if oeRequiresSourceNotes(meta.Tag) {
		notes := strings.TrimSpace(answers[oeSourceNotesKey])
		if notes == "" {
			return nil, errors.New("OE requires source notes for SM737 releases")
		}
		blocks = append(blocks, oeDescriptionEvidenceBlock(oeSourceNotesLabel, notes))
	}
	return blocks, nil
}

func appendOEDescriptionEvidence(description string, evidence []string) string {
	description = oeEvidenceBlockPattern.ReplaceAllString(description, "")
	parts := make([]string, 0, len(evidence)+1)
	if base := strings.TrimSpace(description); base != "" {
		parts = append(parts, base)
	}
	parts = append(parts, evidence...)
	return strings.Join(parts, "\n\n")
}

func oeDescriptionEvidenceBlock(label string, value string) string {
	return "[b]" + label + "[/b]\n[code]" + strings.TrimSpace(value) + "[/code]"
}
