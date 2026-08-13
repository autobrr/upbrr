// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func workflowTestFingerprint(t *testing.T, value any) WorkflowFingerprint {
	t.Helper()
	fingerprint, err := CanonicalWorkflowFingerprint(value)
	if err != nil {
		t.Fatalf("fingerprint fixture: %v", err)
	}
	return fingerprint
}

func TestReleaseFactInstructionSnapshotFingerprintAndClonePreserveTriState(t *testing.T) {
	t.Parallel()

	zero := 0
	empty := ""
	falseValue := false
	snapshot := ReleaseFactInstructionSnapshot{
		ID:         "facts-1",
		WorkflowID: "workflow-1",
		Revision:   1,
		Instructions: ReleaseFactInstructions{
			Identity: ExternalIDOverrides{TMDBID: &zero},
			ReleaseName: ReleaseNameOverrides{
				Tag:    &empty,
				NoYear: &falseValue,
			},
			TrackerIDs: map[string]string{" btn ": " 123 "},
		},
		CreatedAt: time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC),
	}
	snapshot, err := snapshot.WithFingerprint()
	if err != nil {
		t.Fatalf("fingerprint fact instructions: %v", err)
	}
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("validate fact instructions: %v", err)
	}
	if got := snapshot.Instructions.TrackerIDs["BTN"]; got != "123" {
		t.Fatalf("normalized tracker id = %q", got)
	}

	clone, err := snapshot.Clone()
	if err != nil {
		t.Fatalf("clone fact instructions: %v", err)
	}
	*clone.Instructions.Identity.TMDBID = 9
	*clone.Instructions.ReleaseName.Tag = "GRP"
	if *snapshot.Instructions.Identity.TMDBID != 0 || *snapshot.Instructions.ReleaseName.Tag != "" || *snapshot.Instructions.ReleaseName.NoYear {
		t.Fatal("clone mutated tri-state source values")
	}

	changed := snapshot
	changed.Instructions.Identity.TMDBID = nil
	changedFingerprint, err := changed.ComputeFingerprint()
	if err != nil {
		t.Fatalf("fingerprint changed instructions: %v", err)
	}
	if changedFingerprint == snapshot.Fingerprint {
		t.Fatal("absent and explicit zero instructions produced the same fingerprint")
	}
}

func TestReleaseFactInstructionUpdatePreservesAbsentNullAndZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		present bool
		reset   bool
		zeroID  bool
	}{
		{name: "absent", payload: `{}`},
		{
			name:    "reset",
			payload: `{"instructions":null}`,
			present: true,
			reset:   true,
		},
		{
			name:    "zero",
			payload: `{"instructions":{"Identity":{"TMDBID":0}}}`,
			present: true,
			zeroID:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var update ReleaseFactInstructionUpdate
			if err := json.Unmarshal([]byte(test.payload), &update); err != nil {
				t.Fatalf("unmarshal update: %v", err)
			}
			if update.Instructions.Present != test.present || update.Instructions.Reset != test.reset {
				t.Fatalf("presence/reset = %t/%t", update.Instructions.Present, update.Instructions.Reset)
			}
			if test.zeroID {
				if update.Instructions.Value.Identity.TMDBID == nil || *update.Instructions.Value.Identity.TMDBID != 0 {
					t.Fatal("explicit zero provider id was not preserved")
				}
			}
			encoded, err := json.Marshal(update)
			if err != nil {
				t.Fatalf("marshal update: %v", err)
			}
			var roundTrip ReleaseFactInstructionUpdate
			if err := json.Unmarshal(encoded, &roundTrip); err != nil {
				t.Fatalf("round-trip update: %v", err)
			}
			if roundTrip.Instructions.Present != test.present || roundTrip.Instructions.Reset != test.reset {
				t.Fatal("round-trip changed patch presence")
			}
		})
	}
}

func TestTrackerProjectionInstructionsPreserveAbsentNullAndEmptyName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		present bool
		reset   bool
		value   string
	}{
		{name: "absent", payload: `{}`},
		{
			name:    "reset",
			payload: `{"uploadReleaseName":null}`,
			present: true,
			reset:   true,
		},
		{
			name:    "clear",
			payload: `{"uploadReleaseName":""}`,
			present: true,
		},
		{
			name:    "value",
			payload: `{"uploadReleaseName":"Example.Release.2026.1080p-GRP"}`,
			present: true,
			value:   "Example.Release.2026.1080p-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var instructions TrackerProjectionInstructions
			if err := json.Unmarshal([]byte(test.payload), &instructions); err != nil {
				t.Fatalf("unmarshal projection instructions: %v", err)
			}
			if instructions.UploadReleaseName.Present != test.present || instructions.UploadReleaseName.Reset != test.reset ||
				instructions.UploadReleaseName.Value != test.value {
				t.Fatalf("patch = %#v", instructions.UploadReleaseName)
			}
			encoded, err := json.Marshal(instructions)
			if err != nil {
				t.Fatalf("marshal projection instructions: %v", err)
			}
			var roundTrip TrackerProjectionInstructions
			if err := json.Unmarshal(encoded, &roundTrip); err != nil {
				t.Fatalf("round-trip projection instructions: %v", err)
			}
			if roundTrip.UploadReleaseName != instructions.UploadReleaseName {
				t.Fatal("round-trip changed projection patch presence")
			}
		})
	}
}

