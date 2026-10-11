// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/autobrr/upbrr/internal/bbcode"
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/internal/description/unit3d"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// buildDescription composes ANT's reviewed text separately from the API screenshot
// payload. Reviewed text is preserved apart from ANT's TMDB logo exclusion.
func buildDescription(ctx context.Context, req trackers.PreparationInput, assets trackers.DescriptionAssets) (string, error) {
	if assets.Final {
		return strings.TrimSpace(stripTMDBLogoImages(assets.Description, req.Meta.ProviderMetadata.TMDB)), nil
	}
	cfg := req.Runtime.DescriptionConfig()
	cfg.Description.AddLogo = false
	meta := api.NewDescriptionSubject(req.Meta)
	meta.DescriptionTemplate = prepareDescriptionText(meta.DescriptionTemplate, cfg.Description.ScreenshotHeader, assets.Screenshots)
	base, audioAnalysis := description.SplitTrailingSourceAudioSpoiler(assets.Description)
	base = prepareDescriptionText(base, cfg.Description.ScreenshotHeader, assets.Screenshots)
	// ANT receives ordinary screenshots separately. Without retained applied
	// tonemapping evidence, their configuration alone cannot justify a notice.
	if audioAnalysis != "" {
		base = strings.TrimSpace(base + "\n\n" + audioAnalysis)
	}
	composed, err := unit3d.ComposeDescription(ctx, meta, cfg, req.Logger, base, assets.MenuImages, nil)
	if err != nil {
		return "", fmt.Errorf("trackers: ANT description: %w", err)
	}
	finalized := finalizeDescription(stripTMDBLogoImages(composed, req.Meta.ProviderMetadata.TMDB))
	if req.Intent == trackers.PreparationIntentDryRun {
		unit3d.SaveDescriptionDebug(meta, "ANT", req.Runtime.DBPath, finalized, req.Logger)
	}
	return finalized, nil
}

// stripTMDBLogoImages removes image tags for the prepared TMDB logo identities,
// including other TMDB sizes of the same asset. Complete code and pre blocks
// remain unchanged; absent metadata leaves the text intact.
func stripTMDBLogoImages(value string, metadata *api.TMDBMetadata) string {
	if metadata == nil {
		return value
	}
	for _, logoURL := range []string{metadata.Logo, metadata.TMDBLogo} {
		if logoURL = strings.TrimSpace(logoURL); logoURL == "" {
			continue
		}
		logoPattern := regexp.QuoteMeta(logoURL)
		// TMDB supplies an original URL and a bare asset filename. Imported
		// tracker descriptions can use another size of the same logo asset.
		if parsed, err := url.Parse(logoURL); err == nil {
			logoFile := strings.TrimPrefix(parsed.Path, "/")
			if parsed.Host != "" {
				logoFile = ""
				if strings.EqualFold(parsed.Hostname(), "image.tmdb.org") {
					if imagePath, ok := strings.CutPrefix(parsed.EscapedPath(), "/t/p/"); ok {
						_, logoFile, _ = strings.Cut(imagePath, "/")
					}
				}
			}
			if logoFile != "" && !strings.Contains(logoFile, "/") {
				logoPattern = `(?:` + logoPattern + `|(?i:https?://image\.tmdb\.org)/t/p/[^/\s]+/` + regexp.QuoteMeta(logoFile) + `)`
			}
		}
		image := regexp.MustCompile(`(?is:\[img[^\]]*\])\s*` + logoPattern + `\s*(?i:\[/img\])`)
		value = description.MapOutsideLiteralBlocks(value, func(fragment string) string {
			return image.ReplaceAllString(fragment, "")
		})
	}
	return value
}

// prepareDescriptionText removes only owned screenshot content and known uploader
// signatures. Other images, release notes, and supported user markup are retained.
// Complete literal and comparison blocks bypass this cleanup.
func prepareDescriptionText(value, screenshotHeader string, screenshots []api.ScreenshotImage) string {
	return description.MapOutsideLiteralBlocks(value, func(value string) string {
		value = bbcode.NormalizeNewlines(value)
		return strings.TrimSpace(unit3d.PrepareSiteText(value, func(fragment string) string {
			leading := len(fragment) - len(strings.TrimLeft(fragment, "\n"))
			trailing := len(fragment) - len(strings.TrimRight(fragment, "\n"))
			cleaned := prepareDescriptionFragment(fragment, screenshotHeader, screenshots)
			return strings.Repeat("\n", leading) + strings.Trim(cleaned, "\n") + strings.Repeat("\n", trailing)
		}))
	})
}

func prepareDescriptionFragment(value, screenshotHeader string, screenshots []api.ScreenshotImage) string {
	value = trackers.StripDescriptionSignatures(value)
	var images []*regexp.Regexp
	for _, screenshot := range screenshots {
		for _, imageURL := range []string{screenshot.ImgURL, screenshot.RawURL, screenshot.WebURL} {
			if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
				images = append(images, regexp.MustCompile(`(?is:\[img[^\]]*\])\s*`+regexp.QuoteMeta(imageURL)+`\s*(?i:\[/img\])`))
			}
		}
	}
	header := strings.TrimSpace(bbcode.NormalizeNewlines(screenshotHeader))
	parts := strings.Split(bbcode.RemoveExtraLines(value), "\n\n")
	for index, part := range parts {
		for _, image := range images {
			part = image.ReplaceAllString(part, "")
		}
		if part == parts[index] {
			continue
		}
		part = antEmptyURLPattern.ReplaceAllString(part, "")
		part = antEmptyAlignmentPattern.ReplaceAllString(part, "")
		if header != "" {
			// Only discard a header belonging to the removed screenshot section.
			if strings.TrimSpace(part) == header {
				part = ""
			} else if strings.TrimSpace(part) == "" && index > 0 && strings.TrimSpace(parts[index-1]) == header {
				parts[index-1] = ""
			}
		}
		parts[index] = part
	}
	return strings.TrimSpace(bbcode.RemoveExtraLines(strings.Join(parts, "\n\n")))
}

// resolveScreenshotPayload selects up to four nonempty URLs in input order,
// preferring RawURL to ImgURL. Disabled screenshot submission returns empty text.
func resolveScreenshotPayload(images []api.ScreenshotImage, allow bool) string {
	if !allow || len(images) == 0 {
		return ""
	}
	urls := make([]string, 0, 4)
	for _, image := range images {
		rawURL := strings.TrimSpace(image.RawURL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(image.ImgURL)
		}
		if rawURL == "" {
			continue
		}
		urls = append(urls, rawURL)
		if len(urls) == 4 {
			break
		}
	}
	return strings.Join(urls, "\n")
}
