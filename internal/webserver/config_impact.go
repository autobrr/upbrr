// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

// configImpacts classifies every effective Config root into the narrowest
// persisted workflow dependency family. DBPath is host-owned and normalized
// before this function is called, so it is intentionally not an impact.
func configImpacts(previous, next config.Config) []api.ConfigImpact {
	details := configImpactDetails(previous, next)
	impacts := make([]api.ConfigImpact, 0, len(details))
	for _, detail := range details {
		if !slices.Contains(impacts, detail.Kind) {
			impacts = append(impacts, detail.Kind)
		}
	}
	return impacts
}

// ConfigImpactDetails classifies an effective configuration change for the
// durable activation transaction shared by the settings UI and CLI imports.
func ConfigImpactDetails(previous, next config.Config) []api.ConfigImpactDetail {
	return configImpactDetails(previous, next)
}

func configImpactDetails(previous, next config.Config) []api.ConfigImpactDetail {
	impacts := make([]api.ConfigImpact, 0, 8)
	add := func(impact api.ConfigImpact) {
		if !slices.Contains(impacts, impact) {
			impacts = append(impacts, impact)
		}
	}

	if previous.MainSettings.TMDBAPI != next.MainSettings.TMDBAPI ||
		previous.MainSettings.SceneDetection != next.MainSettings.SceneDetection ||
		!configValueEqual(previous.Metadata, next.Metadata) ||
		!configValueEqual(previous.ArrIntegration, next.ArrIntegration) {
		add(api.ConfigImpactProvider)
	}
	if previous.MainSettings.TrackerPassChecks != next.MainSettings.TrackerPassChecks ||
		!configValueEqual(previous.TorrentCreation, next.TorrentCreation) {
		add(api.ConfigImpactTrackers)
	}
	if !configValueEqual(previous.Description, next.Description) {
		add(api.ConfigImpactDescription)
	}
	if previous.ScreenshotHandling.Screens != next.ScreenshotHandling.Screens ||
		previous.ScreenshotHandling.CutoffScreens != next.ScreenshotHandling.CutoffScreens {
		add(api.ConfigImpactScreenshotSelection)
	}
	previousCapture := previous.ScreenshotHandling
	nextCapture := next.ScreenshotHandling
	previousCapture.Screens = 0
	previousCapture.CutoffScreens = 0
	nextCapture.Screens = 0
	nextCapture.CutoffScreens = 0
	if !configValueEqual(previousCapture, nextCapture) {
		add(api.ConfigImpactScreenshotCapture)
	}
	if !configValueEqual(previous.ImageHosting, next.ImageHosting) || trackerImageSettingsChanged(previous.Trackers.Trackers, next.Trackers.Trackers) {
		add(api.ConfigImpactImageHosting)
	}
	if !configValueEqual(previous.ClientSetup, next.ClientSetup) ||
		!configValueEqual(previous.TorrentClients, next.TorrentClients) ||
		!configValueEqual(previous.PostUpload, next.PostUpload) {
		add(api.ConfigImpactClientInjection)
	}
	if previous.MainSettings.UpdateNotification != next.MainSettings.UpdateNotification ||
		previous.MainSettings.VerboseNotification != next.MainSettings.VerboseNotification ||
		previous.MainSettings.InputHistoryLimit != next.MainSettings.InputHistoryLimit ||
		previous.MainSettings.UseFavicons != next.MainSettings.UseFavicons ||
		previous.MainSettings.FaviconOnly != next.MainSettings.FaviconOnly ||
		!configValueEqual(previous.Logging, next.Logging) {
		add(api.ConfigImpactPresentation)
	}
	result := make([]api.ConfigImpactDetail, 0, len(impacts)+1)
	for _, impact := range impacts {
		result = append(result, api.ConfigImpactDetail{Kind: impact})
	}
	if !configValueEqual(previous.Trackers, next.Trackers) {
		changed := changedTrackerImpactIDs(previous.Trackers, next.Trackers)
		result = append(result, api.ConfigImpactDetail{Kind: api.ConfigImpactTrackers, TrackerIDs: changed})
	}
	return result
}

func changedTrackerImpactIDs(previous, next config.TrackersConfig) []api.TrackerID {
	if !configValueEqual(previous.DefaultTrackers, next.DefaultTrackers) || previous.PreferredTracker != next.PreferredTracker {
		return nil
	}
	ids := make([]api.TrackerID, 0, len(previous.Trackers)+len(next.Trackers))
	for name, oldConfig := range previous.Trackers {
		newConfig, ok := next.Trackers[name]
		if !ok || !configValueEqual(oldConfig, newConfig) {
			ids = append(ids, api.TrackerID(name))
		}
	}
	for name := range next.Trackers {
		if _, ok := previous.Trackers[name]; !ok {
			ids = append(ids, api.TrackerID(name))
		}
	}
	return ids
}

func trackerImageSettingsChanged(previous, next map[string]config.TrackerConfig) bool {
	if len(previous) != len(next) {
		return true
	}
	for name, oldCfg := range previous {
		newCfg, ok := next[name]
		if !ok || oldCfg.ImageHost != newCfg.ImageHost || oldCfg.ImgRehost != newCfg.ImgRehost || oldCfg.ImgAPI != newCfg.ImgAPI {
			return true
		}
	}
	return false
}

func configValueEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}
