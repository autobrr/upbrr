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
		Trackers:       TrackersConfig{Trackers: map[string]TrackerConfig{"BJS": {Anon: true, ShowGroupIfAnon: true}, "FF": {}}},
		TorrentClients: map[string]TorrentClientConfig{"qbit": {Type: "qbit", URL: "https://client.invalid"}},
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
	client := next.TorrentClients["qbit"]
	client.StorageDir = "legacy torrent storage"
	next.TorrentClients["qbit"] = client
	tracker := next.Trackers.Trackers["FF"]
	tracker.CheckForRules, tracker.CheckRequests, tracker.FullMediainfo = true, true, true
	tracker.UseSpanishTitle, tracker.UseItalianTitle, tracker.UseMetadataName = true, true, true
	tracker.PTGenAPI, tracker.LoginQuestion, tracker.LoginAnswer, tracker.UserID = "legacy endpoint", "legacy question", "synthetic answer", "legacy user"
	tracker.Internal = true
	next.Trackers.Trackers["FF"] = tracker
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
	if !bytes.Equal(before, after) || next.TorrentClients["qbit"].StorageDir != "legacy torrent storage" || !next.Trackers.Trackers["FF"].CheckRequests {
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
