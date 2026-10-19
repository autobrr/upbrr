// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // Registers comparison source decoders.
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "golang.org/x/image/webp" // Registers the WebP comparison source decoder.

	"github.com/autobrr/upbrr/internal/bbcode/comparison"
	"github.com/autobrr/upbrr/internal/description"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	"github.com/autobrr/upbrr/internal/logging"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	dbsvc "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// ResolveDescriptionComparisons snapshots effective non-final comparison blocks
// without downloading images or changing stored source descriptions.
func (s *Service) ResolveDescriptionComparisons(
	ctx context.Context, subject api.DescriptionSubject, trackersList []string,
) (result []api.DescriptionComparison, resolveErr error) {
	logger := logging.FromContext(ctx, s.logger)
	defer func() {
		if resolveErr != nil {
			logger.Warnf("trackers: description comparisons state=blocked stage=resolve")
		}
	}()
	meta := uploadSubjectForDescription(subject)
	preloaded, err := preloadDescriptionAssetData(ctx, meta, s.repo, s.registry)
	if err != nil {
		return nil, err
	}
	byBlock := make(map[string]int)
	for _, tracker := range trackersList {
		mode, ok := s.registry.LookupUploadContentMode(tracker)
		if !ok || !mode.UsesDescription() {
			continue
		}
		text, _, final := resolveTrackerDescription(ctx, tracker, meta, s.repo, nil, preloaded, s.registry)
		if final {
			continue
		}
		var invalid bool
		description.MapComparisonBlocks(text, func(block comparison.Block) string {
			if len(block.Images) == 0 {
				return block.BBCode
			}
			if len(block.Images) > 100 || len(block.Images)%max(1, len(block.Sources)) != 0 {
				invalid = true
				return block.BBCode
			}
			index, exists := byBlock[block.BBCode]
			if !exists {
				index = len(result)
				byBlock[block.BBCode] = index
				result = append(result, api.DescriptionComparison{
					BBCode:    block.BBCode,
					Sources:   block.Sources,
					ImageURLs: block.Images,
				})
			}
			if !slices.Contains(result[index].Trackers, tracker) {
				result[index].Trackers = append(result[index].Trackers, tracker)
			}
			return block.BBCode
		})
		if invalid {
			return nil, errors.New("description comparison requires complete source rows and at most 100 images")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("resolve description comparisons: %w", err)
	}
	logger.Debugf("trackers: description comparisons state=resolved collections=%d trackers=%d", len(result), len(trackersList))
	return result, nil
}

// MaterializeDescriptionComparison uses the guarded description downloader and
// private release cache. Explicit skip never downloads missing source images.
func (s *Service) MaterializeDescriptionComparison(
	ctx context.Context, subject api.DescriptionSubject, item api.DescriptionComparison,
) (api.ImageCollectionInput, error) {
	if len(item.ImageURLs) == 0 || len(item.ImageURLs) > 100 || len(item.ImageURLs)%max(1, len(item.Sources)) != 0 {
		return api.ImageCollectionInput{}, errors.New("invalid description comparison matrix")
	}
	generation, err := api.CanonicalWorkflowFingerprint(struct {
		BBCode string
	}{item.BBCode})
	if err != nil {
		return api.ImageCollectionInput{}, fmt.Errorf("description comparison identity: %w", err)
	}
	group := string(generation)
	input := api.ImageCollectionInput{
		Generation: group,
		Purpose:    api.ScreenshotPurposeComparison,
		Category:   "Comps",
		Label:      api.CollectionLabel(strings.Join(item.Sources, " vs "), "Comparison"),
		Columns:    max(1, len(item.Sources)),
	}
	tmpRoot, err := dbsvc.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return input, fmt.Errorf("description comparison cache: %w", err)
	}
	meta := uploadSubjectForDescription(subject)
	dir, err := paths.DescriptionComparisonTempRoot(tmpRoot, meta.SourcePath)
	if err != nil {
		return input, fmt.Errorf("description comparison release cache: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return input, fmt.Errorf("description comparison cache directory: %w", err)
	}
	skip := subject.ExactMedia != nil && subject.ExactMedia.ImageHostUploadSkipped
	logger := logging.FromContext(ctx, s.logger)
	logger.Debugf("trackers: description comparison state=materializing members=%d columns=%d skip=%t", len(item.ImageURLs), input.Columns, skip)
	client := newDescriptionSlotImageHTTPClient()
	if !skip && slices.ContainsFunc(item.ImageURLs, func(rawURL string) bool {
		return isPTPDescriptionImageURL(imagehost.DirectImageURL(rawURL))
	}) {
		client = PTPDescriptionImageHTTPClient(ctx, client, s.cfg, logging.FromContext(ctx, s.logger))
	}
	var total int64
	for index, rawURL := range item.ImageURLs {
		if err := ctx.Err(); err != nil {
			return input, fmt.Errorf("description comparison canceled: %w", err)
		}
		digest := sha256.Sum256([]byte(rawURL))
		cached := filepath.Join(dir, fmt.Sprintf("%x.png", digest))
		if info, err := os.Stat(cached); err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			if skip {
				return api.ImageCollectionInput{}, nil
			}
			logger.Debugf("trackers: comparison image download state=started index=%d count=%d", index+1, len(item.ImageURLs))
			source := filepath.Join(dir, fmt.Sprintf("%x.source", digest))
			if err := downloadDescriptionSlotImage(ctx, client, rawURL, source); err != nil {
				if canceled := ctx.Err(); canceled != nil {
					return input, fmt.Errorf("description comparison canceled: %w", canceled)
				}
				return input, fmt.Errorf("description comparison image %d download failed", index+1)
			}
			if err := comparisonSourcePNG(source, cached); err != nil {
				return input, fmt.Errorf("description comparison image %d: %w", index+1, err)
			}
		}
		info, err := os.Stat(cached)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 20<<20 {
			return input, errors.New("description comparison image exceeds the upload size limit")
		}
		total += info.Size()
		if total > 100<<20 {
			return input, errors.New("description comparison exceeds the upload size limit")
		}
		label := fmt.Sprintf("Image %d", index+1)
		if len(item.Sources) > 0 {
			label = api.CollectionLabel(item.Sources[index%input.Columns], label)
		}
		input.Images = append(input.Images, api.ScreenshotImage{
			Path:            cached,
			Purpose:         api.ScreenshotPurposeFinal,
			ComparisonGroup: group,
			ComparisonCount: len(item.ImageURLs),
		})
		input.Labels = append(input.Labels, label)
	}
	return input, nil
}

func comparisonSourcePNG(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil || len(data) == 0 || len(data) > 20<<20 {
		return errors.New("comparison source exceeds the image size limit")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 64<<20 {
		return errors.New("comparison source is not a supported image within the pixel limit")
	}
	if format == "gif" {
		return errors.New("comparison sources must be still PNG, JPEG or WebP images")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return errors.New("comparison source image is corrupt")
	}
	if format != "png" {
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, decoded); err != nil {
			return fmt.Errorf("encode comparison PNG: %w", err)
		}
		data = encoded.Bytes()
	}
	if len(data) > 20<<20 {
		return errors.New("comparison PNG exceeds the image size limit")
	}
	// #nosec G703 -- destination is a SHA-256 basename in the managed per-input comparison cache.
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		return errors.New("cannot cache comparison PNG")
	}
	return nil
}
