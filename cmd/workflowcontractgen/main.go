// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

// Command workflowcontractgen generates the release-workflow OpenAPI document
// and its WebUI transport types from pkg/api DTOs plus the public route manifest.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/autobrr/upbrr/pkg/api"
)

const (
	openAPIPath    = "internal/webserver/openapi/release-workflow-v1.json"
	typeScriptPath = "webui/src/api/generated/release-workflow.ts"
)

type schema struct {
	Ref                  string             `json:"$ref,omitempty"`
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	AnyOf                []*schema          `json:"anyOf,omitempty"`
	Properties           map[string]*schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *schema            `json:"items,omitempty"`
	AdditionalProperties *schema            `json:"additionalProperties,omitempty"`
}

type route struct {
	Path        string
	Method      string
	OperationID string
	Request     reflect.Type
	Response    reflect.Type
	Status      string
	LongRunning bool
	Binary      bool
	Multipart   bool
}

func main() {
	check := flag.Bool("check", false, "fail when generated workflow contracts differ")
	flag.Parse()

	openAPI, typeScript, err := generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	outputs := []struct {
		path string
		data []byte
	}{
		{path: openAPIPath, data: openAPI},
		{path: typeScriptPath, data: typeScript},
	}
	for _, output := range outputs {
		if *check {
			current, readErr := os.ReadFile(output.path)
			if readErr != nil {
				err = errors.Join(err, fmt.Errorf("read %s: %w", output.path, readErr))
				continue
			}
			if !bytes.Equal(current, output.data) {
				err = errors.Join(err, fmt.Errorf("%s is stale; run make workflow-contracts", output.path))
			}
			continue
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(output.path), 0o755); mkdirErr != nil {
			err = errors.Join(err, fmt.Errorf("create %s: %w", filepath.Dir(output.path), mkdirErr))
			continue
		}
		if writeErr := os.WriteFile(output.path, output.data, 0o600); writeErr != nil {
			err = errors.Join(err, fmt.Errorf("write %s: %w", output.path, writeErr))
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() ([]byte, []byte, error) {
	builder := newSchemaBuilder()
	for _, root := range contractRoots() {
		builder.ensureNamed(root)
	}
	for _, item := range routeManifest() {
		if item.Request != nil {
			builder.ensureNamed(item.Request)
		}
		if item.Response != nil {
			builder.ensureNamed(item.Response)
		}
	}

	document := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "upbrr Release Workflow API",
			"version":     "1.0.0",
			"description": "Owner-scoped, revisioned release workflow commands and retained state.",
		},
		"servers":  []any{map[string]any{"url": "/api/v1"}},
		"security": []any{map[string]any{"bearerAuth": []any{}}},
		"paths":    buildPaths(routeManifest()),
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"},
			},
			"schemas": builder.schemas,
		},
	}
	openAPI, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal OpenAPI: %w", err)
	}
	openAPI = append(openAPI, '\n')
	return openAPI, generateTypeScript(builder.schemas), nil
}

func contractRoots() []reflect.Type {
	values := []any{
		api.ContinueReleaseWorkflowRequest{},
		api.GetReleaseWorkflowRequest{},
		api.ReleaseWorkflowOperationRequest{},
		api.GetReleaseWorkflowMediaPlanRequest{},
		api.PreviewReleaseWorkflowFrameRequest{},
		api.SetReleaseWorkflowMediaSelectionRequest{},
		api.DeleteReleaseWorkflowMediaRequest{},
		api.ReorderReleaseWorkflowMediaRequest{},
		api.AttachReleaseWorkflowMediaRequest{},
		api.UploadReleaseWorkflowImagesRequest{},
		api.RemoveReleaseWorkflowHostedImagesRequest{},
		api.RetryReleaseWorkflowImageHostRequest{},
		api.SaveReleaseWorkflowDescriptionOverrideRequest{},
		api.ResetReleaseWorkflowDescriptionOverrideRequest{},
		api.RetryReleaseWorkflowUploadRequest{},
		api.CancelReleaseWorkflowRequest{},
		api.InvalidateReleaseWorkflowTrackersRequest{},
		api.ReleaseWorkflowCurrent{},
		api.WorkflowOperationStatus{},
		api.MediaPlan{},
		api.FramePreview{},
		api.HostedImageAttempt{},
		api.UploadDryRunResult{},
	}
	types := make([]reflect.Type, 0, len(values))
	for _, value := range values {
		types = append(types, reflect.TypeOf(value))
	}
	return types
}

