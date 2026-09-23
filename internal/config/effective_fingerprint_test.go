// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"encoding/json"
	"reflect"
	"testing"
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

func TestRetiredA4KConfigKeepsItsPreviousFingerprintShape(t *testing.T) {
	trackers := TrackersConfig{Trackers: map[string]TrackerConfig{
		"A4K": {
			APIKey:  "synthetic-key",
			Anon:    true,
			Unknown: map[string]any{"RetainedOption": "value"},
		},
	}}
	payload, err := json.Marshal(trackers)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Trackers map[string]map[string]any `json:"Trackers"`
	}
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"LinkDirName":           "",
		"APIKey":                "synthetic-key",
		"ImageHost":             "",
		"Anon":                  true,
		"ModQ":                  false,
		"FaviconURL":            "",
		"TorrentClient":         "",
		"Internal":              false,
		"DupeBypassGroups":      []any{},
		"PersonalReleaseGroups": []any{},
		"InternalGroups":        []any{},
		"RetainedOption":        "value",
	}
	if got := stored.Trackers["A4K"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("retired A4K fingerprint shape changed: got %#v, want %#v", got, want)
	}
}
