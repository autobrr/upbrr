// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/autobrr/upbrr/internal/config"

	"github.com/autobrr/upbrr/internal/imageevidence"
	imagehost "github.com/autobrr/upbrr/internal/imagehosting/host"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	db "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// ImportURLs executes a registered tracker protocol for selected remote images.
// Only the workflow image stage supplies original source URLs; Upload remains
// the ordinary file-upload route and cannot select this capability implicitly.
// Protected source-only URLs require ordinary hosting of the existing local image.
// Post-import verification failures retain the accepted URL in the private cause
// and require confirmation before another mutation.
func (s *Service) ImportURLs(
	ctx context.Context,
	meta api.ImageHostingSubject,
	host, usageScope string,
	images []api.ScreenshotImage,
) ([]api.UploadedImageLink, error) {
	tracker, capability := trackers.ImageURLImportForScope(s.registry, host, usageScope)
	if capability == nil || capability.Import == nil || !meta.MediaBinding.Valid() {
		return nil, errors.New("image URL import capability is unavailable")
	}
	s.logger.Infof("image hosting: importing selected screenshots host=%s tracker=%s state=started count=%d", host, tracker, len(images))
	trackerCfg, _ := config.TrackerConfigByName(s.cfg.Trackers.Trackers, tracker)
	credential := trackerCfg.APIKey
	account, err := trackers.ImageHostAccountScope(s.cfg, s.registry, host, usageScope)
	if err != nil {
		return nil, fmt.Errorf("image hosting: resolve import account: %w", err)
	}
	root, err := db.Subdir(s.cfg.MainSettings.DBPath, "tmp")
	if err != nil {
		return nil, fmt.Errorf("image hosting: resolve import root: %w", err)
	}
	dir, _, err := paths.ReleaseTempDirFor(root, meta.SourcePath, api.ReleaseInfo{})
	if err != nil {
		return nil, fmt.Errorf("image hosting: resolve import directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("image hosting: create import directory: %w", err)
	}
	var results []api.UploadedImageLink
	for _, image := range images {
		if err := ctx.Err(); err != nil {
			return results, fmt.Errorf("image hosting: import canceled: %w", err)
		}
		source := imagehost.DirectImageURL(image.RawURL)
		if source == "" {
			return results, errors.New("image URL import requires an original source")
		}
		if imagehost.IsSourceOnlyURL(source) {
			return results, errors.New("protected image sources require ordinary hosting")
		}
		digest, err := imageevidence.Digest(image.Path)
		if err != nil {
			return results, errors.New("image URL import source content is unavailable")
		}
		// Both remote endpoints must still describe the selected local bytes.
		if !s.importedImageMatches(ctx, source, dir, digest) {
			return results, errors.New("image URL import source could not be verified")
		}
		hosted, err := s.importImageURL(ctx, capability, credential, source, image.Path)
		if err != nil {
			return results, fmt.Errorf("image hosting: native URL import: %w", err)
		}
		if imagehost.ExtractHost(hosted) != capability.Host || !s.importedImageMatches(ctx, hosted, dir, digest) {
			return results, api.NewOperationError(api.OperationFailure{
				Code:      api.OperationFailureUnknownOutcome,
				Operation: api.OperationKindImageHosting,
				Message:   "Image import completed, but its hosted content could not be verified. Verify hosted assets before retrying.",
				Recovery:  api.OperationRecoveryConfirm,
			}, &url.Error{
				Op:  "verify imported image",
				URL: hosted,
				Err: errors.New("hosted content could not be verified"),
			})
		}
		link := api.UploadedImageLink{
			SourcePath:               meta.MediaBinding.SourcePath,
			PreparedMediaFingerprint: meta.MediaBinding.PreparedMediaFingerprint,
			PreparedGeneration:       meta.MediaBinding.PreparedGeneration,
			ImagePath:                image.Path,
			Purpose:                  image.Purpose,
			DiscID:                   image.DiscID,
			ContentSHA256:            digest,
			Tonemapping:              imageevidence.Tonemapping(image.Path, image.ContentSHA256, image.Tonemapping),
			Host:                     capability.Host,
			UsageScope:               capability.UsageScope,
			AccountScope:             account,
			ImgURL:                   hosted,
			RawURL:                   hosted,
			WebURL:                   hosted,
			SizeBytes:                image.SizeBytes,
			UploadedAt:               time.Now().UTC(),
		}
		results = append(results, link)
		if s.repo != nil {
			if err := s.repo.SaveUploadedImages(ctx, meta.MediaBinding, host, []api.UploadedImageLink{link}); err != nil {
				return results, api.NewOperationError(api.OperationFailure{
					Code:      api.OperationFailureUnknownOutcome,
					Operation: api.OperationKindImageHosting,
					Message:   "Image import completed, but its result could not be retained. Verify hosted assets before retrying.",
					Recovery:  api.OperationRecoveryConfirm,
				}, err)
			}
			if _, err := syncScreenshotSlotVariants(ctx, s.repo, meta.MediaBinding, []api.UploadedImageLink{link}); err != nil {
				s.logger.Warnf("image hosting: imported slot association unavailable host=%s state=failed", host)
			}
		}
	}
	s.logger.Infof("image hosting: image import completed host=%s tracker=%s state=completed count=%d", host, tracker, len(results))
	return results, nil
}

// importImageURL shares the live-test mutation guard with ordinary uploads.
// Journaled native outputs remain retained when the provider has no cleanup API.
func (s *Service) importImageURL(ctx context.Context, capability *trackers.ImageURLImportCapability, credential, source, imagePath string) (string, error) {
	perform := func() ([]uploadResult, error) {
		hosted, err := capability.Import(ctx, s.client, credential, source)
		if err != nil {
			return nil, fmt.Errorf("native image import: %w", err)
		}
		return []uploadResult{{
			ImgURL: hosted,
			RawURL: hosted,
			WebURL: hosted,
		}}, nil
	}
	var results []uploadResult
	var err error
	if s.liveImageJournal != nil {
		guard := &liveTestUploader{
			provider:  capability.Host,
			journal:   s.liveImageJournal,
			maxImages: s.liveImageLimit,
		}
		results, err = guard.upload(ctx, []string{imagePath}, perform)
	} else {
		results, err = perform()
	}
	if err != nil {
		return "", fmt.Errorf("image URL import: %w", err)
	}
	return results[0].RawURL, nil
}

func (s *Service) importedImageMatches(ctx context.Context, rawURL, dir, digest string) bool {
	file, err := os.CreateTemp(dir, "image-import-*")
	if err != nil {
		return false
	}
	name := file.Name()
	_ = file.Close()
	defer os.Remove(name)
	if err := trackers.DownloadValidatedImage(ctx, nil, rawURL, name); err != nil {
		return false
	}
	actual, err := imageevidence.Digest(name)
	return err == nil && actual == digest
}
