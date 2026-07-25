// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package architecturepolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRepositoryAcceptsCanonicalRepository(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "canonical.go")
	if err := os.WriteFile(path, []byte("package sample\ntype ExternalIdentity struct{}\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestCheckRepositoryRejectsCategoryAliasInNamingFacts(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "pkg", "api")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	path := filepath.Join(directory, "prepared_release.go")
	if err := os.WriteFile(path, []byte("package api\ntype NamingFacts struct { Category string }\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0].Message, "ExternalIdentity") {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestCheckRepositoryRejectsWorkflowStateInPreparationState(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "internal", "preparedrelease", "state")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	path := filepath.Join(directory, "state.go")
	if err := os.WriteFile(path, []byte("package state\ntype State struct { Trackers []string }\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0].Message, "Trackers") {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestCheckRepositoryRejectsPreparedReleaseWorkflowInput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "workflow.go")
	fixture := "package sample\ntype UploadService interface { Run(PreparedRelease) error }\ntype PreparedRelease struct{}\n"
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0].Message, "owner-local inputs") {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestCheckRepositoryRejectsMultiSourceRequestField(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "pkg/api/core.go", "package api\ntype Request struct { Paths []string }\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "single-source")
}

func TestCheckRepositoryRejectsCorrelationInCanonicalPreparationInput(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "pkg/api/prepare.go", "package api\ntype PrepareInput struct { CorrelationID string }\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "operation presentation correlation")
}

func TestCheckRepositoryRejectsDisplayConstructionOutsidePreparedRelease(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "internal/core/display.go", "package core\nvar value = api.ProviderDisplay{Name: \"example\"}\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "display construction")
}

func TestCheckRepositoryRejectsBroadRequestReconstructionInPreparedRelease(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/preparedrelease/collector.go",
		"package preparedrelease\nvar request = api.Request{}\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "broad api.Request")
}

func TestCheckRepositoryRejectsForcedUnattendedPreparation(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/preparedrelease/collector.go",
		"package preparedrelease\nvar mode = api.InteractionModeUnattended\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "caller interaction mode")
}

func TestCheckRepositoryRejectsDirectProductionClientSearch(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/core/dupe.go",
		"package core\nfunc search(client Client) { client.SearchPathedTorrents(nil, nil) }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "internal/clientdiscovery")
}

func TestCheckRepositoryRejectsTrackerNamingAlgorithmInUploadFile(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/standalone/example/upload.go",
		"package example\nfunc resolveUploadName() string { return \"example\" }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "naming algorithms belong in name.go")
}

func TestCheckRepositoryRejectsStaticBannedGroupsOutsideOwnedFile(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/standalone/example/definition.go",
		"package example\nfunc bannedGroups() []string { return []string{\"GRP\"} }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "banned-group declarations belong in banned_groups.go")
}

func TestCheckRepositoryAcceptsTrackerUploadOrchestrationCalls(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/standalone/example/upload.go",
		"package example\nfunc prepareUpload() { _ = resolveCategory() }\n",
	)
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/standalone/example/taxonomy.go",
		"package example\nfunc resolveCategory() string { return \"1\" }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestCheckRepositoryRejectsTrackerResponsibilitiesDeclaredInUpload(t *testing.T) {
	tests := []struct {
		name           string
		declaration    string
		responsibility string
	}{
		{
			name:           "auth",
			declaration:    "func resolveAuthSession() {}",
			responsibility: "auth",
		},
		{
			name:           "taxonomy",
			declaration:    "func resolveCategory() string { return \"1\" }",
			responsibility: "taxonomy",
		},
		{
			name:           "description",
			declaration:    "func buildDescription() string { return \"\" }",
			responsibility: "description",
		},
		{
			name:           "media",
			declaration:    "func readMediaInfo() string { return \"\" }",
			responsibility: "media",
		},
		{
			name:           "questionnaire",
			declaration:    "func buildQuestionnaire() {}",
			responsibility: "questionnaire",
		},
		{
			name:           "validation",
			declaration:    "func validatePayload() string { return \"\" }",
			responsibility: "validation",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writePolicyFixture(
				t,
				root,
				"internal/trackers/impl/standalone/example/upload.go",
				"package example\n"+test.declaration+"\n",
			)
			violations, err := CheckRepository(root)
			if err != nil {
				t.Fatalf("check repository: %v", err)
			}
			assertViolationContains(t, violations, "tracker "+test.responsibility+" algorithms belong in")
		})
	}
}

func TestCheckRepositoryAcceptsUnit3DCallbackInOwnedFile(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/unit3d/sites/example/profile.go",
		"package example\nvar site = SiteProfile{ResolveTypeID: resolveTypeID}\n",
	)
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/unit3d/sites/example/taxonomy.go",
		"package example\nfunc resolveTypeID() string { return \"1\" }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestCheckRepositoryRejectsUnit3DCallbackOutsideOwnedFile(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/unit3d/sites/example/profile.go",
		"package example\nfunc resolveTypeID() string { return \"1\" }\nvar site = SiteProfile{ResolveTypeID: resolveTypeID}\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "site-local taxonomy.go")
}

func TestCheckRepositoryRejectsTrackerImportingPresentationOwner(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/standalone/example/definition.go",
		"package example\nimport _ \"github.com/autobrr/upbrr/internal/webserver\"\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "cannot import CLI, server, or workflow presentation owners")
}

func TestCheckRepositoryRejectsLateTrackerNameResolutionInUploadFile(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/standalone/example/upload.go",
		"package example\nfunc build() string { return resolveUploadName() }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "must consume the reviewed release name")
}

func TestCheckRepositoryRejectsUnversionedUnit3DCustomName(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/unit3d/sites/example/profile.go",
		"package example\nvar site = unit3d.SiteProfile{BuildName: buildName}\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "require BuildNameVersion")
}

func TestCheckRepositoryRejectsTrackerPrincipalPayloadWithoutReviewedName(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/impl/standalone/example/upload.go",
		"package example\nfunc payload(meta Meta) map[string]string { return map[string]string{\"name\": meta.ReleaseName} }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "must consume PreparationInput.ReviewedUploadName")
}

func TestCheckRepositoryAcceptsOwnedClientSearch(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/clientdiscovery/discovery.go",
		"package clientdiscovery\nfunc search(client Client) { client.SearchPathedTorrents(nil, nil) }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestCheckRepositoryRejectsFrontendBDMVPathDerivation(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"webui/src/releaseSession/layout.ts",
		"const bdmv = `${sourcePath}/BDMV`;\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "derive BDMV resource paths")
}

func TestCheckRepositoryRejectsReleasePageProductionClientImport(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/pages/input/index.tsx", "import { preparationClient } from \"../../api/app\";\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "release-session facet")
}

func TestCheckRepositoryRejectsRawReleaseSessionMutationSurface(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/releaseSession/types.ts", "export type Facet = { update: Dispatch<string> };\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "React mutation primitives")
}

func TestCheckRepositoryRejectsPreparationProgressOutsideReleaseSession(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/app.tsx", "const event = \"preparation:progress\";\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "preparation progress subscription")
}

func TestCheckRepositoryRejectsTrackersInFrontendPreparationIntent(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"webui/src/releaseSession/types.ts",
		"export type PreparationIntent = { sourcePath: string; trackers: readonly string[]; };\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "cannot contain workflow tracker selection")
}

func TestCheckRepositoryRejectsLegacyJobCoordination(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/releaseSession/jobs.ts", "const jobs = jobsClient.list();\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "Job coordination is removed")
}

