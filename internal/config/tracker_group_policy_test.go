// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"encoding/json"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestTrackerGroupListsNormalizeYAMLScalarAndSequence(t *testing.T) {
	var cfg struct {
		Trackers TrackersConfig `yaml:"trackers"`
	}
	err := yaml.Unmarshal([]byte(`
trackers:
  NBL:
    api_key: key
    dupe_bypass_groups: " NTb, -GRP, ntb, --Keep "
    personal_release_groups:
      - " -Mine "
      - mine
    internal_groups: " Internal, -Internal "
`), &cfg)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	tracker := cfg.Trackers.Trackers["NBL"]
	if !slices.Equal(tracker.DupeBypassGroups, CSVList{"NTb", "GRP", "-Keep"}) {
		t.Fatalf("DupeBypassGroups = %#v", tracker.DupeBypassGroups)
	}
	if !slices.Equal(tracker.PersonalReleaseGroups, CSVList{"Mine"}) {
		t.Fatalf("PersonalReleaseGroups = %#v", tracker.PersonalReleaseGroups)
	}
	if !slices.Equal(tracker.InternalGroups, CSVList{"Internal"}) {
		t.Fatalf("InternalGroups = %#v", tracker.InternalGroups)
	}
}

func TestTrackerGroupListsNormalizeJSONAndRoundTripLegacyInternal(t *testing.T) {
	var trackers TrackersConfig
	err := json.Unmarshal([]byte(`{
  "Trackers": {
    "NBL": {
      "APIKey": "key",
      "Internal": true,
      "DupeBypassGroups": ["-NTb", "ntb"],
      "PersonalReleaseGroups": ["Mine"],
      "InternalGroups": ["-Internal"]
    }
  }
}`), &trackers)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	tracker := trackers.Trackers["NBL"]
	if !tracker.Internal || !slices.Equal(tracker.DupeBypassGroups, CSVList{"NTb"}) ||
		!slices.Equal(tracker.PersonalReleaseGroups, CSVList{"Mine"}) ||
		!slices.Equal(tracker.InternalGroups, CSVList{"Internal"}) {
		t.Fatalf("tracker config = %#v", tracker)
	}

	payload, err := json.Marshal(trackers)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode marshaled config: %v", err)
	}
	decodedTrackers, ok := decoded["Trackers"].(map[string]any)
	if !ok {
		t.Fatalf("Trackers was not serialized as an object: %#v", decoded["Trackers"])
	}
	entry, ok := decodedTrackers["NBL"].(map[string]any)
	if !ok {
		t.Fatalf("NBL was not serialized as an object: %#v", decodedTrackers["NBL"])
	}
	if entry["Internal"] != true {
		t.Fatalf("legacy Internal field was not preserved: %#v", entry)
	}
	for _, key := range []string{"DupeBypassGroups", "PersonalReleaseGroups", "InternalGroups"} {
		if _, ok := entry[key].([]any); !ok {
			t.Fatalf("%s was not serialized as an array: %#v", key, entry[key])
		}
	}
}

func TestTrackerCatalogAddsGroupListsWithoutDeprecatedToggle(t *testing.T) {
	schemas, err := OrderedTrackerSchemas()
	if err != nil {
		t.Fatalf("OrderedTrackerSchemas() error = %v", err)
	}
	for _, schema := range schemas {
		fields := make(map[string]TrackerFieldSchema, len(schema.Fields))
		for _, field := range schema.Fields {
			fields[field.JSONKey] = field
		}
		if _, ok := fields["Internal"]; ok {
			t.Fatalf("%s exposes deprecated Internal toggle", schema.Name)
		}
		for _, key := range []string{"DupeBypassGroups", "PersonalReleaseGroups", "InternalGroups"} {
			field, ok := fields[key]
			if !ok {
				t.Fatalf("%s missing %s", schema.Name, key)
			}
			if value, ok := field.Default.([]string); !ok || len(value) != 0 {
				t.Fatalf("%s.%s default = %#v", schema.Name, key, field.Default)
			}
		}
	}
}
