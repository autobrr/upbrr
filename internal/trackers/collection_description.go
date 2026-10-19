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
	"github.com/autobrr/upbrr/internal/description"
	"github.com/autobrr/upbrr/pkg/api"
)

func imageCollectionDescriptionLink(collection api.ImageCollection) (string, error) {
	if err := collection.Validate(); err != nil {
		return "", fmt.Errorf("collection description: %w", err)
	}
	label := collection.Label
	if collection.Purpose.IsAnalysis() {
		label = api.ImageCollectionCategoryAnalysis
	}
	return "[url=" + collection.URL + "]" + label + "[/url]", nil
}

var analysisCollectionLinkPattern = regexp.MustCompile(`\[url=https://slow\.pics/c/[A-Za-z0-9_-]{1,128}\]` + api.ImageCollectionCategoryAnalysis + `\[/url\]`)

func exactAnalysisCollectionLinks(exact *api.ExactMediaAssets) (string, error) {
	if exact == nil || exact.CollectionHost != "slowpics" {
		return "", nil
	}
	var links []string
	for _, collection := range exact.Collections {
		if !collection.Purpose.IsAnalysis() {
			continue
		}
		link, err := imageCollectionDescriptionLink(collection)
		if err != nil {
			return "", err
		}
		links = append(links, link)
	}
	return strings.Join(links, "\n"), nil
}

func replaceAnalysisCollectionLinks(value, links string, collections []api.ImageCollection) string {
	comparisonLinks := make(map[string]bool)
	for _, collection := range collections {
		if collection.Purpose == api.ScreenshotPurposeComparison {
			comparisonLinks["[url="+collection.URL+"]"+collection.Label+"[/url]"] = true
		}
	}
	value = description.MapOutsideLiteralAndHTMLTags(value, func(fragment string, _ func(string) string) string {
		return comparison.MapOutsideBlocks(fragment, func(body string) string {
			return analysisCollectionLinkPattern.ReplaceAllStringFunc(body, func(link string) string {
				if comparisonLinks[link] {
					return link
				}
				return ""
			})
		})
	})
	return strings.TrimSpace(strings.Join([]string{value, links}, "\n\n"))
}

func exactComparisonCollectionBlock(exact *api.ExactMediaAssets) (string, error) {
	if exact == nil || exact.CollectionHost != "slowpics" {
		return "", nil
	}
	var links []string
	for _, collection := range exact.Collections {
		if collection.Purpose != api.ScreenshotPurposeComparison {
			continue
		}
		if slices.ContainsFunc(exact.DescriptionComparisons, func(item api.DescriptionComparison) bool {
			return item.Collection != nil && item.Collection.Fingerprint == collection.Fingerprint
		}) {
			continue
		}
		link, err := imageCollectionDescriptionLink(collection)
		if err != nil {
			return "", fmt.Errorf("collection description: %w", err)
		}
		links = append(links, link)
	}
	if len(links) == 0 {
		return "", nil
	}
	return "[spoiler=Comps]\n" + strings.Join(links, "\n") + "\n[/spoiler]", nil
}

func replaceDescriptionComparisonCollections(tracker, value string, exact *api.ExactMediaAssets) (string, error) {
	if exact == nil || exact.CollectionHost != "slowpics" {
		return value, nil
	}
	for _, item := range exact.DescriptionComparisons {
		if item.Collection == nil || !slices.ContainsFunc(item.Trackers, func(name string) bool { return strings.EqualFold(name, tracker) }) {
			continue
		}
		if item.Collection.Purpose != api.ScreenshotPurposeComparison {
			return "", errors.New("collection description: imported comparison has invalid purpose")
		}
		link, err := imageCollectionDescriptionLink(*item.Collection)
		if err != nil {
			return "", err
		}
		value = description.MapComparisonBlocks(value, func(block comparison.Block) string {
			if block.BBCode == item.BBCode {
				return link
			}
			return block.BBCode
		})
	}
	return value, nil
}
