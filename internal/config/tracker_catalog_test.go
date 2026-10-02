// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package config

import (
	"slices"
	"testing"
)

func TestOrderedTrackerSchemasPreserveExampleOrderAndActivation(t *testing.T) {
	t.Parallel()

	schemas, err := OrderedTrackerSchemas()
	if err != nil {
		t.Fatalf("OrderedTrackerSchemas() error = %v", err)
	}
	if len(schemas) == 0 {
		t.Fatal("OrderedTrackerSchemas() returned no schemas")
	}
	if schemas[0].Name != "ACM" {
		t.Fatalf("first schema = %q, want ACM", schemas[0].Name)
	}
	if slices.ContainsFunc(schemas, func(schema TrackerSchema) bool { return schema.Name == "MANUAL" }) {
		t.Fatal("catalog contains removed MANUAL entry")
	}
	for _, schema := range schemas {
		activationCount := 0
		for _, field := range schema.Fields {
			if field.Activation {
				activationCount++
			}
			if field.YAMLKey == "url" {
				t.Fatalf("schema %s contains deprecated url field", schema.Name)
			}
		}
		if activationCount == 0 {
			t.Fatalf("schema %s has no activation fields", schema.Name)
		}
	}
}

func TestTrackerConfiguredUsesOnlyActivationFields(t *testing.T) {
	t.Parallel()

	schema := TrackerSchema{
		Name: "EXAMPLE",
		Fields: []TrackerFieldSchema{
			{
				JSONKey:    "APIKey",
				YAMLKey:    "api_key",
				Default:    "",
				Activation: true,
			},
			{
				JSONKey: "ImageHost",
				YAMLKey: "image_host",
				Default: "",
			},
		},
	}
	if TrackerConfigured(TrackerConfig{ImageHost: "imgbox"}, schema) {
		t.Fatal("optional image host marked tracker configured")
	}
	if !TrackerConfigured(TrackerConfig{APIKey: "configured"}, schema) {
		t.Fatal("non-empty activation field did not mark tracker configured")
	}
	if !TrackerConfigured(TrackerConfig{APIKey: "********"}, schema) {
		t.Fatal("redacted activation field did not remain configured")
	}

	partialSchema := TrackerSchema{
		Name: "PARTIAL",
		Fields: []TrackerFieldSchema{
			{
				JSONKey:    "Username",
				YAMLKey:    "username",
				Default:    "",
				Activation: true,
			},
			{
				JSONKey:    "Password",
				YAMLKey:    "password",
				Default:    "",
				Activation: true,
			},
		},
	}
	if !TrackerConfigured(TrackerConfig{Username: "configured", Password: ""}, partialSchema) {
		t.Fatal("one non-empty activation field should mark tracker configured before readiness")
	}
}

func TestRemovedTHRIsAbsentFromCatalog(t *testing.T) {
	t.Parallel()

	schemas, err := OrderedTrackerSchemas()
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(schemas, func(schema TrackerSchema) bool { return schema.Name == "THR" }) {
		t.Fatal("obsolete THR settings must not be advertised")
	}
}

func TestLegacyTHRSecretsRemainProtected(t *testing.T) {
	t.Parallel()

	legacy := TrackerConfig{
		PronfoAPIKey: "synthetic-pronfo-key",
		PronfoRAPIID: "synthetic-pronfo-id",
		PronfoTheme:  "legacy-theme",
	}
	cfg := &Config{Trackers: TrackersConfig{Trackers: map[string]TrackerConfig{"THR": legacy}}}
	encrypted, err := encryptConfigSecretsWithHelper(cfg, "synthetic-helper")
	if err != nil {
		t.Fatal(err)
	}
	stored := encrypted.Trackers.Trackers["THR"]
	if !isSecretEnvelope(stored.PronfoAPIKey) || !isSecretEnvelope(stored.PronfoRAPIID) {
		t.Fatal("legacy THR secrets must remain encrypted")
	}
	decrypted, err := decryptConfigSecretsWithHelper(encrypted, "synthetic-helper")
	if err != nil {
		t.Fatal(err)
	}
	restored := decrypted.Trackers.Trackers["THR"]
	if restored.PronfoAPIKey != legacy.PronfoAPIKey || restored.PronfoRAPIID != legacy.PronfoRAPIID || restored.PronfoTheme != legacy.PronfoTheme {
		t.Fatal("legacy THR config did not survive the protected round trip")
	}
}
