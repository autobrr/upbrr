// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"bytes"
	"maps"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEffectiveConfigFingerprintIgnoresDatabasePath(t *testing.T) {
	first := Config{MainSettings: MainSettingsConfig{TMDBAPI: "token", DBPath: "E:/state/one.db"}}
	second := first
	second.MainSettings.DBPath = "E:/state/two.db"

	firstFingerprint, err := EffectiveConfigFingerprint(first)
	if err != nil {
		t.Fatal(err)
	}
	secondFingerprint, err := EffectiveConfigFingerprint(second)
	if err != nil {
		t.Fatal(err)
	}
	if firstFingerprint != secondFingerprint {
		t.Fatalf("database path changed effective fingerprint: %s != %s", firstFingerprint, secondFingerprint)
	}
}

func TestEffectiveConfigFingerprintIgnoresCompatibilitySettingsWithoutMutatingStorage(t *testing.T) {
	base := Config{
		Trackers: TrackersConfig{Trackers: map[string]TrackerConfig{
			"BJS":  {Anon: true, ShowGroupIfAnon: true},
			"FF":   {},
			"AZ":   {},
			"DC":   {},
			"EMUW": {},
			"SHRI": {},
		}},
		TorrentClients: map[string]TorrentClientConfig{"watch": {Type: "watch"}},
	}
	next := base
	next.Trackers.Trackers = maps.Clone(base.Trackers.Trackers)
	next.TorrentClients = maps.Clone(base.TorrentClients)
	next.MainSettings.UpdateNotification = true
	next.MainSettings.VerboseNotification = true
	next.MainSettings.TrackerPassChecks = 3
	next.Metadata = MetadataConfig{
		UserOverrides: true,
		PingUnit3D:    true,
		CheckPredb:    true,
	}
	next.ScreenshotHandling.CutoffScreens = 4
	next.Description = DescriptionSettingsConfig{
		MultiScreens:  2,
		PackThumbSize: 300,
		CharLimit:     100,
		FileLimit:     3,
		ProcessLimit:  4,
	}
	next.ArrIntegration = ArrIntegrationConfig{EmbyDir: "legacy movies", EmbyTVDir: "legacy episodes"}
	next.TorrentCreation.MkbrrThreads = 3
	next.PostUpload = PostUploadConfig{
		ShowUploadDuration:       true,
		PrintTrackerMessages:     true,
		PrintTrackerLinks:        true,
		SearchRequests:           true,
		CrossSeeding:             true,
		CrossSeedCheckEverything: true,
	}
	client := next.TorrentClients["watch"]
	client.StorageDir = "legacy torrent storage"
	next.TorrentClients["watch"] = client
	next.Trackers.Trackers["FF"] = TrackerConfig{
		CheckRequests: true,
		FullMediainfo: true,
		Internal:      true,
	}
	next.Trackers.Trackers["AZ"] = TrackerConfig{CheckForRules: true}
	next.Trackers.Trackers["DC"] = TrackerConfig{UseMetadataName: true}
	next.Trackers.Trackers["EMUW"] = TrackerConfig{UseSpanishTitle: true}
	next.Trackers.Trackers["SHRI"] = TrackerConfig{UseItalianTitle: true}
	before, err := yaml.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	want, err := EffectiveConfigFingerprint(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := EffectiveConfigFingerprint(next)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("compatibility-only settings changed workflow identity")
	}
	after, err := yaml.Marshal(next)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || next.TorrentClients["watch"].StorageDir != "legacy torrent storage" || !next.Trackers.Trackers["FF"].CheckRequests {
		t.Fatal("fingerprinting changed retained configuration values")
	}
	next.Metadata.KeepImages = true
	active, err := EffectiveConfigFingerprint(next)
	if err != nil {
		t.Fatal(err)
	}
	if active == want {
		t.Fatal("active sibling change did not change workflow identity")
	}
}
