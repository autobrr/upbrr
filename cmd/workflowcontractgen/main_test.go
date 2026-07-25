// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestTrackerProjectionInstructionsSchemaPreservesTriStateFields(t *testing.T) {
	t.Parallel()

	builder := newSchemaBuilder()
	builder.ensureNamed(reflect.TypeFor[api.TrackerProjectionInstructions]())
	definition := builder.schemas["TrackerProjectionInstructions"]
	if definition == nil {
		t.Fatal("tracker projection instruction schema was not generated")
	}
	name := definition.Properties["uploadReleaseName"]
	if name == nil || renderTypeScript(name, 0) != "string | null" {
		t.Fatalf("upload release name schema = %#v", name)
	}
	for _, field := range []string{"additionalNames", "questionnaire"} {
		property := definition.Properties[field]
		if property == nil || property.AdditionalProperties == nil ||
			renderTypeScript(property.AdditionalProperties, 0) != "string | null" {
			t.Fatalf("%s schema = %#v", field, property)
		}
	}
}

func TestRouteManifestDeclaresEveryPathParameter(t *testing.T) {
	t.Parallel()

	for _, item := range routeManifest() {
		declared := make(map[string]struct{})
		for _, raw := range routeParameters(item) {
			parameter := requireType[map[string]any](t, raw)
			if parameter["in"] == "path" {
				declared[requireType[string](t, parameter["name"])] = struct{}{}
			}
		}
		for segment := range strings.SplitSeq(item.Path, "/") {
			if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' {
				continue
			}
			name := segment[1 : len(segment)-1]
			if _, ok := declared[name]; !ok {
				t.Errorf("route %s does not declare path parameter %s", item.Path, name)
			}
		}
	}
}

func TestRouteManifestMatchesSpecialMutationAuthority(t *testing.T) {
	t.Parallel()

	paths := buildPaths(routeManifest())
	stagePath := requireType[map[string]any](t, paths["/workflows/{workflowId}/media/resources"])
	stage := requireType[map[string]any](t, stagePath["post"])
	stageHeaders := routeHeaderNames(t, stage)
	if _, ok := stageHeaders["If-Match"]; !ok {
		t.Fatal("staged media route must require exact workflow revision")
	}
	if _, ok := stageHeaders["Idempotency-Key"]; ok {
		t.Fatal("staged media route must not advertise unused idempotency authority")
	}
	requestBody := requireType[map[string]any](t, stage["requestBody"])
	content := requireType[map[string]any](t, requestBody["content"])
	if _, ok := content["multipart/form-data"]; !ok {
		t.Fatal("staged media route must advertise multipart input")
	}

	cancelPath := requireType[map[string]any](t, paths["/workflows/{workflowId}/operations/{operationId}/cancel"])
	cancel := requireType[map[string]any](t, cancelPath["post"])
	cancelHeaders := routeHeaderNames(t, cancel)
	if _, ok := cancelHeaders["If-Match"]; ok {
		t.Fatal("operation cancellation must not advertise unused workflow revision")
	}
	if _, ok := cancelHeaders["Idempotency-Key"]; ok {
		t.Fatal("operation cancellation must not advertise unused idempotency authority")
	}
}

func routeHeaderNames(t *testing.T, operation map[string]any) map[string]struct{} {
	t.Helper()
	result := make(map[string]struct{})
	for _, raw := range requireType[[]any](t, operation["parameters"]) {
		parameter := requireType[map[string]any](t, raw)
		if parameter["in"] == "header" {
			result[requireType[string](t, parameter["name"])] = struct{}{}
		}
	}
	return result
}

func requireType[T any](t *testing.T, value any) T {
	t.Helper()
	typed, ok := value.(T)
	if !ok {
		t.Fatalf("value type = %T, want %T", value, *new(T))
	}
	return typed
}

func TestPointerFieldsAreOptionalAndNullable(t *testing.T) {
	t.Parallel()

	builder := newSchemaBuilder()
	builder.ensureNamed(reflect.TypeFor[api.ExternalIDOverrides]())
	definition := builder.schemas["ExternalIDOverrides"]
	providerID := definition.Properties["TMDBID"]
	if providerID == nil || renderTypeScript(providerID, 0) != "number | null" {
		t.Fatalf("provider ID schema = %#v", providerID)
	}
	for _, required := range definition.Required {
		if required == "TMDBID" {
			t.Fatal("nullable provider ID must permit omission")
		}
	}
}
