// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
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
	if _, exists := definition.Properties["authorizedRuleFingerprint"]; exists {
		t.Fatal("projection instructions expose server rule authorization")
	}
}

func TestWorkflowPatchStringSchemaPreservesAbsentNullAndString(t *testing.T) {
	t.Parallel()

	builder := buildContractSchemaBuilder()
	definition := builder.schemas["WorkflowPatchstring"]
	if definition == nil || renderTypeScript(definition, 0) != "string | null" {
		t.Fatalf("workflow patch string schema = %#v", definition)
	}
	projection := builder.schemas["ReleaseWorkflowUploadTrackerProjection"]
	if projection == nil || projection.Properties["uploadReleaseName"] == nil ||
		projection.Properties["uploadReleaseName"].Ref != "#/components/schemas/WorkflowPatchstring" {
		t.Fatalf("upload release name projection schema = %#v", projection)
	}
	generated := string(generateTypeScript(builder.schemas))
	if !strings.Contains(generated, "export type WorkflowPatchstring = string | null;") {
		t.Fatal("generated TypeScript does not preserve WorkflowPatch[string] null semantics")
	}
}

func TestLegacyUploadApprovalSchemasAreDeprecated(t *testing.T) {
	t.Parallel()

	builder := buildContractSchemaBuilder()
	for _, name := range []string{"UploadApproval", "ReleaseWorkflowUploadApproval"} {
		if definition := builder.schemas[name]; definition == nil || !definition.Deprecated {
			t.Fatalf("legacy schema %s deprecation = %#v", name, definition)
		}
	}
	for schemaName, propertyName := range map[string]string{
		"ContinueReleaseWorkflowRequest":        "approval",
		"ReleaseWorkflowUploadFeedbackResponse": "uploadApproval",
	} {
		definition := builder.schemas[schemaName]
		if definition == nil || definition.Properties[propertyName] == nil ||
			!definition.Properties[propertyName].Deprecated {
			t.Fatalf("legacy property %s.%s deprecation = %#v", schemaName, propertyName, definition)
		}
	}
	generated := string(generateTypeScript(builder.schemas))
	for _, declaration := range []string{
		"/** @deprecated */\nexport type UploadApproval",
		"/** @deprecated */\nexport type ReleaseWorkflowUploadApproval",
		"/** @deprecated */\n  approval?: UploadApproval",
		"/** @deprecated */\n  uploadApproval?: ReleaseWorkflowUploadApproval",
	} {
		if !strings.Contains(generated, declaration) {
			t.Fatalf("generated TypeScript missing %q", declaration)
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

	paths := buildPaths(routeManifest(), buildContractSchemaBuilder())
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

func TestOpenAPIRoutesUseProjectedBodiesAndAccurateResponses(t *testing.T) {
	t.Parallel()

	builder := buildContractSchemaBuilder()
	paths := buildPaths(routeManifest(), builder)
	continuation := operationAt(t, paths, "/continuations", "post")
	continuationHeaders := routeHeaderNames(t, continuation)
	if _, ok := continuationHeaders["If-Match"]; ok {
		t.Fatal("continuation must not advertise If-Match")
	}
	if _, ok := continuationHeaders["Idempotency-Key"]; !ok {
		t.Fatal("continuation must advertise Idempotency-Key")
	}
	continuationSchema := requestSchemaAt(t, continuation)
	if _, ok := continuationSchema.Properties["idempotencyKey"]; ok {
		t.Fatal("continuation body must omit transport-injected idempotencyKey")
	}
	if _, ok := continuationSchema.Properties["authority"]; !ok {
		t.Fatal("continuation body must retain optional workflow authority")
	}
	continuationBody := requireType[map[string]any](t, continuation["requestBody"])
	continuationContent := requireType[map[string]any](t, continuationBody["content"])
	continuationMediaType := requireType[map[string]any](t, continuationContent["application/json"])
	examples := requireType[map[string]any](t, continuationMediaType["examples"])
	for _, name := range []string{"startWorkflow", "continueWorkflow"} {
		if _, ok := examples[name]; !ok {
			t.Fatalf("continuation examples missing %s", name)
		}
	}
	assertResponseSchemaRef(t, continuation, "200", "ReleaseWorkflowCurrent")
	assertResponseSchemaRef(t, continuation, "202", "ReleaseWorkflowCurrent")

	selectMedia := operationAt(t, paths, "/workflows/{workflowId}/media/{mediaId}/selection", "put")
	selectSchema := requestSchemaAt(t, selectMedia)
	for _, field := range []string{"workflowId", "expectedRevision", "idempotencyKey"} {
		if _, ok := selectSchema.Properties[field]; ok {
			t.Fatalf("standard mutation body retained transport field %s", field)
		}
		if _, ok := builder.schemas["SetReleaseWorkflowMediaSelectionRequest"].Properties[field]; !ok {
			t.Fatalf("public request projection mutated canonical schema field %s", field)
		}
	}
	mediaSchema := selectSchema.Properties["media"]
	if mediaSchema == nil || mediaSchema.Ref != "" {
		t.Fatalf("route-bound media schema was not projected: %#v", mediaSchema)
	}
	if slices.Contains(mediaSchema.Required, "id") {
		t.Fatal("route-bound media ID must be optional in the projected body")
	}
	if !strings.Contains(mediaSchema.Properties["id"].Description, "{mediaId}") {
		t.Fatalf("route-bound media ID description = %q", mediaSchema.Properties["id"].Description)
	}

	for _, requestPath := range []string{
		"/workflows/{workflowId}/media/{mediaId}/images/upload",
		"/workflows/{workflowId}/media/{mediaId}/images/retry",
		"/workflows/{workflowId}/uploads/{resultId}/retry",
	} {
		assertResponseSchemaRef(t, operationAt(t, paths, requestPath, "post"), "202", "ReleaseWorkflowCurrent")
	}
	assertResponseSchemaRef(
		t,
		operationAt(t, paths, "/workflows/{workflowId}/operations/{operationId}", "get"),
		"200",
		"Operation",
	)
	assertResponseSchemaRef(
		t,
		operationAt(t, paths, "/workflows/{workflowId}/operations/{operationId}/cancel", "post"),
		"200",
		"Operation",
	)

	artifact := operationAt(t, paths, "/workflows/{workflowId}/media/{mediaId}/artifacts/{artifactId}", "get")
	revision := parameterAt(t, artifact, "query", "revision")
	if revision["required"] != true || revision["example"] != 1 {
		t.Fatalf("media revision parameter = %#v", revision)
	}
	revisionSchema := requireType[map[string]any](t, revision["schema"])
	if revisionSchema["minimum"] != 1 {
		t.Fatalf("media revision schema = %#v", revisionSchema)
	}
}

func TestOpenAPIRoutesUseReusableAccurateErrorsAndPresentationMetadata(t *testing.T) {
	t.Parallel()

	paths := buildPaths(routeManifest(), buildContractSchemaBuilder())
	for _, item := range routeManifest() {
		operation := operationAt(t, paths, item.Path, strings.ToLower(item.Method))
		if operation["summary"] == "" || operation["description"] == "" {
			t.Fatalf("%s %s lacks presentation metadata", item.Method, item.Path)
		}
		tags := requireType[[]string](t, operation["tags"])
		if len(tags) != 1 || tags[0] == "" {
			t.Fatalf("%s %s tags = %#v", item.Method, item.Path, tags)
		}
		responses := requireType[map[string]any](t, operation["responses"])
		for _, success := range item.Success {
			response := requireType[map[string]any](t, responses[success.Status])
			_, hasHeaders := response["headers"]
			if success.ETag != hasHeaders {
				t.Fatalf("%s %s response %s ETag metadata = %t, want %t", item.Method, item.Path, success.Status, hasHeaders, success.ETag)
			}
		}
		if _, ok := responses["422"]; ok {
			t.Fatalf("%s %s retained unsupported 422", item.Method, item.Path)
		}
		for status, raw := range responses {
			if status[0] == '2' {
				continue
			}
			response := requireType[map[string]any](t, raw)
			ref := requireType[string](t, response["$ref"])
			if !strings.HasPrefix(ref, "#/components/responses/") {
				t.Fatalf("%s %s response %s ref = %q", item.Method, item.Path, status, ref)
			}
		}
		for _, status := range []string{"401", "403", "429", "500"} {
			if _, ok := responses[status]; !ok {
				t.Fatalf("%s %s missing authenticated error %s", item.Method, item.Path, status)
			}
		}
	}

	mutationResponses := requireType[map[string]any](
		t,
		operationAt(t, paths, "/workflows/{workflowId}/cancel", "post")["responses"],
	)
	for _, status := range []string{"413", "428"} {
		if _, ok := mutationResponses[status]; !ok {
			t.Fatalf("JSON mutation missing response %s", status)
		}
	}
	continuationResponses := requireType[map[string]any](t, operationAt(t, paths, "/continuations", "post")["responses"])
	if _, ok := continuationResponses["413"]; !ok {
		t.Fatal("continuation missing payload-too-large response")
	}
	if _, ok := continuationResponses["428"]; ok {
		t.Fatal("continuation must not advertise If-Match precondition errors")
	}
}

func TestOpenAPIErrorSchemaExamplesAndBearerGuidance(t *testing.T) {
	t.Parallel()

	builder := buildContractSchemaBuilder()
	apiError := builder.schemas["APIErrorResponse"]
	if apiError == nil || !slices.Contains(apiError.Required, "error") {
		t.Fatalf("APIErrorResponse schema = %#v", apiError)
	}
	if slices.Contains(apiError.Required, "failure") || apiError.Properties["failure"] == nil {
		t.Fatal("APIErrorResponse failure must be present and optional")
	}
	for name, raw := range errorResponseComponents() {
		component := requireType[map[string]any](t, raw)
		content := requireType[map[string]any](t, component["content"])
		mediaType := requireType[map[string]any](t, content["application/json"])
		encoded, err := json.Marshal(mediaType["example"])
		if err != nil {
			t.Fatalf("marshal %s example: %v", name, err)
		}
		lower := strings.ToLower(string(encoded))
		for _, forbidden := range []string{"bearer ", "api_token", "api_key", "cookie"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s example contains secret-shaped key material", name)
			}
		}
	}

	openAPI, _, err := generate()
	if err != nil {
		t.Fatalf("generate contracts: %v", err)
	}
	if !bytes.Contains(openAPI, []byte("Settings → API Tokens")) ||
		!bytes.Contains(openAPI, []byte("workflow:read")) ||
		!bytes.Contains(openAPI, []byte("example-command-001")) {
		t.Fatal("generated OpenAPI lacks bearer or parameter guidance")
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	t.Parallel()

	firstOpenAPI, firstTypeScript, err := generate()
	if err != nil {
		t.Fatalf("first generation: %v", err)
	}
	secondOpenAPI, secondTypeScript, err := generate()
	if err != nil {
		t.Fatalf("second generation: %v", err)
	}
	if !bytes.Equal(firstOpenAPI, secondOpenAPI) || !bytes.Equal(firstTypeScript, secondTypeScript) {
		t.Fatal("repeated generation was not byte-identical")
	}
}

func operationAt(t *testing.T, paths map[string]any, requestPath string, method string) map[string]any {
	t.Helper()
	pathItem := requireType[map[string]any](t, paths[requestPath])
	return requireType[map[string]any](t, pathItem[method])
}

func requestSchemaAt(t *testing.T, operation map[string]any) *schema {
	t.Helper()
	requestBody := requireType[map[string]any](t, operation["requestBody"])
	content := requireType[map[string]any](t, requestBody["content"])
	mediaType := requireType[map[string]any](t, content["application/json"])
	return requireType[*schema](t, mediaType["schema"])
}

func parameterAt(t *testing.T, operation map[string]any, location string, name string) map[string]any {
	t.Helper()
	for _, raw := range requireType[[]any](t, operation["parameters"]) {
		parameter := requireType[map[string]any](t, raw)
		if parameter["in"] == location && parameter["name"] == name {
			return parameter
		}
	}
	t.Fatalf("missing %s parameter %s", location, name)
	return nil
}

func assertResponseSchemaRef(t *testing.T, operation map[string]any, status string, want string) {
	t.Helper()
	responses := requireType[map[string]any](t, operation["responses"])
	response := requireType[map[string]any](t, responses[status])
	content := requireType[map[string]any](t, response["content"])
	mediaType := requireType[map[string]any](t, content["application/json"])
	responseSchema := requireType[map[string]any](t, mediaType["schema"])
	if responseSchema["$ref"] != "#/components/schemas/"+want {
		t.Fatalf("response %s schema = %#v", status, responseSchema)
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

func TestCompositeUploadRoutesAndSchemasAreGenerated(t *testing.T) {
	t.Parallel()

	builder := buildContractSchemaBuilder()
	paths := buildPaths(routeManifest(), builder)
	create := operationAt(t, paths, "/uploads", "post")
	feedback := operationAt(t, paths, "/uploads/{workflowId}/feedback", "post")
	for name, operation := range map[string]map[string]any{
		"create":   create,
		"feedback": feedback,
	} {
		responses := requireType[map[string]any](t, operation["responses"])
		if _, ok := responses["200"]; !ok {
			t.Fatalf("%s composite route lacks 200 response", name)
		}
		if _, ok := responses["202"]; !ok {
			t.Fatalf("%s composite route lacks 202 response", name)
		}
	}
	createBody := requireType[map[string]any](t, create["requestBody"])
	createContent := requireType[map[string]any](t, createBody["content"])
	createJSON := requireType[map[string]any](t, createContent["application/json"])
	examples := requireType[map[string]any](t, createJSON["examples"])
	for _, name := range []string{"strictUnattended", "unattendedConfirm", "debugNoSeed", "duplicateAllowlist"} {
		if _, ok := examples[name]; !ok {
			t.Fatalf("composite create examples missing %s", name)
		}
	}
	requestSchema := builder.schemas["CreateReleaseWorkflowUploadRequest"]
	if requestSchema == nil || !slices.Contains(requestSchema.Required, "source") ||
		!slices.Contains(requestSchema.Required, "unattended") {
		t.Fatalf("composite request required fields = %#v", requestSchema)
	}
	kindSchema := builder.schemas["ReleaseWorkflowUploadFeedbackKind"]
	if kindSchema == nil || len(kindSchema.Enum) != 13 {
		t.Fatalf("composite feedback enum = %#v", kindSchema)
	}
}