func TestTrackerProjectionInstructionsIgnoreRuleAuthorization(t *testing.T) {
	t.Parallel()

	var instructions TrackerProjectionInstructions
	if err := json.Unmarshal([]byte(`{"authorizedRuleFingerprint":"forged"}`), &instructions); err != nil {
		t.Fatalf("unmarshal projection rule authorization: %v", err)
	}
	payload, err := json.Marshal(instructions)
	if err != nil {
		t.Fatalf("marshal projection rule authorization: %v", err)
	}
	if strings.Contains(string(payload), "authorizedRuleFingerprint") {
		t.Fatalf("caller rule authorization survived: %s", payload)
	}
}

func TestTrackerProjectionInstructionNormalizationRejectsDuplicateTrackerIDs(t *testing.T) {
	t.Parallel()

	duplicateTrackerID := TrackerID(" alpha ")
	_, err := (TrackerProjectionInstructionSnapshot{Instructions: map[TrackerID]TrackerProjectionInstructions{
		"ALPHA":            {},
		duplicateTrackerID: {},
	}}).Normalize()
	if err == nil || !strings.Contains(err.Error(), "duplicate tracker id ALPHA") {
		t.Fatalf("duplicate normalized tracker ID error = %v", err)
	}
}

func TestTrackerCatalogNormalizesAliasesAndFingerprintsDeterministically(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	policyA := workflowTestFingerprint(t, "policy-a")
	policyB := workflowTestFingerprint(t, "policy-b")
	catalog := TrackerCatalogSnapshot{
		ID:             "catalog-1",
		Revision:       1,
		CatalogVersion: "v1",
		Trackers: []TrackerCatalogDescriptor{
			{
				TrackerID:         "beta",
				DisplayName:       "Beta",
				Aliases:           []string{" old-beta ", "BETA", "OLD-BETA"},
				Family:            "standalone",
				BaseURL:           "https://beta.example.invalid/",
				UploadContentMode: "description",
				ProjectorVersion:  "1",
				PolicyFingerprint: policyB,
			},
			{
				TrackerID:         "alpha",
				DisplayName:       "Alpha",
				Family:            "unit3d",
				BaseURL:           "https://alpha.example.invalid",
				UploadContentMode: "description",
				ProjectorVersion:  "1",
				PolicyFingerprint: policyA,
			},
		},
		CreatedAt: createdAt,
	}
	catalog, err := catalog.WithFingerprint()
	if err != nil {
		t.Fatalf("fingerprint catalog: %v", err)
	}
	if err := catalog.Validate(); err != nil {
		t.Fatalf("validate catalog: %v", err)
	}
	if catalog.Trackers[0].TrackerID != "ALPHA" || catalog.Trackers[1].TrackerID != "BETA" {
		t.Fatalf("catalog order = %v, %v", catalog.Trackers[0].TrackerID, catalog.Trackers[1].TrackerID)
	}
	if got, ok := catalog.ResolveTrackerID(" old-beta "); !ok || got != "BETA" {
		t.Fatalf("alias resolution = %q, %t", got, ok)
	}

	reordered := catalog
	reordered.Trackers = []TrackerCatalogDescriptor{catalog.Trackers[1], catalog.Trackers[0]}
	fingerprint, err := reordered.ComputeFingerprint()
	if err != nil {
		t.Fatalf("fingerprint reordered catalog: %v", err)
	}
	if fingerprint != catalog.Fingerprint {
		t.Fatal("catalog fingerprint depends on input ordering")
	}
}

