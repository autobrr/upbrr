// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"context"
	"html"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/trackers"
)

var (
	azTagStripPattern  = regexp.MustCompile(`(?is)\[/?(?:size|align|left|center|right|img|table|tr|td|spoiler|code|url)[^\]]*\]`)
	azNFOStripPattern  = regexp.MustCompile(`(?is)\[center\]\[spoiler=.*? NFO:\]\[code\].*?\[/code\]\[/spoiler\]\[/center\]`)
	azLinkStripPattern = regexp.MustCompile(`https?://\S+|www\.\S+`)
)

func buildDescription(raw string, defaults config.DescriptionSettingsConfig) string {
	trimmed := trackers.StripDescriptionSignatures(raw)
	if trimmed == "" {
		return ""
	}
	trimmed, audioAnalysis := description.SplitTrailingSourceAudioSpoiler(trimmed)
	trimmed = azNFOStripPattern.ReplaceAllString(trimmed, "")
	trimmed = azLinkStripPattern.ReplaceAllString(trimmed, "")
	trimmed = azTagStripPattern.ReplaceAllString(trimmed, "")
	escaped := html.EscapeString(strings.TrimSpace(trimmed))
	lines := strings.Split(escaped, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		cleaned = append(cleaned, value)
	}
	base := strings.Join(cleaned, "<br>\n")
	if audioAnalysis == "" {
		return base
	}
	audioAnalysis = description.RelabelAnalysis(audioAnalysis, defaults.AudioPlotsLabel, defaults.HDRPlotsLabel)
	parts := []description.Section{
		{ID: "kept", Text: base},
		{ID: "audio_analysis", Text: description.Render(audioAnalysis)},
	}
	if base == "" && !defaults.ItemOptions["audio_analysis"].Hidden {
		// Retain the original separator behavior when analysis is the only item.
		return "<br>\n" + parts[1].Text
	}
	return strings.TrimSpace(description.FormatSectionsSeparated(parts, defaults, "<br>\n"))
}

func buildDescriptionFromAssets(_ context.Context, req trackers.PreparationInput) string {
	assets, err := trackers.PreparedDescriptionAssets(req.Assets)
	if err != nil {
		return ""
	}
	if assets.Final {
		return strings.TrimSpace(assets.Description)
	}
	return buildDescription(assets.Description, req.Runtime.Description)
}

// DescriptionLayout reflects the two text groups the AZ HTML composer emits.
func (*Definition) DescriptionLayout() []trackers.DescriptionLayoutItem {
	return []trackers.DescriptionLayoutItem{
		{ID: "kept", Label: "Imported or custom text"},
		{
			ID:     "audio_analysis",
			Label:  "Audio and HDR analysis",
			Fields: []string{"AudioPlotsLabel", "HDRPlotsLabel"},
		},
	}
}

// RenderDescriptionSample uses the AZ HTML composer without supplying media or credentials.
func (*Definition) RenderDescriptionSample(
	_ context.Context,
	sample trackers.DescriptionSample,
	defaults config.DescriptionSettingsConfig,
	_ config.TrackerConfig,
) (string, error) {
	return buildDescription(sample.Assets.Description, defaults), nil
}
