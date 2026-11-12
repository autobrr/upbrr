// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"fmt"
	"maps"

	"github.com/autobrr/upbrr/pkg/api"
)

// EffectiveConfigFingerprint identifies settings that can affect a running
// workflow, excluding host plumbing and retained compatibility-only values.
func EffectiveConfigFingerprint(cfg Config) (api.WorkflowFingerprint, error) {
	return LegacyEffectiveConfigFingerprint(WorkflowConfigForComparison(cfg))
}

// LegacyEffectiveConfigFingerprint reproduces the former DBPath-only comparison
// contract. Startup may use an exact match to prove a fingerprint-format change;
// new workflow identities must use EffectiveConfigFingerprint instead.
func LegacyEffectiveConfigFingerprint(cfg Config) (api.WorkflowFingerprint, error) {
	cfg.MainSettings.DBPath = ""
	fingerprint, err := api.CanonicalWorkflowFingerprint(cfg)
	if err != nil {
		return "", fmt.Errorf("canonical effective config fingerprint: %w", err)
	}
	return fingerprint, nil
}

// WorkflowConfigForComparison returns a comparison-only view without currently
// inactive compatibility controls. Runtime and persistence keep the original
// configuration. Maps whose entries are changed are cloned; other nested values
// remain shared and must be treated as read-only.
func WorkflowConfigForComparison(cfg Config) Config {
	cfg.MainSettings.DBPath = ""
	cfg.MainSettings.UpdateNotification = false
	cfg.MainSettings.VerboseNotification = false
	cfg.MainSettings.TrackerPassChecks = 0
	cfg.Metadata.UserOverrides = false
	cfg.Metadata.PingUnit3D = false
	cfg.Metadata.CheckPredb = false
	cfg.ScreenshotHandling.CutoffScreens = 0
	cfg.Description.MultiScreens = 0
	cfg.Description.PackThumbSize = 0
	cfg.Description.CharLimit = 0
	cfg.Description.FileLimit = 0
	cfg.Description.ProcessLimit = 0
	cfg.ArrIntegration.EmbyDir = ""
	cfg.ArrIntegration.EmbyTVDir = ""
	cfg.TorrentCreation.MkbrrThreads = 0
	cfg.PostUpload.ShowUploadDuration = false
	cfg.PostUpload.PrintTrackerMessages = false
	cfg.PostUpload.PrintTrackerLinks = false
	cfg.PostUpload.SearchRequests = false
	cfg.PostUpload.CrossSeeding = false
	cfg.PostUpload.CrossSeedCheckEverything = false
	cfg.TorrentClients = maps.Clone(cfg.TorrentClients)
	for name, client := range cfg.TorrentClients {
		client.StorageDir = ""
		cfg.TorrentClients[name] = client
	}
	cfg.Trackers.Trackers = maps.Clone(cfg.Trackers.Trackers)
	for name, tracker := range cfg.Trackers.Trackers {
		cfg.Trackers.Trackers[name] = TrackerConfigForComparison(tracker)
	}
	return cfg
}

// TrackerConfigForComparison removes named compatibility controls with no feature
// consumers. Active attribution controls and unknown extension values are retained.
// When a retained feature gains a runtime consumer, its setting must participate
// in comparisons again.
func TrackerConfigForComparison(cfg TrackerConfig) TrackerConfig {
	cfg.CheckForRules = false
	cfg.CheckRequests = false
	cfg.FullMediainfo = false
	cfg.UseSpanishTitle = false
	cfg.UseItalianTitle = false
	cfg.UseMetadataName = false
	cfg.PTGenAPI = ""
	cfg.LoginQuestion = ""
	cfg.LoginAnswer = ""
	cfg.UserID = ""
	cfg.Internal = false
	return cfg
}