func TestReleaseWorkflowRejectsInvalidLineageAndBlockedStatus(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	workflow := ReleaseWorkflow{
		ID:               "workflow-1",
		Revision:         1,
		FactInstructions: ReleaseFactInstructionSnapshotRef{ID: "facts-1", Revision: 1},
		Status:           WorkflowStatusActive,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := workflow.Validate(); err != nil {
		t.Fatalf("validate minimal workflow: %v", err)
	}

	selection := TrackerSelectionRef{ID: "selection-1", Revision: 1}
	workflow.Selection = &selection
	if err := workflow.Validate(); err == nil || !strings.Contains(err.Error(), "catalog and runtime") {
		t.Fatalf("expected selection lineage failure, got %v", err)
	}

	workflow.Selection = nil
	workflow.Status = WorkflowStatusBlocked
	if err := workflow.Validate(); err == nil || !strings.Contains(err.Error(), "actions or failures") {
		t.Fatalf("expected blocked-status failure, got %v", err)
	}
}

func TestDirectUploadContractsValidateExactLineageAndTerminalOutcomes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)
	lineage := struct {
		projection   TrackerReleaseProjectionSetRef
		dupes        DupeAssessmentRef
		media        MediaArtifactSetRef
		descriptions DescriptionSetRef
	}{
		projection:   TrackerReleaseProjectionSetRef{ID: "projections-1", Revision: 2},
		dupes:        DupeAssessmentRef{ID: "dupes-1", Revision: 3},
		media:        MediaArtifactSetRef{ID: "media-1", Revision: 4},
		descriptions: DescriptionSetRef{ID: "descriptions-1", Revision: 5},
	}
	fingerprint := workflowTestFingerprint(t, "direct-upload-input")
	dryRun := UploadDryRunResult{
		ID:               "dry-run-1",
		WorkflowID:       "workflow-1",
		Revision:         6,
		ProjectionSet:    lineage.projection,
		Dupes:            lineage.dupes,
		Media:            lineage.media,
		Descriptions:     lineage.descriptions,
		InputFingerprint: fingerprint,
		TrackerIDs:       []TrackerID{"ALPHA"},
		Reports: []TrackerDryRunReport{{
			TrackerID:           "ALPHA",
			UploadReleaseName:   "Example.Release.2026.1080p-GRP",
			Status:              StageStatusCompleted,
			Endpoint:            "https://tracker.example.invalid/upload",
			Fields:              []UploadPlanField{{Key: "api_token", Value: "[redacted]"}},
			SemanticFingerprint: workflowTestFingerprint(t, "direct-upload-alpha"),
			ClientInjection:     ClientInjectionOutcome{Status: StageStatusSkipped},
		}},
		SucceededCount: 1,
		Status:         StageStatusCompleted,
		CreatedAt:      now,
	}
	if err := dryRun.Validate(); err != nil {
		t.Fatalf("validate direct dry run: %v", err)
	}
	missingTargets := dryRun
	missingTargets.TrackerIDs = nil
	if err := missingTargets.Validate(); err == nil || !strings.Contains(err.Error(), "target tracker IDs") {
		t.Fatalf("expected missing target tracker rejection, got %v", err)
	}
	unsafeDryRun := dryRun
	unsafeDryRun.Reports = append([]TrackerDryRunReport(nil), dryRun.Reports...)
	unsafeDryRun.Reports[0].Endpoint += "?api_token=private"
	if err := unsafeDryRun.Validate(); err == nil || !strings.Contains(err.Error(), "query values") {
		t.Fatalf("expected unsafe dry-run endpoint rejection, got %v", err)
	}

	result := UploadResult{
		ID:               "upload-result-1",
		WorkflowID:       "workflow-1",
		Revision:         7,
		ProjectionSet:    lineage.projection,
		Dupes:            lineage.dupes,
		Media:            lineage.media,
		Descriptions:     lineage.descriptions,
		InputFingerprint: fingerprint,
		Results: []UploadTrackerResult{
			{TrackerID: "ALPHA", Status: StageStatusCompleted},
			{
				TrackerID: "BETA",
				Status:    StageStatusFailed,
				Failures: []WorkflowFailure{{Failure: OperationFailure{
					Code:      OperationFailureInternal,
					Operation: OperationKindUploadExecute,
					Message:   "Synthetic tracker failure.",
					Recovery:  OperationRecoveryRetry,
				}}},
			},
		},
		Status:    StageStatusFailed,
		CreatedAt: now,
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("validate direct upload result: %v", err)
	}
	result.Status = StageStatusCompleted
	if err := result.Validate(); err == nil || !strings.Contains(err.Error(), "cannot contain failed trackers") {
		t.Fatalf("expected aggregate upload status rejection, got %v", err)
	}
}