func TestCheckRepositoryRejectsMessageSubstringRecovery(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/releaseSession/errors.ts", "if (error.message.includes(\"stale\")) recover();\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "error-message substrings")
}

func TestCheckRepositoryRejectsReleaseJobHook(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/pages/input/jobs.ts", "const jobs = useReleaseJobs(release);\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "Job coordination is removed")
}

func TestCheckRepositoryAcceptsOwnedFrontendBoundaries(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/releaseSession/workflow.ts", "export const workflowTransport = true;\n")
	writePolicyFixture(t, root, "webui/src/pages/history/index.tsx", "import { historyClient } from \"../../api/app\";\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %#v", violations)
	}
}

func TestCheckRepositoryRejectsOptionalWorkflowPortAndFallback(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"webui/src/releaseSession/ports.ts",
		"export type Ports = { workflow?: WorkflowPorts };\nexport const fallback = workflowOnly;\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "transport must be mandatory")
	assertViolationContains(t, violations, "fallback or Job coordination is removed")
}

func TestCheckRepositoryRejectsLegacyJobPackage(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "internal/webserver/jobs/engine.go", "package jobs\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "legacy release Job package is removed")
}

func TestCheckRepositoryRejectsRouteSpecificWorkflowDTO(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "internal/webserver/api_v1_routes.go", "package webserver\ntype apiV1UploadRequest struct{}\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "shared pkg/api request DTOs")
}

