// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// ImageCollectionCategoryAnalysis combines exact HDR and audio images in one set.
const ImageCollectionCategoryAnalysis = "HDR and audio data"

// ImageCollectionMember preserves ordered, content-bound member identity without local paths.
type ImageCollectionMember struct {
	Identity string
	Label    string
}

// ImageCollection is a complete set link, never an embeddable image URL.
// Fingerprint binds the ordered content, generation, labels, purpose and host configuration.
type ImageCollection struct {
	// ContentFingerprint fences ambiguous attempts even across preparation generations.
	ContentFingerprint string
	// AttemptID and EffectID bind private reservation writes to one durable attempt.
	AttemptID    string `json:"AttemptID,omitempty"`
	EffectID     string `json:"EffectID,omitempty"`
	Fingerprint  string
	Host         string
	URL          string
	Purpose      ScreenshotPurpose
	Category     string
	Label        string
	AccountScope string
	Completed    bool
	Members      []ImageCollectionMember
}

// ImageCollectionInput is private workflow authority for an ordered comparison matrix.
type ImageCollectionInput struct {
	Generation string
	Purpose    ScreenshotPurpose
	Category   string
	Label      string
	Images     []ScreenshotImage
	Labels     []string
	// Columns is the source count; zero or one keeps all images in a single viewer row.
	Columns int
}

// DescriptionComparison binds one imported block to its exact destination trackers.
// Collection is populated only after every source image has been uploaded.
type DescriptionComparison struct {
	BBCode     string
	Sources    []string
	ImageURLs  []string
	Trackers   []string
	Collection *ImageCollection
}

// AllowsCollectionHosting reports the only image purposes accepted by collection hosts.
func (p ScreenshotPurpose) AllowsCollectionHosting() bool {
	return p == ScreenshotPurposeComparison || p == ScreenshotPurposeHDRAnalysis || p == ScreenshotPurposeAudioAnalysis
}

// CollectionLabel removes paths, markup and control characters from transmitted display names.
func CollectionLabel(value, fallback string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	if index := strings.LastIndex(value, "/"); index >= 0 {
		value = value[index+1:]
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '[' || r == ']' || r == '<' || r == '>' {
			return -1
		}
		if r == '_' {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 120 {
		value = string(runes[:120])
	}
	if value == "" || value == "." || value == ".." {
		return fallback
	}
	return value
}

// Validate rejects partial sets and URLs that cannot safely appear as slow.pics links.
func (c ImageCollection) Validate() error {
	if !c.Completed || c.Host != "slowpics" || c.Fingerprint == "" || c.ContentFingerprint == "" || c.AccountScope == "" ||
		!c.Purpose.AllowsCollectionHosting() || len(c.Members) == 0 || c.Label != CollectionLabel(c.Label, "") || c.Label == "" {
		return errors.New("invalid completed image collection")
	}
	if !ValidImageCollectionCategory(c.Purpose, c.Category) {
		return errors.New("invalid image collection category")
	}
	parsed, err := url.Parse(c.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "slow.pics" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		!strings.HasPrefix(parsed.Path, "/c/") || parsed.RawPath != "" {
		return errors.New("invalid image collection URL")
	}
	key := strings.TrimPrefix(parsed.Path, "/c/")
	if len(key) == 0 || len(key) > 128 {
		return errors.New("invalid image collection key")
	}
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return errors.New("invalid image collection key")
		}
	}
	for _, member := range c.Members {
		if member.Identity == "" || member.Label == "" || member.Label != CollectionLabel(member.Label, "") {
			return errors.New("invalid image collection member")
		}
	}
	return nil
}

// ValidImageCollectionCategory keeps presentation categories within producer authority.
func ValidImageCollectionCategory(purpose ScreenshotPurpose, category string) bool {
	if purpose.IsAnalysis() && category == ImageCollectionCategoryAnalysis {
		return true
	}
	if purpose == ScreenshotPurposeComparison {
		return category == "Comps"
	}
	if purpose == ScreenshotPurposeAudioAnalysis {
		return category == "Audio Plots"
	}
	return purpose == ScreenshotPurposeHDRAnalysis &&
		(category == "Audio Plots" || category == "Plots" || category == "Grade Checks" || category == "Alignment Checks")
}

// ImageCollectionEffectScope binds reconciliation to one attempt, never all sets.
func ImageCollectionEffectScope(attemptID string) string { return "slowpics_collection:" + attemptID }

// IsImageCollectionEffectScope distinguishes collection recovery from individual images.
func IsImageCollectionEffectScope(scope string) bool {
	return strings.HasPrefix(scope, "slowpics_collection:") && len(scope) > len("slowpics_collection:")
}

// ImageCollectionOutcomeError carries the durable attempt behind a safe failure.
// An empty attempt cannot authorize recovery of a legacy or unmanaged reservation.
type ImageCollectionOutcomeError struct {
	AttemptID string
	Cause     error
}

func (e *ImageCollectionOutcomeError) Error() string { return e.Cause.Error() }
func (e *ImageCollectionOutcomeError) Unwrap() error { return e.Cause }