func routeManifest() []route {
	current := reflect.TypeFor[api.ReleaseWorkflowCurrent]()
	operation := reflect.TypeFor[api.WorkflowOperationStatus]()
	return []route{
		{
			Path:        "/continuations",
			Method:      "post",
			OperationID: "continueWorkflow",
			Request:     reflect.TypeFor[api.ContinueReleaseWorkflowRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}",
			Method:      "get",
			OperationID: "getWorkflow",
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/trackers/invalidate",
			Method:      "post",
			OperationID: "invalidateWorkflowTrackers",
			Request:     reflect.TypeFor[api.InvalidateReleaseWorkflowTrackersRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/media/plan",
			Method:      "get",
			OperationID: "getWorkflowMediaPlan",
			Response:    reflect.TypeFor[api.MediaPlan](),
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/media/previews",
			Method:      "post",
			OperationID: "previewWorkflowMediaFrame",
			Request:     reflect.TypeFor[api.PreviewReleaseWorkflowFrameRequest](),
			Response:    reflect.TypeFor[api.FramePreview](),
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/media/previews/{previewId}",
			Method:      "get",
			OperationID: "openWorkflowMediaPreview",
			Status:      "200",
			Binary:      true,
		},
		{
			Path:        "/workflows/{workflowId}/media/resources",
			Method:      "post",
			OperationID: "stageWorkflowMediaResource",
			Response:    reflect.TypeFor[api.WorkflowResourceRef](),
			Status:      "201",
			Multipart:   true,
		},
		{
			Path:        "/workflows/{workflowId}/media/attach",
			Method:      "post",
			OperationID: "attachWorkflowMedia",
			Request:     reflect.TypeFor[api.AttachReleaseWorkflowMediaRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/artifacts/{artifactId}",
			Method:      "get",
			OperationID: "openWorkflowMediaArtifact",
			Status:      "200",
			Binary:      true,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/selection",
			Method:      "put",
			OperationID: "selectWorkflowMedia",
			Request:     reflect.TypeFor[api.SetReleaseWorkflowMediaSelectionRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/reorder",
			Method:      "put",
			OperationID: "reorderWorkflowMedia",
			Request:     reflect.TypeFor[api.ReorderReleaseWorkflowMediaRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/delete",
			Method:      "post",
			OperationID: "deleteWorkflowMedia",
			Request:     reflect.TypeFor[api.DeleteReleaseWorkflowMediaRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/images/upload",
			Method:      "post",
			OperationID: "uploadWorkflowImages",
			Request:     reflect.TypeFor[api.UploadReleaseWorkflowImagesRequest](),
			Response:    operation,
			Status:      "202",
			LongRunning: true,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/images/retry",
			Method:      "post",
			OperationID: "retryWorkflowImageHost",
			Request:     reflect.TypeFor[api.RetryReleaseWorkflowImageHostRequest](),
			Response:    operation,
			Status:      "202",
			LongRunning: true,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/images/remove",
			Method:      "post",
			OperationID: "removeWorkflowHostedImages",
			Request:     reflect.TypeFor[api.RemoveReleaseWorkflowHostedImagesRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/descriptions/{descriptionId}/groups/{groupKey}/save",
			Method:      "post",
			OperationID: "saveWorkflowDescriptionOverride",
			Request:     reflect.TypeFor[api.SaveReleaseWorkflowDescriptionOverrideRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/descriptions/{descriptionId}/groups/{groupKey}/reset",
			Method:      "post",
			OperationID: "resetWorkflowDescriptionOverride",
			Request:     reflect.TypeFor[api.ResetReleaseWorkflowDescriptionOverrideRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/uploads/{resultId}/retry",
			Method:      "post",
			OperationID: "retryWorkflowUploads",
			Request:     reflect.TypeFor[api.RetryReleaseWorkflowUploadRequest](),
			Response:    operation,
			Status:      "202",
			LongRunning: true,
		},
		{
			Path:        "/workflows/{workflowId}/cancel",
			Method:      "post",
			OperationID: "cancelWorkflow",
			Request:     reflect.TypeFor[api.CancelReleaseWorkflowRequest](),
			Response:    current,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/operations/{operationId}",
			Method:      "get",
			OperationID: "getWorkflowOperation",
			Response:    operation,
			Status:      "200",
		},
		{
			Path:        "/workflows/{workflowId}/operations/{operationId}/cancel",
			Method:      "post",
			OperationID: "cancelWorkflowOperation",
			Response:    operation,
			Status:      "200",
		},
	}
}

func buildPaths(routes []route) map[string]any {
	paths := map[string]any{
		"/openapi.json": map[string]any{
			"get": map[string]any{
				"operationId": "getOpenAPI",
				"security":    []any{},
				"responses": map[string]any{
					"200": map[string]any{"description": "OpenAPI document"},
				},
			},
		},
	}
	for _, item := range routes {
		operation := map[string]any{
			"operationId": item.OperationID,
			"parameters":  routeParameters(item),
			"responses":   routeResponses(item),
		}
		if item.Request != nil {
			operation["requestBody"] = map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/" + schemaName(item.Request)},
					},
				},
			}
		} else if item.Multipart {
			operation["requestBody"] = map[string]any{
				"required": true,
				"content": map[string]any{
					"multipart/form-data": map[string]any{
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"file": map[string]any{"type": "string", "format": "binary"},
							},
							"required": []string{"file"},
						},
					},
				},
			}
		}
		pathItem, ok := paths[item.Path].(map[string]any)
		if !ok {
			pathItem = map[string]any{}
			paths[item.Path] = pathItem
		}
		pathItem[item.Method] = operation
	}
	return paths
}

func routeParameters(item route) []any {
	parameters := make([]any, 0, 6)
	for segment := range strings.SplitSeq(item.Path, "/") {
		if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' {
			continue
		}
		parameters = append(parameters, map[string]any{
			"name":     segment[1 : len(segment)-1],
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		})
	}
	if item.Method != http.MethodGet {
		operationCancel := strings.HasSuffix(item.Path, "/operations/{operationId}/cancel")
		if item.Path != "/workflows" && !operationCancel {
			parameters = append(parameters, map[string]any{
				"name":     "If-Match",
				"in":       "header",
				"required": true,
				"schema":   map[string]any{"type": "string"},
			})
		}
		if !item.Multipart && !operationCancel {
			parameters = append(parameters, map[string]any{
				"name":     "Idempotency-Key",
				"in":       "header",
				"required": true,
				"schema":   map[string]any{"type": "string"},
			})
		}
	}
	return parameters
}

func routeResponses(item route) map[string]any {
	response := map[string]any{"description": "Successful response"}
	if item.Binary {
		response["content"] = map[string]any{"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}
	} else if item.Response != nil {
		response["content"] = map[string]any{
			"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/" + schemaName(item.Response)}},
		}
	}
	responses := map[string]any{item.Status: response}
	for _, status := range []string{"400", "401", "403", "404", "409", "422"} {
		responses[status] = map[string]any{"description": "Structured operation failure"}
	}
	return responses
}

type schemaBuilder struct {
	schemas  map[string]*schema
	owners   map[string]reflect.Type
	visiting map[reflect.Type]bool
	enums    map[reflect.Type][]any
}

func newSchemaBuilder() *schemaBuilder {
	return &schemaBuilder{
		schemas:  make(map[string]*schema),
		owners:   make(map[string]reflect.Type),
		visiting: make(map[reflect.Type]bool),
		enums: map[reflect.Type][]any{
			reflect.TypeFor[api.WorkflowStatus](): stringValues("draft", "active", "blocked", "completed", "canceled", "failed"),
			reflect.TypeFor[api.StageStatus](): stringValues(
				"pending",
				"queued",
				"ready",
				"blocked",
				"stale",
				"failed",
				"partial",
				"skipped",
				"running",
				"completed",
				"executed",
				"interrupted",
				"canceled",
				"unavailable",
			),
			reflect.TypeFor[api.ReadinessStatus]():       stringValues("unknown", "ready", "blocked", "ineligible", "stale"),
			reflect.TypeFor[api.RequiredActionStatus]():  stringValues("pending", "resolved", "expired"),
			reflect.TypeFor[api.WorkflowExecutionMode](): stringValues("normal", "debug"),
		},
	}
}

func stringValues(values ...string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func (b *schemaBuilder) ensureNamed(value reflect.Type) {
	value = indirect(value)
	if value.Name() == "" || value.PkgPath() == "" || isSpecial(value) {
		return
	}
	name := schemaName(value)
	if owner, exists := b.owners[name]; exists && owner != value {
		panic(fmt.Sprintf("workflow schema name collision: %s and %s", owner, value))
	}
	if _, exists := b.schemas[name]; exists || b.visiting[value] {
		return
	}
	b.owners[name] = value
	b.visiting[value] = true
	b.schemas[name] = b.definition(value)
	delete(b.visiting, value)
}

func (b *schemaBuilder) definition(value reflect.Type) *schema {
	if value == reflect.TypeFor[api.TrackerProjectionInstructions]() {
		stringOrNull := func() *schema {
			return &schema{AnyOf: []*schema{{Type: "string"}, {Type: "null"}}}
		}
		return &schema{
			Type: "object",
			Properties: map[string]*schema{
				"uploadReleaseName": stringOrNull(),
				"additionalNames": {
					Type:                 "object",
					AdditionalProperties: stringOrNull(),
				},
				"questionnaire": {
					Type:                 "object",
					AdditionalProperties: stringOrNull(),
				},
				"trackerConfig": b.inline(reflect.TypeFor[api.TrackerConfigOverrides]()),
				"trackerSite":   b.inline(reflect.TypeFor[api.TrackerSiteOverrides]()),
			},
		}
	}
	if enum := b.enums[value]; len(enum) > 0 {
		return &schema{Type: primitiveType(value.Kind()), Enum: enum}
	}
	switch value.Kind() {
	case reflect.Struct:
		result := &schema{Type: "object", Properties: map[string]*schema{}}
		b.addFields(result, value)
		sort.Strings(result.Required)
		return result
	case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		return &schema{Type: primitiveType(value.Kind())}
	case reflect.Invalid, reflect.Complex64, reflect.Complex128, reflect.Array, reflect.Chan, reflect.Func, reflect.Interface,
		reflect.Map, reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return b.inline(value)
	default:
		return b.inline(value)
	}
}

func (b *schemaBuilder) addFields(result *schema, value reflect.Type) {
	for field := range value.Fields() {
		if field.PkgPath != "" {
			continue
		}
		jsonName, options := parseJSONTag(field.Tag.Get("json"))
		if jsonName == "-" {
			continue
		}
		if field.Anonymous && jsonName == "" && indirect(field.Type).Kind() == reflect.Struct {
			b.addFields(result, indirect(field.Type))
			continue
		}
		if jsonName == "" {
			jsonName = field.Name
		}
		fieldSchema := b.inline(field.Type)
		if field.Tag.Get("ts_type") == "string" {
			fieldSchema = &schema{Type: "string"}
		}
		result.Properties[jsonName] = fieldSchema
		if !options["omitempty"] && field.Type.Kind() != reflect.Pointer {
			result.Required = append(result.Required, jsonName)
		}
	}
}

func (b *schemaBuilder) inline(value reflect.Type) *schema {
	if value.Kind() == reflect.Pointer {
		return &schema{AnyOf: []*schema{b.inline(value.Elem()), {Type: "null"}}}
	}
	value = indirect(value)
	if isSpecial(value) {
		if value.PkgPath() == "time" && value.Name() == "Time" {
			return &schema{Type: "string", Format: "date-time"}
		}
		return &schema{}
	}
	if value.Name() != "" && value.PkgPath() != "" {
		b.ensureNamed(value)
		return &schema{Ref: "#/components/schemas/" + schemaName(value)}
	}
	switch value.Kind() {
	case reflect.Struct:
		result := &schema{Type: "object", Properties: map[string]*schema{}}
		b.addFields(result, value)
		sort.Strings(result.Required)
		return result
	case reflect.Slice, reflect.Array:
		if value.Elem().Kind() == reflect.Uint8 {
			return &schema{Type: "string", Format: "byte"}
		}
		return &schema{Type: "array", Items: b.inline(value.Elem())}
	case reflect.Map:
		return &schema{Type: "object", AdditionalProperties: b.inline(value.Elem())}
	case reflect.Interface:
		return &schema{}
	case reflect.Invalid, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func, reflect.Pointer, reflect.String, reflect.UnsafePointer:
		return &schema{Type: primitiveType(value.Kind())}
	default:
		return &schema{Type: primitiveType(value.Kind())}
	}
}

func schemaName(value reflect.Type) string {
	value = indirect(value)
	if value == reflect.TypeFor[api.WorkflowOperationStatus]() {
		return "Operation"
	}
	name := value.Name()
	var result strings.Builder
	for _, character := range name {
		if unicode.IsLetter(character) || unicode.IsDigit(character) || character == '_' {
			result.WriteRune(character)
		}
	}
	return result.String()
}

func indirect(value reflect.Type) reflect.Type {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value
}

func isSpecial(value reflect.Type) bool {
	return value.PkgPath() == "time" && value.Name() == "Time" || value.PkgPath() == "encoding/json" && value.Name() == "RawMessage"
}

func primitiveType(kind reflect.Kind) string {
	switch kind {
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Invalid, reflect.Complex64, reflect.Complex128, reflect.Array, reflect.Chan, reflect.Func, reflect.Interface,
		reflect.Map, reflect.Pointer, reflect.Slice, reflect.String, reflect.Struct, reflect.UnsafePointer:
		return "string"
	default:
		return "string"
	}
}

func parseJSONTag(tag string) (string, map[string]bool) {
	parts := strings.Split(tag, ",")
	options := make(map[string]bool, len(parts))
	for _, option := range parts[1:] {
		options[option] = true
	}
	return parts[0], options
}

func generateTypeScript(schemas map[string]*schema) []byte {
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	var output strings.Builder
	output.WriteString("// Copyright (c) 2025-2026, Audionut and the autobrr contributors.\n")
	output.WriteString("// SPDX-License-Identifier: GPL-2.0-or-later\n\n")
	output.WriteString("// Code generated by cmd/workflowcontractgen. DO NOT EDIT.\n\n")
	for _, name := range names {
		output.WriteString("export type ")
		output.WriteString(name)
		output.WriteString(" = ")
		output.WriteString(renderTypeScript(schemas[name], 0))
		output.WriteString(";\n\n")
	}
	return []byte(output.String())
}

func renderTypeScript(value *schema, depth int) string {
	if value == nil {
		return "unknown"
	}
	if value.Ref != "" {
		parts := strings.Split(value.Ref, "/")
		return parts[len(parts)-1]
	}
	if len(value.AnyOf) > 0 {
		items := make([]string, 0, len(value.AnyOf))
		for _, item := range value.AnyOf {
			rendered := renderTypeScript(item, depth)
			if !slices.Contains(items, rendered) {
				items = append(items, rendered)
			}
		}
		return strings.Join(items, " | ")
	}
	if len(value.Enum) > 0 {
		items := make([]string, len(value.Enum))
		for index, item := range value.Enum {
			encoded, err := json.Marshal(item)
			if err != nil {
				panic(fmt.Sprintf("marshal TypeScript enum value: %v", err))
			}
			items[index] = string(encoded)
		}
		return strings.Join(items, " | ")
	}
	switch value.Type {
	case "string":
		return "string"
	case "integer", "number":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	case "array":
		item := renderTypeScript(value.Items, depth)
		if strings.Contains(item, " | ") {
			item = "(" + item + ")"
		}
		return "readonly " + item + "[]"
	case "object":
		if value.AdditionalProperties != nil && len(value.Properties) == 0 {
			return "Readonly<Record<string, " + renderTypeScript(value.AdditionalProperties, depth) + ">>"
		}
		required := make(map[string]bool, len(value.Required))
		for _, name := range value.Required {
			required[name] = true
		}
		names := make([]string, 0, len(value.Properties))
		for name := range value.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		indent := strings.Repeat("  ", depth+1)
		closingIndent := strings.Repeat("  ", depth)
		var result strings.Builder
		result.WriteString("Readonly<{\n")
		for _, name := range names {
			result.WriteString(indent)
			result.WriteString(typeScriptProperty(name))
			if !required[name] {
				result.WriteByte('?')
			}
			result.WriteString(": ")
			result.WriteString(renderTypeScript(value.Properties[name], depth+1))
			result.WriteString(";\n")
		}
		result.WriteString(closingIndent)
		result.WriteString("}>")
		return result.String()
	default:
		return "unknown"
	}
}

func typeScriptProperty(name string) string {
	if name == "" {
		return strconv.Quote(name)
	}
	for index, character := range name {
		if !unicode.IsLetter(character) && character != '_' && character != '$' && (index == 0 || !unicode.IsDigit(character)) {
			return strconv.Quote(name)
		}
	}
	return name
}
