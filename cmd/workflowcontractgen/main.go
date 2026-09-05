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
	"maps"
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
	Description          string             `json:"description,omitempty"`
	Deprecated           bool               `json:"deprecated,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	AnyOf                []*schema          `json:"anyOf,omitempty"`
	Properties           map[string]*schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *schema            `json:"items,omitempty"`
	AdditionalProperties *schema            `json:"additionalProperties,omitempty"`
}

type successResponse struct {
	Status      string
	Description string
	Response    reflect.Type
	Binary      bool
	ETag        bool
}

type requestExample struct {
	Summary string
	Value   any
}

type errorProfile string

const (
	errorProfileAuthenticatedRead     errorProfile = "authenticated-read"
	errorProfileRevisionedRead        errorProfile = "revisioned-read"
	errorProfileJSONMutation          errorProfile = "json-mutation"
	errorProfileMultipartMutation     errorProfile = "multipart-mutation"
	errorProfileContinuation          errorProfile = "continuation"
	errorProfileUploadCreate          errorProfile = "upload-create"
	errorProfileOperationCancellation errorProfile = "operation-cancellation"
)

type route struct {
	Path            string
	Method          string
	OperationID     string
	Tag             string
	Summary         string
	Description     string
	Request         reflect.Type
	RequestExamples map[string]requestExample
	Success         []successResponse
	Errors          errorProfile
	Multipart       bool
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
	builder := buildContractSchemaBuilder()

	document := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":       "upbrr Release Workflow API",
			"version":     api.ReleaseWorkflowAPIVersion,
			"description": "Owner-scoped, revisioned release workflow commands and retained state.",
		},
		"servers":  []any{map[string]any{"url": "/api/v1"}},
		"security": []any{map[string]any{"bearerAuth": []any{}}},
		"tags": []any{
			map[string]any{"name": "Descriptions", "description": "Description override management."},
			map[string]any{"name": "Media", "description": "Media planning, previews, artifacts, selection, and image hosting."},
			map[string]any{"name": "Operations", "description": "Durable long-running operation status and cancellation."},
			map[string]any{"name": "Uploads", "description": "Tracker upload retry operations."},
			map[string]any{"name": "Workflow", "description": "Workflow continuation, state, invalidation, and cancellation."},
		},
		"paths": buildPaths(routeManifest(), builder),
		"components": map[string]any{
			"securitySchemes": map[string]any{
				//nolint:gosec // Public bearer-scheme guidance; no credential is embedded.
				"bearerAuth": map[string]any{
					"type":         "http",
					"scheme":       "bearer",
					"description":  "Create a scoped token under Settings → API Tokens. Available scopes: workflow:read, workflow:write, workflow:execute.",
					"bearerFormat": "upbrr API token",
				},
			},
			"schemas":   builder.schemas,
			"responses": errorResponseComponents(),
		},
	}
	openAPI, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal OpenAPI: %w", err)
	}
	openAPI = append(openAPI, '\n')
	return openAPI, generateTypeScript(builder.schemas), nil
}

func buildContractSchemaBuilder() *schemaBuilder {
	builder := newSchemaBuilder()
	for _, root := range contractRoots() {
		builder.ensureNamed(root)
	}
	for _, item := range routeManifest() {
		if item.Request != nil {
			builder.ensureNamed(item.Request)
		}
		for _, response := range item.Success {
			if response.Response != nil {
				builder.ensureNamed(response.Response)
			}
		}
	}
	builder.ensureNamed(reflect.TypeFor[api.OperationFailure]())
	builder.schemas["APIErrorResponse"] = apiErrorResponseSchema()
	markDeprecatedSchema(builder.schemas["UploadApproval"])
	markDeprecatedSchema(builder.schemas["ReleaseWorkflowUploadApproval"])
	markDeprecatedProperty(builder.schemas["ContinueReleaseWorkflowRequest"], "approval")
	markDeprecatedProperty(builder.schemas["ReleaseWorkflowUploadFeedbackResponse"], "uploadApproval")
	return builder
}

func markDeprecatedSchema(value *schema) {
	if value != nil {
		value.Deprecated = true
	}
}

func markDeprecatedProperty(value *schema, property string) {
	if value != nil && value.Properties[property] != nil {
		value.Properties[property].Deprecated = true
	}
}

func contractRoots() []reflect.Type {
	values := []any{
		api.CreateReleaseWorkflowUploadRequest{},
		api.ReleaseWorkflowUploadFeedback{},
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
		api.RetryReleaseWorkflowClientInjectionRequest{},
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
			Path:        "/capabilities",
			Method:      http.MethodGet,
			OperationID: "getReleaseWorkflowCapabilities",
			Tag:         "Operations",
			Summary:     "Get authenticated integration capabilities",
			Description: "Returns token owner/scopes, supported features, upload schema compatibility, and non-secret configured resource choices.",
			Success: jsonSuccess(
				"200",
				"Authenticated non-secret integration capability metadata.",
				reflect.TypeFor[api.ReleaseWorkflowCapabilities](),
				false,
			),
			Errors: errorProfileAuthenticatedRead,
		},
		{
			Path:        "/uploads",
			Method:      http.MethodPost,
			OperationID: "createWorkflowUpload",
			Tag:         "Uploads",
			Summary:     "Start a composite upload",
			Description: "Creates one owner-scoped workflow and drives it toward an upload or debug dry-run behind one durable operation.",
			Request:     reflect.TypeFor[api.CreateReleaseWorkflowUploadRequest](),
			RequestExamples: map[string]requestExample{
				"strictUnattended": {
					Summary: "Strict unattended upload",
					Value: map[string]any{
						"source":     map[string]any{"path": `D:\Example Release 2026`},
						"unattended": map[string]any{"confirm": false},
						"execution":  map[string]any{"mode": "upload"},
						"trackers":   map[string]any{"include": []string{"EXAMPLE"}},
					},
				},
				"unattendedConfirm": {
					Summary: "Upload requiring feedback before execution",
					Value: map[string]any{
						"source":     map[string]any{"path": `D:\Example Release 2026`},
						"unattended": map[string]any{"confirm": true},
						"duplicates": map[string]any{"onEvidence": "ask"},
					},
				},
				"debugNoSeed": {
					Summary: "Debug dry-run without client injection",
					Value: map[string]any{
						"source":     map[string]any{"path": `D:\Example Release 2026`},
						"unattended": map[string]any{"confirm": false},
						"execution":  map[string]any{"mode": "debug"},
						"trackers":   map[string]any{"include": []string{"EXAMPLE"}},
						"client":     map[string]any{"noSeed": true},
					},
				},
				"duplicateAllowlist": {
					Summary: "Explicit duplicate-evidence upload authority",
					Value: map[string]any{
						"source":     map[string]any{"path": `D:\Example Release 2026`},
						"unattended": map[string]any{"confirm": false},
						"trackers":   map[string]any{"include": []string{"EXAMPLE"}},
						"duplicates": map[string]any{
							"onEvidence":  "block",
							"allowUpload": []string{"EXAMPLE"},
						},
					},
				},
			},
			Success: []successResponse{
				{
					Status:      "200",
					Description: "An idempotent replay returned an already terminal composite upload.",
					Response:    current,
					ETag:        true,
				},
				{
					Status:      "202",
					Description: "Composite upload accepted with a non-terminal operation attached.",
					Response:    current,
					ETag:        true,
				},
			},
			Errors: errorProfileUploadCreate,
		},
		{
			Path:        "/uploads/{workflowId}/feedback",
			Method:      http.MethodPost,
			OperationID: "submitWorkflowUploadFeedback",
			Tag:         "Uploads",
			Summary:     "Submit composite upload feedback",
			Description: "Resolves one exact pending action and starts one new composite operation when more work can proceed.",
			Request:     reflect.TypeFor[api.ReleaseWorkflowUploadFeedback](),
			RequestExamples: map[string]requestExample{
				"duplicateReview": {
					Summary: "Allow upload despite duplicate evidence",
					Value: map[string]any{
						"action": map[string]any{
							"id":               "action-example",
							"workflowRevision": 14,
						},
						"response": map[string]any{
							"kind": "duplicateReview",
							"duplicateReview": map[string]any{
								"trackerId": "EXAMPLE",
								"decision":  "ignored",
							},
						},
					},
				},
				"trackerApproval": {
					Summary: "Approve exact post-dupe trackers",
					Value: map[string]any{
						"action": map[string]any{
							"id":               "action-example",
							"workflowRevision": 14,
						},
						"response": map[string]any{
							"kind": "trackerApproval",
							"trackerApproval": map[string]any{
								"confirmed":  true,
								"trackerIds": []string{"EXAMPLE"},
							},
						},
					},
				},
			},
			Success: []successResponse{
				{
					Status:      "200",
					Description: "Feedback replay returned an already terminal composite upload.",
					Response:    current,
					ETag:        true,
				},
				{
					Status:      "202",
					Description: "Feedback accepted and composite processing resumed or remains blocked for another action.",
					Response:    current,
					ETag:        true,
				},
			},
			Errors: errorProfileJSONMutation,
		},
		{
			Path:        "/continuations",
			Method:      http.MethodPost,
			OperationID: "continueWorkflow",
			Tag:         "Workflow",
			Summary:     "Continue a workflow",
			Description: "Starts a workflow or advances an existing owner-scoped workflow toward the requested goal.",
			Request:     reflect.TypeFor[api.ContinueReleaseWorkflowRequest](),
			RequestExamples: map[string]requestExample{
				"startWorkflow": {
					Summary: "Start and prepare a workflow",
					Value: map[string]any{
						"goal": "prepared",
						"intent": map[string]any{
							"factInstructions": map[string]any{},
						},
					},
				},
				"continueWorkflow": {
					Summary: "Continue an existing workflow",
					Value: map[string]any{
						"authority": map[string]any{
							"workflowId":       "workflow-example",
							"expectedRevision": 1,
						},
						"goal": "prepared",
						"intent": map[string]any{
							"factInstructions": map[string]any{},
						},
					},
				},
			},
			Success: []successResponse{
				{
					Status:      "200",
					Description: "Workflow already reached a terminal result for the requested goal.",
					Response:    current,
					ETag:        true,
				},
				{
					Status:      "202",
					Description: "Workflow accepted with a non-terminal operation attached.",
					Response:    current,
					ETag:        true,
				},
			},
			Errors: errorProfileContinuation,
		},
		{
			Path:        "/workflows/{workflowId}",
			Method:      http.MethodGet,
			OperationID: "getWorkflow",
			Tag:         "Workflow",
			Summary:     "Get workflow state",
			Description: "Returns the authoritative owner-scoped workflow and retained stage snapshots.",
			Success:     jsonSuccess("200", "Current workflow state.", current, true),
			Errors:      errorProfileAuthenticatedRead,
		},
		{
			Path:        "/workflows/{workflowId}/trackers/invalidate",
			Method:      http.MethodPost,
			OperationID: "invalidateWorkflowTrackers",
			Tag:         "Workflow",
			Summary:     "Invalidate tracker state",
			Description: "Invalidates selected tracker projections and their dependent workflow state.",
			Request:     reflect.TypeFor[api.InvalidateReleaseWorkflowTrackersRequest](),
			Success:     jsonSuccess("200", "Workflow state after tracker invalidation.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/media/plan",
			Method:      http.MethodGet,
			OperationID: "getWorkflowMediaPlan",
			Tag:         "Media",
			Summary:     "Get media plan",
			Description: "Returns the safe media capture and artifact plan for the workflow.",
			Success:     jsonSuccess("200", "Current media plan.", reflect.TypeFor[api.MediaPlan](), false),
			Errors:      errorProfileAuthenticatedRead,
		},
		{
			Path:        "/workflows/{workflowId}/media/previews",
			Method:      http.MethodPost,
			OperationID: "previewWorkflowMediaFrame",
			Tag:         "Media",
			Summary:     "Create frame preview",
			Description: "Creates a non-authoritative frame preview at the requested timestamp.",
			Request:     reflect.TypeFor[api.PreviewReleaseWorkflowFrameRequest](),
			Success:     jsonSuccess("200", "Created frame preview metadata.", reflect.TypeFor[api.FramePreview](), false),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/media/previews/{previewId}",
			Method:      http.MethodGet,
			OperationID: "openWorkflowMediaPreview",
			Tag:         "Media",
			Summary:     "Open frame preview",
			Description: "Streams a retained preview with its runtime image content type.",
			Success:     binarySuccess("Preview image bytes. Runtime content types include PNG, JPEG, and WebP."),
			Errors:      errorProfileAuthenticatedRead,
		},
		{
			Path:        "/workflows/{workflowId}/media/resources",
			Method:      http.MethodPost,
			OperationID: "stageWorkflowMediaResource",
			Tag:         "Media",
			Summary:     "Stage media resource",
			Description: "Stages one PNG, JPEG, or WebP resource of at most 20 MiB.",
			Success:     jsonSuccess("201", "Staged workflow resource reference.", reflect.TypeFor[api.WorkflowResourceRef](), false),
			Errors:      errorProfileMultipartMutation,
			Multipart:   true,
		},
		{
			Path:        "/workflows/{workflowId}/media/attach",
			Method:      http.MethodPost,
			OperationID: "attachWorkflowMedia",
			Tag:         "Media",
			Summary:     "Attach staged media",
			Description: "Attaches staged resources to the workflow's current media lineage.",
			Request:     reflect.TypeFor[api.AttachReleaseWorkflowMediaRequest](),
			Success:     jsonSuccess("200", "Workflow state after attaching media.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/artifacts/{artifactId}",
			Method:      http.MethodGet,
			OperationID: "openWorkflowMediaArtifact",
			Tag:         "Media",
			Summary:     "Open media artifact",
			Description: "Streams an artifact from the exact requested media revision with its runtime image content type.",
			Success:     binarySuccess("Media artifact bytes. Runtime content types include PNG, JPEG, and WebP."),
			Errors:      errorProfileRevisionedRead,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/selection",
			Method:      http.MethodPut,
			OperationID: "selectWorkflowMedia",
			Tag:         "Media",
			Summary:     "Set media selection",
			Description: "Changes selected artifacts in the route-bound media set.",
			Request:     reflect.TypeFor[api.SetReleaseWorkflowMediaSelectionRequest](),
			Success:     jsonSuccess("200", "Workflow state after updating media selection.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/reorder",
			Method:      http.MethodPut,
			OperationID: "reorderWorkflowMedia",
			Tag:         "Media",
			Summary:     "Reorder media artifacts",
			Description: "Establishes the exact artifact order for the route-bound media set.",
			Request:     reflect.TypeFor[api.ReorderReleaseWorkflowMediaRequest](),
			Success:     jsonSuccess("200", "Workflow state after reordering media.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/delete",
			Method:      http.MethodPost,
			OperationID: "deleteWorkflowMedia",
			Tag:         "Media",
			Summary:     "Delete media artifacts",
			Description: "Deletes retained artifacts from the route-bound media set.",
			Request:     reflect.TypeFor[api.DeleteReleaseWorkflowMediaRequest](),
			Success:     jsonSuccess("200", "Workflow state after deleting media.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/images/upload",
			Method:      http.MethodPost,
			OperationID: "uploadWorkflowImages",
			Tag:         "Media",
			Summary:     "Upload selected images",
			Description: "Starts durable image-hosting work for selected artifacts in the route-bound media set.",
			Request:     reflect.TypeFor[api.UploadReleaseWorkflowImagesRequest](),
			Success:     jsonSuccess("202", "Workflow state with the non-terminal image-hosting operation attached.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/images/retry",
			Method:      http.MethodPost,
			OperationID: "retryWorkflowImageHost",
			Tag:         "Media",
			Summary:     "Retry image hosting",
			Description: "Starts a durable retry for a prior image-hosting failure.",
			Request:     reflect.TypeFor[api.RetryReleaseWorkflowImageHostRequest](),
			Success:     jsonSuccess("202", "Workflow state with the non-terminal image-hosting retry attached.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/media/{mediaId}/images/remove",
			Method:      http.MethodPost,
			OperationID: "removeWorkflowHostedImages",
			Tag:         "Media",
			Summary:     "Remove hosted images",
			Description: "Removes hosted outcomes for selected route-bound media artifacts.",
			Request:     reflect.TypeFor[api.RemoveReleaseWorkflowHostedImagesRequest](),
			Success:     jsonSuccess("200", "Workflow state after removing hosted images.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/descriptions/{descriptionId}/groups/{groupKey}/save",
			Method:      http.MethodPost,
			OperationID: "saveWorkflowDescriptionOverride",
			Tag:         "Descriptions",
			Summary:     "Save description override",
			Description: "Saves one route-bound description group override.",
			Request:     reflect.TypeFor[api.SaveReleaseWorkflowDescriptionOverrideRequest](),
			Success:     jsonSuccess("200", "Workflow state after saving the description override.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/descriptions/{descriptionId}/groups/{groupKey}/reset",
			Method:      http.MethodPost,
			OperationID: "resetWorkflowDescriptionOverride",
			Tag:         "Descriptions",
			Summary:     "Reset description override",
			Description: "Resets one route-bound description group to its generated value.",
			Request:     reflect.TypeFor[api.ResetReleaseWorkflowDescriptionOverrideRequest](),
			Success:     jsonSuccess("200", "Workflow state after resetting the description override.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/uploads/{resultId}/retry",
			Method:      http.MethodPost,
			OperationID: "retryWorkflowUploads",
			Tag:         "Uploads",
			Summary:     "Retry failed uploads",
			Description: "Starts durable upload retries for selected failed trackers from the route-bound prior result.",
			Request:     reflect.TypeFor[api.RetryReleaseWorkflowUploadRequest](),
			Success:     jsonSuccess("202", "Workflow state with the non-terminal upload retry attached.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/uploads/{resultId}/client-injections/retry",
			Method:      http.MethodPost,
			OperationID: "retryWorkflowClientInjections",
			Tag:         "Uploads",
			Summary:     "Retry failed client injections",
			Description: "Retries selected client injections from retained registered tracker artifacts without resubmitting tracker uploads.",
			Request:     reflect.TypeFor[api.RetryReleaseWorkflowClientInjectionRequest](),
			Success:     jsonSuccess("202", "Workflow state with the non-terminal client-injection retry attached.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/cancel",
			Method:      http.MethodPost,
			OperationID: "cancelWorkflow",
			Tag:         "Workflow",
			Summary:     "Cancel workflow",
			Description: "Cancels the current workflow using exact revision and idempotency authority.",
			Request:     reflect.TypeFor[api.CancelReleaseWorkflowRequest](),
			Success:     jsonSuccess("200", "Canceled workflow state.", current, true),
			Errors:      errorProfileJSONMutation,
		},
		{
			Path:        "/workflows/{workflowId}/operations/{operationId}",
			Method:      http.MethodGet,
			OperationID: "getWorkflowOperation",
			Tag:         "Operations",
			Summary:     "Get workflow operation",
			Description: "Returns durable status for one owner-scoped workflow operation.",
			Success:     jsonSuccess("200", "Current operation status.", operation, false),
			Errors:      errorProfileAuthenticatedRead,
		},
		{
			Path:        "/workflows/{workflowId}/operations/{operationId}/cancel",
			Method:      http.MethodPost,
			OperationID: "cancelWorkflowOperation",
			Tag:         "Operations",
			Summary:     "Cancel workflow operation",
			Description: "Requests cancellation without workflow revision or idempotency authority.",
			Success:     jsonSuccess("200", "Operation status after cancellation.", operation, false),
			Errors:      errorProfileOperationCancellation,
		},
	}
}

func jsonSuccess(status string, description string, response reflect.Type, etag bool) []successResponse {
	return []successResponse{{
		Status:      status,
		Description: description,
		Response:    response,
		ETag:        etag,
	}}
}

func binarySuccess(description string) []successResponse {
	return []successResponse{{
		Status:      "200",
		Description: description,
		Binary:      true,
	}}
}

func buildPaths(routes []route, builder *schemaBuilder) map[string]any {
	paths := map[string]any{
		"/openapi.json": map[string]any{
			"get": map[string]any{
				"operationId": "getOpenAPI",
				"tags":        []string{"Operations"},
				"summary":     "Get raw OpenAPI contract",
				"description": "Returns the generated OpenAPI 3.1 contract used by the instance-local interactive documentation.",
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
			"tags":        []string{item.Tag},
			"summary":     item.Summary,
			"description": item.Description,
			"parameters":  routeParameters(item),
			"responses":   routeResponses(item),
		}
		if item.Request != nil {
			mediaType := map[string]any{
				"schema": projectedRequestSchema(builder, item),
			}
			if len(item.RequestExamples) > 0 {
				examples := make(map[string]any, len(item.RequestExamples))
				for name, example := range item.RequestExamples {
					examples[name] = map[string]any{
						"summary": example.Summary,
						"value":   example.Value,
					}
				}
				mediaType["examples"] = examples
			}
			operation["requestBody"] = map[string]any{
				"description": requestBodyDescription(item),
				"required":    true,
				"content": map[string]any{
					"application/json": mediaType,
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
		pathItem[strings.ToLower(item.Method)] = operation
	}
	return paths
}

func routeParameters(item route) []any {
	parameters := make([]any, 0, 7)
	for segment := range strings.SplitSeq(item.Path, "/") {
		if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' {
			continue
		}
		name := segment[1 : len(segment)-1]
		description, example := pathParameterMetadata(name)
		parameters = append(parameters, map[string]any{
			"name":        name,
			"in":          "path",
			"description": description,
			"required":    true,
			"example":     example,
			"schema":      map[string]any{"type": "string"},
		})
	}
	if item.Errors == errorProfileJSONMutation || item.Errors == errorProfileMultipartMutation {
		parameters = append(parameters, map[string]any{
			"name":        "If-Match",
			"in":          "header",
			"description": "Optimistic workflow revision authority. Send the current ETag value returned by a workflow response.",
			"required":    true,
			"example":     "\"1\"",
			"schema":      map[string]any{"type": "string"},
		})
	}
	if item.Errors == errorProfileJSONMutation || item.Errors == errorProfileContinuation || item.Errors == errorProfileUploadCreate {
		parameters = append(parameters, map[string]any{
			"name":        "Idempotency-Key",
			"in":          "header",
			"description": "One stable key per logical mutation. Reuse it only when retrying that same logical request.",
			"required":    true,
			"example":     "example-command-001",
			"schema":      map[string]any{"type": "string"},
		})
	}
	if item.OperationID == "openWorkflowMediaArtifact" {
		parameters = append(parameters, map[string]any{
			"name":        "revision",
			"in":          "query",
			"description": "Exact positive revision of the route-bound media artifact set.",
			"required":    true,
			"example":     1,
			"schema": map[string]any{
				"type":    "integer",
				"minimum": 1,
			},
		})
	}
	return parameters
}

func routeResponses(item route) map[string]any {
	responses := make(map[string]any, len(item.Success)+9)
	for _, success := range item.Success {
		response := map[string]any{"description": success.Description}
		if success.Binary {
			binarySchema := map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}
			response["content"] = map[string]any{
				"application/octet-stream": binarySchema,
				"image/jpeg":               binarySchema,
				"image/png":                binarySchema,
				"image/webp":               binarySchema,
			}
		} else if success.Response != nil {
			response["content"] = map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"$ref": "#/components/schemas/" + schemaName(success.Response)},
				},
			}
		}
		if success.ETag {
			response["headers"] = map[string]any{
				"ETag": map[string]any{
					"description": "Quoted authoritative workflow revision for the next If-Match header.",
					"schema":      map[string]any{"type": "string"},
					"example":     "\"1\"",
				},
			}
		}
		responses[success.Status] = response
	}
	for _, reference := range errorReferences(item.Errors) {
		responses[reference.Status] = map[string]any{
			"$ref": "#/components/responses/" + reference.Component,
		}
	}
	return responses
}

func pathParameterMetadata(name string) (string, string) {
	switch name {
	case "workflowId":
		return "Owner-scoped workflow identifier.", "workflow-example"
	case "mediaId":
		return "Route-bound media artifact-set identifier.", "media-example"
	case "operationId":
		return "Durable workflow operation identifier.", "operation-example"
	case "previewId":
		return "Retained frame-preview identifier.", "preview-example"
	case "artifactId":
		return "Opaque public artifact identifier.", "artifact-example"
	case "descriptionId":
		return "Route-bound description-set identifier.", "description-example"
	case "groupKey":
		return "Route-bound description group key.", "overview"
	case "resultId":
		return "Route-bound prior upload-result identifier.", "result-example"
	}
	return "Opaque route identifier.", "identifier-example"
}

func requestBodyDescription(item route) string {
	if item.Errors == errorProfileUploadCreate {
		return "Idempotency-Key is supplied in the header. The body contains one typed, single-source upload specification."
	}
	if item.Errors == errorProfileContinuation {
		return "Idempotency-Key is supplied in the header. authority is optional when starting a workflow and required when continuing one."
	}
	return "Top-level workflowId, expectedRevision, and idempotencyKey are supplied by the route and headers. Route-bound nested IDs are server supplied; compatibility values remain accepted but are ignored."
}

func projectedRequestSchema(builder *schemaBuilder, item route) *schema {
	projected := cloneSchema(builder.schemas[schemaName(item.Request)])
	omitted := []string{"workflowId", "expectedRevision", "idempotencyKey"}
	if item.Errors == errorProfileContinuation || item.Errors == errorProfileUploadCreate {
		omitted = []string{"idempotencyKey"}
	}
	for _, name := range omitted {
		delete(projected.Properties, name)
		projected.Required = slices.DeleteFunc(projected.Required, func(required string) bool {
			return required == name
		})
	}
	for _, binding := range routeBodyBindings(item.OperationID) {
		projected = projectRouteBoundField(builder, projected, binding.Fields, binding.Parameter)
	}
	return projected
}

type routeBodyBinding struct {
	Fields    []string
	Parameter string
}

func routeBodyBindings(operationID string) []routeBodyBinding {
	switch operationID {
	case "selectWorkflowMedia", "reorderWorkflowMedia", "deleteWorkflowMedia", "uploadWorkflowImages",
		"retryWorkflowImageHost", "removeWorkflowHostedImages":
		return []routeBodyBinding{{Fields: []string{"media", "id"}, Parameter: "mediaId"}}
	case "saveWorkflowDescriptionOverride":
		return []routeBodyBinding{
			{Fields: []string{"override", "descriptions", "id"}, Parameter: "descriptionId"},
			{Fields: []string{"override", "groupKey"}, Parameter: "groupKey"},
		}
	case "resetWorkflowDescriptionOverride":
		return []routeBodyBinding{
			{Fields: []string{"descriptions", "id"}, Parameter: "descriptionId"},
			{Fields: []string{"groupKey"}, Parameter: "groupKey"},
		}
	case "retryWorkflowUploads":
		return []routeBodyBinding{{Fields: []string{"retry", "result", "id"}, Parameter: "resultId"}}
	case "continueWorkflow", "invalidateWorkflowTrackers", "previewWorkflowMediaFrame", "attachWorkflowMedia", "cancelWorkflow":
		return nil
	}
	return nil
}

func projectRouteBoundField(builder *schemaBuilder, value *schema, fields []string, parameter string) *schema {
	projected := cloneSchema(value)
	if projected.Ref != "" {
		name := projected.Ref[strings.LastIndex(projected.Ref, "/")+1:]
		projected = cloneSchema(builder.schemas[name])
	}
	if len(fields) == 0 || projected == nil {
		return projected
	}
	name := fields[0]
	property, ok := projected.Properties[name]
	if !ok {
		return projected
	}
	if len(fields) == 1 {
		property = cloneSchema(property)
		property.Description = "Server supplied from the {" + parameter + "} path parameter; compatibility values are accepted but ignored."
		projected.Properties[name] = property
		projected.Required = slices.DeleteFunc(projected.Required, func(required string) bool {
			return required == name
		})
		return projected
	}
	projected.Properties[name] = projectRouteBoundField(builder, property, fields[1:], parameter)
	return projected
}

func cloneSchema(value *schema) *schema {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Properties = maps.Clone(value.Properties)
	cloned.Required = slices.Clone(value.Required)
	cloned.AnyOf = slices.Clone(value.AnyOf)
	cloned.Enum = slices.Clone(value.Enum)
	return &cloned
}

type errorReference struct {
	Status    string
	Component string
}

func errorReferences(profile errorProfile) []errorReference {
	switch profile {
	case errorProfileAuthenticatedRead:
		return []errorReference{
			{Status: "401", Component: "Unauthorized"},
			{Status: "403", Component: "Forbidden"},
			{Status: "404", Component: "NotFound"},
			{Status: "429", Component: "TooManyRequests"},
			{Status: "500", Component: "InternalServerError"},
		}
	case errorProfileRevisionedRead:
		return []errorReference{
			{Status: "400", Component: "BadRequest"},
			{Status: "401", Component: "Unauthorized"},
			{Status: "403", Component: "Forbidden"},
			{Status: "404", Component: "NotFound"},
			{Status: "429", Component: "TooManyRequests"},
			{Status: "500", Component: "InternalServerError"},
		}
	case errorProfileJSONMutation:
		return []errorReference{
			{Status: "400", Component: "BadRequest"},
			{Status: "401", Component: "Unauthorized"},
			{Status: "403", Component: "Forbidden"},
			{Status: "404", Component: "NotFound"},
			{Status: "409", Component: "Conflict"},
			{Status: "413", Component: "PayloadTooLarge"},
			{Status: "428", Component: "PreconditionRequired"},
			{Status: "429", Component: "TooManyRequests"},
			{Status: "500", Component: "InternalServerError"},
		}
	case errorProfileMultipartMutation:
		return []errorReference{
			{Status: "400", Component: "BadRequest"},
			{Status: "401", Component: "Unauthorized"},
			{Status: "403", Component: "Forbidden"},
			{Status: "404", Component: "NotFound"},
			{Status: "409", Component: "Conflict"},
			{Status: "428", Component: "PreconditionRequired"},
			{Status: "429", Component: "TooManyRequests"},
			{Status: "500", Component: "InternalServerError"},
		}
	case errorProfileContinuation, errorProfileUploadCreate:
		return []errorReference{
			{Status: "400", Component: "BadRequest"},
			{Status: "401", Component: "Unauthorized"},
			{Status: "403", Component: "Forbidden"},
			{Status: "404", Component: "NotFound"},
			{Status: "409", Component: "Conflict"},
			{Status: "413", Component: "PayloadTooLarge"},
			{Status: "429", Component: "TooManyRequests"},
			{Status: "500", Component: "InternalServerError"},
		}
	case errorProfileOperationCancellation:
		return []errorReference{
			{Status: "401", Component: "Unauthorized"},
			{Status: "403", Component: "Forbidden"},
			{Status: "404", Component: "NotFound"},
			{Status: "409", Component: "Conflict"},
			{Status: "429", Component: "TooManyRequests"},
			{Status: "500", Component: "InternalServerError"},
		}
	}
	panic("unknown workflow OpenAPI error profile")
}

func apiErrorResponseSchema() *schema {
	return &schema{
		Type: "object",
		Properties: map[string]*schema{
			"error": {
				Type:        "string",
				Description: "Safe human-readable error summary.",
			},
			"failure": {
				Ref:         "#/components/schemas/OperationFailure",
				Description: "Optional structured workflow failure metadata.",
			},
		},
		Required: []string{"error"},
	}
}

func errorResponseComponents() map[string]any {
	return map[string]any{
		"BadRequest": responseComponent(
			"Invalid header, query, body, or typed request.",
			map[string]any{"error": "Invalid API request."},
		),
		"Unauthorized": responseComponent(
			"Missing, invalid, or revoked bearer token.",
			map[string]any{"error": "API token required"},
		),
		"Forbidden": responseComponent(
			"Bearer token lacks the required workflow scope.",
			map[string]any{"error": "API token scope denied"},
		),
		"NotFound": responseComponent(
			"Owner-scoped workflow or resource was not found.",
			map[string]any{"error": "Release workflow not found."},
		),
		"Conflict": responseComponent(
			"Stale revision, idempotency conflict, or blocked transition.",
			map[string]any{
				"error": "The workflow changed. Reload its current state before continuing.",
				"failure": map[string]any{
					"Code":      "stale_review",
					"Operation": "unknown",
					"Message":   "The workflow changed. Reload its current state before continuing.",
					"Recovery":  "review_again",
				},
			},
		),
		"PayloadTooLarge": responseComponent(
			"JSON request exceeds the public API limit.",
			map[string]any{"error": "Invalid API request body."},
		),
		"PreconditionRequired": responseComponent(
			"Missing or invalid If-Match workflow revision.",
			map[string]any{"error": "Valid If-Match workflow revision required."},
		),
		"TooManyRequests": responseComponent(
			"General request rate limit exceeded.",
			map[string]any{"error": "Rate limit exceeded."},
		),
		"InternalServerError": responseComponent(
			"Authentication or runtime failure hidden behind a safe message.",
			map[string]any{
				"error": "The operation could not be completed.",
				"failure": map[string]any{
					"Code":      "internal",
					"Operation": "unknown",
					"Message":   "The operation could not be completed.",
					"Recovery":  "retry",
				},
			},
		),
	}
}

func responseComponent(description string, example any) map[string]any {
	return map[string]any{
		"description": description,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema":  map[string]any{"$ref": "#/components/schemas/APIErrorResponse"},
				"example": example,
			},
		},
	}
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
			reflect.TypeFor[api.ReleaseWorkflowUploadMode]():           stringValues("upload", "debug"),
			reflect.TypeFor[api.ReleaseWorkflowPreparedReleaseMode]():  stringValues("allow", "require"),
			reflect.TypeFor[api.ReleaseWorkflowDuplicateDisposition](): stringValues("ask", "block", "upload"),
			reflect.TypeFor[api.ReleaseWorkflowUploadFeedbackKind](): stringValues(
				"playlistSelection",
				"metadataSelection",
				"rescanConfirmation",
				"trackerAuthentication",
				"twoFactor",
				"trackerInput",
				"questionnaire",
				"ruleAuthorization",
				"trackerPreparation",
				"duplicateReview",
				"trackerApproval",
				"uploadApproval",
				"reprepare",
				"reconciliation",
			),
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
	if value == reflect.TypeFor[api.WorkflowPatch[string]]() {
		return &schema{AnyOf: []*schema{{Type: "string"}, {Type: "null"}}}
	}
	if value == reflect.TypeFor[api.CreateReleaseWorkflowUploadRequest]() {
		result := &schema{Type: "object", Properties: map[string]*schema{}}
		b.addFields(result, value)
		result.Required = append(result.Required, "unattended")
		sort.Strings(result.Required)
		return result
	}
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
		if schemas[name].Deprecated {
			output.WriteString("/** @deprecated */\n")
		}
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
			if value.Properties[name].Deprecated {
				result.WriteString(indent)
				result.WriteString("/** @deprecated */\n")
			}
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
