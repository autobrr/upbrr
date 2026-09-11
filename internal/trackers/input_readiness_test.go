// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type inputSchemaDefinition struct {
	stubDefinition
	options []string
}

func (d inputSchemaDefinition) InputSchema(api.UploadSubject) *api.TrackerQuestionnaire {
	return &api.TrackerQuestionnaire{Tracker: d.name, Fields: []api.TrackerQuestionnaireField{{
		Key:     "choice",
		Kind:    "select",
		Options: d.options,
	}}}
}

func TestInputReadinessPublishesSelectedSchemasAndFingerprintsChoices(t *testing.T) {
	evaluate := func(options []string) api.InputReadinessEvaluation {
		t.Helper()
		registry := NewRegistry()
		for _, name := range []string{"ONE", "TWO"} {
			if err := registry.RegisterDescriptor(Descriptor{Name: name, Definition: inputSchemaDefinition{stubDefinition: stubDefinition{name: name}, options: options}}); err != nil {
				t.Fatal(err)
			}
		}
		result, err := EvaluateInputReadiness(registry, []api.TrackerID{"ONE"}, api.UploadSubject{})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := evaluate([]string{"first", "second"})
	if len(first.Schemas) != 1 || first.Schemas[0].Tracker != "ONE" || !slices.Equal(first.Schemas[0].Fields[0].Options, []string{"first", "second"}) {
		t.Fatalf("selected schemas = %#v", first.Schemas)
	}
	if second := evaluate([]string{"first", "third"}); first.RequirementsFingerprint == second.RequirementsFingerprint {
		t.Fatal("changed choices reused the previous input schema fingerprint")
	}
}

func TestEvaluateInputReadinessNormalizesSelectedRequirements(t *testing.T) {
	t.Parallel()

	registry := newMetadataRegistry(t)
	evaluation, err := EvaluateInputReadiness(registry, []api.TrackerID{" bhd ", "BHD"}, api.UploadSubject{
		Identity: api.ExternalIdentity{
			IMDBID:   1234567,
			TMDBID:   42,
			Category: api.CanonicalCategoryMovie,
		},
	})
	if err != nil {
		t.Fatalf("evaluate readiness: %v", err)
	}
	if evaluation.RequirementsFingerprint == "" || len(evaluation.Fields) != 2 {
		t.Fatalf("evaluation=%#v", evaluation)
	}
	for _, field := range evaluation.Fields {
		if field.Status != api.InputReadinessFieldReady || len(field.TrackerIDs) != 1 || field.TrackerIDs[0] != "BHD" {
			t.Fatalf("field=%#v", field)
		}
	}
}

func TestEvaluateInputReadinessRejectsUnregisteredSelectedTrackers(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	for _, descriptor := range []Descriptor{
		{Name: "KNOWN", Definition: stubDefinition{name: "KNOWN"}},
		{Name: "SCHEMA", Definition: inputSchemaDefinition{stubDefinition: stubDefinition{name: "SCHEMA"}, options: []string{"one"}}},
	} {
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatalf("register descriptor: %v", err)
		}
	}

	tests := []struct {
		name       string
		registry   *Registry
		selected   []api.TrackerID
		wantErr    bool
		wantSchema bool
	}{
		{
			name:     "unknown only",
			registry: registry,
			selected: []api.TrackerID{"UNKNOWN"},
			wantErr:  true,
		},
		{
			name:     "known and unknown",
			registry: registry,
			selected: []api.TrackerID{"KNOWN", "UNKNOWN"},
			wantErr:  true,
		},
		{
			name:     "registered without metadata policy",
			registry: registry,
			selected: []api.TrackerID{"KNOWN"},
		},
		{
			name:       "schema only",
			registry:   registry,
			selected:   []api.TrackerID{"SCHEMA"},
			wantSchema: true,
		},
		{
			name:     "nil registry with selection",
			selected: []api.TrackerID{"KNOWN"},
			wantErr:  true,
		},
		{name: "nil registry without selection"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation, err := EvaluateInputReadiness(test.registry, test.selected, api.UploadSubject{})
			if (err != nil) != test.wantErr {
				t.Fatalf("evaluate readiness error = %v, want error = %t", err, test.wantErr)
			}
			if err != nil {
				return
			}
			if got := len(evaluation.Schemas) == 1; got != test.wantSchema {
				t.Fatalf("schemas = %#v, want schema = %t", evaluation.Schemas, test.wantSchema)
			}
		})
	}
}

func TestEvaluateInputReadinessRequiresKnownCategory(t *testing.T) {
	t.Parallel()

	evaluation, err := EvaluateInputReadiness(newMetadataRegistry(t), []api.TrackerID{"BHD"}, api.UploadSubject{Identity: api.ExternalIdentity{IMDBID: 1234567}})
	if err != nil {
		t.Fatalf("evaluate readiness: %v", err)
	}
	if len(evaluation.Fields) != 1 || evaluation.Fields[0].Key != "metadata.category" || evaluation.Fields[0].Status != api.InputReadinessFieldMissing {
		t.Fatalf("evaluation=%#v", evaluation)
	}
}

func TestEvaluateInputReadinessChecksTrackerInputWithoutCategory(t *testing.T) {
	t.Parallel()
	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name: "ONE",
		Definition: projectionInputDefinition{inputSchemaDefinition{
			name:    "ONE",
			options: []string{"valid"},
		}},
		Metadata: &TrackerMetadataPolicy{RequireKnownCategory: true},
	}); err != nil {
		t.Fatal(err)
	}
	evaluation, err := EvaluateInputReadiness(registry, []api.TrackerID{"ONE"}, api.UploadSubject{})
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluation.Fields) != 2 || evaluation.Fields[0].Key != "metadata.category" ||
		evaluation.Fields[1].Key != "tracker_input.choice" || evaluation.Fields[1].Status != api.InputReadinessFieldInvalid {
		t.Fatalf("evaluation = %#v", evaluation)
	}
}

type retainedInputSchemaDefinition struct {
	stubDefinition
	schema *api.TrackerQuestionnaire
}

func (d retainedInputSchemaDefinition) InputSchema(api.UploadSubject) *api.TrackerQuestionnaire {
	return d.schema
}

func TestInputSchemaDetachesProviderStorage(t *testing.T) {
	t.Parallel()
	schema := &api.TrackerQuestionnaire{Tracker: "ONE", Fields: []api.TrackerQuestionnaireField{{
		Key:     "choice",
		Kind:    "select",
		Options: []string{"valid"},
	}}}
	registry := NewRegistry()
	if err := registry.Register(retainedInputSchemaDefinition{name: "ONE", schema: schema}); err != nil {
		t.Fatal(err)
	}
	before, err := EvaluateInputReadiness(registry, []api.TrackerID{"ONE"}, api.UploadSubject{})
	if err != nil {
		t.Fatal(err)
	}
	detached := registry.InputSchema("ONE", api.UploadSubject{})
	detached.Tracker = "changed"
	detached.Fields[0].Key = "changed"
	detached.Fields[0].Options[0] = "changed"
	before.Schemas[0].Fields[0].Options[0] = "also changed"
	after, err := EvaluateInputReadiness(registry, []api.TrackerID{"ONE"}, api.UploadSubject{})
	if err != nil {
		t.Fatal(err)
	}
	if after.RequirementsFingerprint != before.RequirementsFingerprint || schema.Tracker != "ONE" ||
		schema.Fields[0].Key != "choice" || schema.Fields[0].Options[0] != "valid" {
		t.Fatalf("caller mutated provider schema: %#v", schema)
	}
}
