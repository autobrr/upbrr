// SPDX-License-Identifier: GPL-2.0-or-later

package config

import "testing"

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
