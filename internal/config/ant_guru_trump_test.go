// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import "testing"

// regression: #803
func TestANTGuruTrumpCatalogDefaultsOffWithoutActivation(t *testing.T) {
	schemas, err := OrderedTrackerSchemas()
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range schemas {
		if schema.Name != "ANT" {
			continue
		}
		for _, field := range schema.Fields {
			if field.JSONKey != "GuruTrump" {
				continue
			}
			if field.YAMLKey != "guru_trump" || field.Default != false || field.Activation {
				t.Fatalf("unexpected Guru setting schema: %#v", field)
			}
			if TrackerConfigured(TrackerConfig{GuruTrump: true}, schema) {
				t.Fatal("Guru setting must not activate an unconfigured tracker")
			}
			return
		}
	}
	t.Fatal("ANT Guru setting is missing from the shared config catalog")
}
