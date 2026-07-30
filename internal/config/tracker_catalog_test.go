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
	if schemas[0].Name != "A4K" {
		t.Fatalf("first schema = %q, want A4K", schemas[0].Name)
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
JSONKey: "APIKey",
 YAMLKey: "api_key",
 Default: "",
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
JSONKey: "Username",
 YAMLKey: "username",
 Default: "",
 Activation: true,
},
			{
JSONKey: "Password",
 YAMLKey: "password",
 Default: "",
 Activation: true,
},
		},
	}
	if !TrackerConfigured(TrackerConfig{Username: "configured", Password: ""}, partialSchema) {
		t.Fatal("one non-empty activation field should mark tracker configured before readiness")
	}
}
