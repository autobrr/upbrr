// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// ImageURLImportCapability describes a versioned, tracker-scoped URL importer.
// Import receives one already validated source URL and performs no persistence.
type ImageURLImportCapability struct {
	ID         string
	Host       string
	UsageScope string
	Import     func(context.Context, *http.Client, string, string) (string, error) `json:"-"`
}

// DefaultImageURLImport resolves an available default route without performing
// any image work. Explicit host choices, skips and failed destinations win.
func DefaultImageURLImport(registry *Registry, cfg config.Config, tracker string, overrides api.ImageHostOverrides) *ImageURLImportCapability {
	if registry == nil || overrides.SkipUpload != nil && *overrides.SkipUpload ||
		overrides.PreferredHost != nil && strings.TrimSpace(*overrides.PreferredHost) != "" {
		return nil
	}
	trackerCfg, _ := config.TrackerConfigByName(cfg.Trackers.Trackers, tracker)
	if strings.TrimSpace(trackerCfg.ImageHost) != "" || strings.TrimSpace(trackerCfg.APIKey) == "" {
		return nil
	}
	policy, ok := registry.LookupImageHostPolicy(tracker)
	if !ok || policy.URLImport == nil || policy.URLImport.Import == nil {
		return nil
	}
	allowed, err := ReusableImageHostAllowedWithRegistry(registry, cfg, tracker, policy.URLImport.Host, overrides)
	if err != nil || !allowed {
		return nil
	}
	return policy.URLImport
}

// filterNativeSlotVariants requires an exact current-account record; slots do
// not themselves retain account authority. Public/global KShare is unchanged.
func filterNativeSlotVariants(
	slots []api.ScreenshotSlot,
	records []api.UploadedImageLink,
	cfg config.Config,
	registry *Registry,
	binding api.PreparedMediaBinding,
) {
	for index := range slots {
		slot := &slots[index]
		slot.Variants = slices.DeleteFunc(slot.Variants, func(variant api.ScreenshotSlotVariant) bool {
			_, capability := ImageURLImportForScope(registry, variant.Host, variant.UsageScope)
			if capability == nil {
				return false
			}
			return !slices.ContainsFunc(records, func(link api.UploadedImageLink) bool {
				return link.SourcePath == binding.SourcePath &&
					link.PreparedMediaFingerprint == binding.PreparedMediaFingerprint &&
					link.PreparedGeneration == binding.PreparedGeneration &&
					link.ImagePath == slot.ImagePath && link.ImagePath == variant.ImagePath &&
					link.Host == variant.Host && link.UsageScope == variant.UsageScope &&
					link.RawURL == variant.RawURL && link.ImgURL == variant.ImgURL &&
					link.WebURL == variant.WebURL &&
					NativeImageLinkCurrent(cfg, registry, link)
			})
		})
	}
}

// ImageURLImportForScope resolves the exact registered destination and usage scope.
func ImageURLImportForScope(registry *Registry, host, scope string) (string, *ImageURLImportCapability) {
	if registry == nil {
		return "", nil
	}
	for _, name := range registry.Names() {
		policy, ok := registry.LookupImageHostPolicy(name)
		if ok && policy.URLImport != nil && strings.EqualFold(host, policy.URLImport.Host) && scope == policy.URLImport.UsageScope {
			return name, policy.URLImport
		}
	}
	return "", nil
}

// ImageHostAccountScope fingerprints backend account authority without publishing credentials.
// Public/global links retain the established generic account fingerprint.
func ImageHostAccountScope(cfg config.Config, registry *Registry, host, scope string) (string, error) {
	var value any
	if tracker, capability := ImageURLImportForScope(registry, host, scope); capability != nil {
		trackerCfg, _ := config.TrackerConfigByName(cfg.Trackers.Trackers, tracker)
		value = struct{ Version, Capability, Host, Scope, Credential string }{
			Version:    "native-image-account-v1",
			Capability: capability.ID,
			Host:       capability.Host,
			Scope:      capability.UsageScope,
			Credential: trackerCfg.APIKey,
		}
	} else {
		value = struct {
			Version string
			Host    string
			Config  config.ImageHostingConfig
		}{
			Version: "media-host-account-v1",
			Host:    strings.ToLower(strings.TrimSpace(host)),
			Config:  cfg.ImageHosting,
		}
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(value)
	if err != nil {
		return "", fmt.Errorf("image host account fingerprint: %w", err)
	}
	return string(fingerprint), nil
}

// NativeImageLinkCurrent rejects missing or stale account authority on native results.
// Ordinary public links keep their existing reuse rules.
func NativeImageLinkCurrent(cfg config.Config, registry *Registry, link api.UploadedImageLink) bool {
	_, capability := ImageURLImportForScope(registry, link.Host, link.UsageScope)
	if capability == nil {
		return true
	}
	scope, err := ImageHostAccountScope(cfg, registry, link.Host, link.UsageScope)
	return err == nil && link.AccountScope != "" && link.AccountScope == scope
}

// DownloadValidatedImage reuses the public-address, redirect, size and image-type
// boundary used for imported description screenshots. The caller owns outPath.
func DownloadValidatedImage(ctx context.Context, client *http.Client, rawURL, outPath string) error {
	if client == nil {
		client = newDescriptionSlotImageHTTPClient()
	}
	return downloadDescriptionSlotImage(ctx, client, rawURL, outPath)
}
