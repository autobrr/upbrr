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
	writePolicyFixture(t, root, "internal/core/display.go", "package core\nvar value = api.ProviderDisplay{}\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "display construction")
}

func TestCheckRepositoryRejectsEligibilityConstructionOutsideCore(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "internal/webserver/eligibility.go", "package webserver\nvar value = api.TrackerEligibility{}\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "eligibility construction")
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

func TestCheckRepositoryRejectsJobCoordinationOutsideRegistry(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/releaseSession/jobs.ts", "const jobs = jobsClient.list();\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "Job coordination")
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

func TestCheckRepositoryRejectsReleaseJobHookOutsideSession(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/pages/input/jobs.ts", "const jobs = useReleaseJobs(release);\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	assertViolationContains(t, violations, "behind useReleaseSession")
}

func TestCheckRepositoryAcceptsOwnedFrontendBoundaries(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "webui/src/jobRegistry/coordinator.ts", "const jobs = jobsClient.list();\nconst event = \"jobs:update\";\n")
	writePolicyFixture(t, root, "webui/src/releaseSession/jobs.ts", "const jobs = useReleaseJobs(release);\n")
	writePolicyFixture(t, root, "webui/src/pages/history/index.tsx", "import { historyClient } from \"../../api/app\";\n")
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatalf("check repository: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("violations = %#v", violations)
	}
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