func TestCheckRepositoryRejectsPathBasedWorkflowMediaContract(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"pkg/api/workflow_requests.go",
		"package api\ntype CaptureReleaseWorkflowMediaRequest struct { SourcePath string }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "opaque artifact IDs")
}

func TestCheckRepositoryRejectsCallerVisibleUploadPlan(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "pkg/api/workflow_requests.go", "package api\ntype UploadPlanRef struct{}\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "cannot build or execute a release upload plan")
}

func TestCheckRepositoryRejectsAdapterPublicationCommandConstruction(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"cmd/upbrr/workflow.go",
		"package main\nfunc publish() { _ = releaseworkflow.PublishPreflightCommand{} }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "cannot be constructed by adapters")
}

func TestCheckRepositoryRejectsHandMaintainedWorkflowTransport(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/api/generated/release-workflow.ts", "export type Workflow = {};\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "must be generated")
}

func TestCheckRepositoryRejectsRemovedBrowserWorkflowRoute(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/webserver/app_routes.go",
		"package webserver\nconst route = \"/api/app/StartReviewedTrackerUpload\"\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "legacy browser workflow route")
}

func TestCheckRepositoryRejectsSideEffectsInWorkflowProjection(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/workflow_projector.go",
		"package trackers\nfunc project(service Service) { service.BuildUploadReview() }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "side-effect free")
}

func TestCheckRepositoryRejectsLegacyJobStartFromReleaseSession(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"webui/src/releaseSession/index.tsx",
		"const start = () => releaseJobs.startDupe({});\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "legacy release Jobs")
}

func TestCheckRepositoryRejectsFrontendLegacyWorkflowCommand(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/api/app.ts", "const method = \"FetchTrackerDryRun\";\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "releaseWorkflowClient")
}

func TestCheckRepositoryRejectsExportedWorkflowPublicationCommand(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/releaseworkflow/contracts.go",
		"package releaseworkflow\ntype PublishMediaArtifactsCommand struct{}\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "package-private")
}

func TestCheckRepositoryRejectsReleasePageOperationProgress(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/pages/upload_images/index.tsx", `<div role="progressbar" />`)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "WorkflowOperationProgress")
}

func TestCheckRepositoryRejectsCLILegacyProductionEntry(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"cmd/upbrr/legacy.go",
		"package main\nfunc upload() error { _, err := core.RunUploadPrepared(ctx, request); return err }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "in-process release workflow")
}

func TestCheckRepositoryRejectsGenericWorkflowTrackerDispatch(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/trackers/workflow_projector.go",
		"package trackers\nfunc project(tracker string) { if strings.EqualFold(tracker, \"BTN\") {} }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "registry-owned stable tracker dispatch")
}

func TestCheckRepositoryRejectsSourcePathOnlyWorkflowArtifactLookup(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(
		t,
		root,
		"internal/core/workflow_media.go",
		"package core\nfunc build() { LoadMediaBySourcePath() }\n",
	)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "source-path-only lookup")
}

func TestCheckRepositoryRequiresProjectionNameAndDirectExecution(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "internal/core/workflow_upload_plan.go", "package core\n")
	writePolicyFixture(t, root, "internal/releaseworkflow/module.go", "package releaseworkflow\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "finalized projection upload name")
	assertViolationContains(t, violations, "direct upload must execute private preparation")
}

func writePolicyFixture(t *testing.T, root string, relative string, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

func assertViolationContains(t *testing.T, violations []Violation, message string) {
	t.Helper()
	for _, violation := range violations {
		if strings.Contains(violation.Message, message) {
			return
		}
	}
	t.Fatalf("violations = %#v, want message containing %q", violations, message)
}