func TestWorkflowPublicContractsDeclareNoSecretOrExecutionFields(t *testing.T) {
	t.Parallel()

	forbidden := []string{"password", "apikey", "apitoken", "authkey", "passkey", "cookie", "session", "callback", "factory", "preparedseed", "executiontoken"}
	types := []reflect.Type{
		reflect.TypeFor[ReleaseFactInstructionSnapshot](),
		reflect.TypeFor[TrackerCatalogSnapshot](),
		reflect.TypeFor[TrackerRuntimeSnapshot](),
		reflect.TypeFor[TrackerReleaseProjectionSet](),
		reflect.TypeFor[TrackerPreflightAssessment](),
		reflect.TypeFor[DupeAssessment](),
		reflect.TypeFor[MediaArtifactSet](),
		reflect.TypeFor[DescriptionInstructions](),
		reflect.TypeFor[DescriptionSet](),
		reflect.TypeFor[UploadPlan](),
		reflect.TypeFor[UploadDryRunResult](),
		reflect.TypeFor[UploadResult](),
		reflect.TypeFor[ReleaseWorkflow](),
	}
	for _, contractType := range types {
		assertWorkflowTypeHasNoForbiddenFields(t, contractType, forbidden, map[reflect.Type]bool{})
	}
}

func TestReadyTrackerProjectionRequiresDeclaredSearchName(t *testing.T) {
	t.Parallel()

	fingerprint := WorkflowFingerprint(strings.Repeat("a", 64))
	projectionSet := TrackerReleaseProjectionSet{
		ID:                "projection-set-1",
		WorkflowID:        "workflow-1",
		Revision:          1,
		Release:           ReleaseSnapshotRef{ID: "release-1", Revision: 1},
		ReleaseRef:        ReleaseRef{SourcePath: `C:\Media\Example.Release.2026.mkv`, Generation: 1},
		Catalog:           TrackerCatalogSnapshotRef{ID: "catalog-1", Revision: 1},
		Runtime:           TrackerRuntimeSnapshotRef{ID: "runtime-1", Revision: 1},
		Selection:         TrackerSelectionRef{ID: "selection-1", Revision: 1},
		InputFingerprint:  fingerprint,
		PolicyFingerprint: fingerprint,
		Projections: []TrackerReleaseProjection{{
			TrackerID:            "EXAMPLE",
			UploadReleaseName:    "Example.Release.2026.1080p-GRP",
			DuplicateCriteria:    TrackerDuplicateCriteria{Name: "Example Release 2026"},
			InputFingerprint:     fingerprint,
			CatalogFingerprint:   fingerprint,
			ConfigFingerprint:    fingerprint,
			ProjectorFingerprint: fingerprint,
			CriteriaFingerprint:  fingerprint,
			Readiness:            ReadinessStatusReady,
			DupeReady:            true,
			UploadReady:          true,
		}},
		Status:    StageStatusReady,
		CreatedAt: time.Now().UTC(),
	}
	if err := projectionSet.Validate(); err == nil || !strings.Contains(err.Error(), "undeclared duplicate-search name") {
		t.Fatalf("expected undeclared search-name rejection, got %v", err)
	}
	projectionSet.Projections[0].AdditionalNames = []TrackerReleaseName{{
		Role:  TrackerReleaseNameRoleSearch,
		Value: "Example Release 2026",
	}}
	if err := projectionSet.Validate(); err != nil {
		t.Fatalf("validate declared search name: %v", err)
	}
	projectionSet.Projections[0].PolicyDecisions = []TrackerPolicyDecision{{
		Code:     "constructibility",
		Decision: "blocked",
		Blocking: true,
	}}
	if err := projectionSet.Validate(); err == nil || !strings.Contains(err.Error(), "dupe-ready with a blocking policy decision") {
		t.Fatalf("expected ready projection blocking-policy rejection, got %v", err)
	}
	projectionSet.Projections[0].PolicyDecisions = nil
	projectionSet.Projections[0].DuplicateCriteria.Name = ""
	if err := projectionSet.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate-search name") {
		t.Fatalf("expected missing search-name rejection, got %v", err)
	}
}

func assertWorkflowTypeHasNoForbiddenFields(t *testing.T, contractType reflect.Type, forbidden []string, seen map[reflect.Type]bool) {
	t.Helper()
	for contractType.Kind() == reflect.Pointer || contractType.Kind() == reflect.Slice || contractType.Kind() == reflect.Array || contractType.Kind() == reflect.Map {
		contractType = contractType.Elem()
	}
	if contractType.Kind() != reflect.Struct || contractType.PkgPath() != reflect.TypeFor[ReleaseWorkflow]().PkgPath() || seen[contractType] {
		return
	}
	seen[contractType] = true
	for field := range contractType.Fields() {
		name := strings.ToLower(field.Name)
		for _, value := range forbidden {
			if strings.Contains(name, value) {
				t.Fatalf("public contract %s declares forbidden field %s", contractType, field.Name)
			}
		}
		assertWorkflowTypeHasNoForbiddenFields(t, field.Type, forbidden, seen)
	}
}
