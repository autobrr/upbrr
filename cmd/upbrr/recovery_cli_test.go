// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestRecoverCommandUnblocksChangedConfigWithoutActivatingIt(t *testing.T) {
	t.Parallel()
	repo, cfg, configPath := newRecoveryCLIFixture(t)
	for _, effect := range []struct {
		owner  string
		id     string
		kind   string
		status api.WorkflowEffectStatus
	}{
		{"cli", "submission", "tracker_submission", api.WorkflowEffectStatusUnknown},
		{"cli", "images", "image_hosting", api.WorkflowEffectStatusStarted},
		{"web-owner", "injection", "client_injection", api.WorkflowEffectStatusUnknown},
	} {
		insertRecoveryCLIWorkflow(t, repo, effect.owner, effect.id, effect.kind, effect.status)
	}
	expired := api.ActiveInputRecord{
		State:          api.ActiveInputActive,
		Revision:       1,
		Fence:          1,
		OwnerID:        "cli",
		CoordinatorID:  "previous-process",
		LeaseExpiresAt: time.Now().UTC().Add(-time.Minute),
		InputID:        "input",
		SourceVersion:  "version",
		WorkflowID:     "images",
	}
	inputPayload, err := json.Marshal(expired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE active_input SET revision = 1, fence = 1, record_json = ? WHERE singleton = 1`, inputPayload); err != nil {
		t.Fatal(err)
	}
	before, err := repo.LoadConfigActivation(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var storedBefore map[string]json.RawMessage
	if err := repo.LoadFullConfig(t.Context(), &storedBefore); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cliConfigActivation(t.Context(), cfg, repo.DBPath(), api.NopLogger{}); !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) ||
		!strings.Contains(err.Error(), "upbrr recover") {
		t.Fatalf("changed-config startup before recovery = %v", err)
	}
	var output bytes.Buffer
	if err := executeCLI(t.Context(), []string{"recover", "-config", configPath}, cliIO{
		in:  strings.NewReader("yes\nyes\nyes\n"),
		out: &output,
	}); err != nil {
		t.Fatalf("recover: %v\n%s", err, output.String())
	}
	if strings.Count(output.String(), "Confirm it did not complete?") != 3 || !strings.Contains(output.String(), "Interrupted effects reconciled") ||
		!strings.Contains(output.String(), "Source: Unavailable.Release.2026-GRP.mkv") || !strings.Contains(output.String(), "scope=imgbb") ||
		!strings.Contains(output.String(), "scope=upload:ANT") {
		t.Fatalf("recovery did not confirm each retained effect: %s", output.String())
	}
	after, err := repo.LoadConfigActivation(t.Context())
	if err != nil || after.ActiveGeneration != before.ActiveGeneration || after.Fingerprint != before.Fingerprint {
		t.Fatalf("recovery changed activation: %#v, err=%v", after, err)
	}
	var storedAfter map[string]json.RawMessage
	if err := repo.LoadFullConfig(t.Context(), &storedAfter); err != nil {
		t.Fatal(err)
	}
	beforeJSON, err := json.Marshal(storedBefore)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, err := json.Marshal(storedAfter)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeJSON, afterJSON) {
		t.Fatal("recovery changed stored configuration")
	}
	var uncertain, succeeded, fences int
	if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT count(*) FROM release_workflow_effects WHERE status IN ('started', 'unknown')`).Scan(&uncertain); err != nil || uncertain != 0 {
		t.Fatalf("unresolved effects=%d, err=%v", uncertain, err)
	}
	if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT count(*) FROM release_workflow_effects WHERE status = 'succeeded'`).Scan(&succeeded); err != nil || succeeded != 1 {
		t.Fatalf("successful effects=%d, err=%v", succeeded, err)
	}
	if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT count(*) FROM submission_fences WHERE status = 'succeeded'`).Scan(&fences); err != nil || fences != 1 {
		t.Fatalf("successful submission fences=%d, err=%v", fences, err)
	}
	if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT count(*) FROM submission_fences WHERE status IN ('started', 'unknown')`).Scan(&fences); err != nil || fences != 0 {
		t.Fatalf("uncertain submission fences=%d, err=%v", fences, err)
	}
	generation, fingerprint, err := cliConfigActivation(t.Context(), cfg, repo.DBPath(), api.NopLogger{})
	if err != nil || generation != before.ActiveGeneration+1 || fingerprint == before.Fingerprint {
		t.Fatalf("same PR configuration after recovery: generation=%d fingerprint=%q err=%v", generation, fingerprint, err)
	}
}

func TestRecoverCommandPreservesPublishedClientResult(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name             string
		publishedUnknown bool
		failSave         bool
	}{
		{name: "result before interrupted retry"},
		{name: "published unknown outcome with registered authority", publishedUnknown: true},
		{name: "failed recovery save preserves prior authority", failSave: true},
	} {
		t.Run(test.name, func(t *testing.T) { checkRecoveryCLIPublishedClientResult(t, test.publishedUnknown, test.failSave) })
	}
}

func checkRecoveryCLIPublishedClientResult(t *testing.T, publishedUnknown, failSave bool) {
	t.Helper()
	repo, cfg, configPath := newRecoveryCLIFixture(t)
	insertRecoveryCLIWorkflow(t, repo, "cli", "retry", "client_injection", api.WorkflowEffectStatusStarted)
	record, err := repo.LoadReleaseWorkflowState(t.Context(), "cli", "retry")
	if err != nil {
		t.Fatal(err)
	}
	var state releaseworkflow.State
	if err := json.Unmarshal(record.Payload, &state); err != nil {
		t.Fatal(err)
	}
	addRecoveryCLIMediaRefs(&state)
	state.Workflow.Dupes = &api.DupeAssessmentRef{ID: "dupes", Revision: 1}
	state.Workflow.Descriptions = &api.DescriptionSetRef{ID: "descriptions", Revision: 1}
	state.Workflow.UploadResult = &api.UploadResultRef{ID: "result", Revision: 1}
	failureCode := api.OperationFailureClientInjection
	recoveryAction := api.OperationRecoveryRetry
	if publishedUnknown {
		failureCode = api.OperationFailureUnknownOutcome
		recoveryAction = api.OperationRecoveryConfirm
	}
	prior := api.UploadResult{
		ID:               "result",
		WorkflowID:       "retry",
		Revision:         1,
		ProjectionSet:    *state.Workflow.TrackerProjections,
		Dupes:            *state.Workflow.Dupes,
		Media:            *state.Workflow.Media,
		Descriptions:     *state.Workflow.Descriptions,
		InputFingerprint: api.WorkflowFingerprint(strings.Repeat("a", 64)),
		Status:           api.StageStatusPartial,
		CreatedAt:        state.Workflow.CreatedAt,
		Results: []api.UploadTrackerResult{{
			TrackerID:              "ANT",
			Status:                 api.StageStatusPartial,
			SubmissionStatus:       api.StageStatusCompleted,
			ClientInjectionStatus:  api.StageStatusFailed,
			ClientInjectionMessage: "Client injection failed.",
			ClientFailureCode:      failureCode,
			RemoteID:               "known-submission",
			Failures: []api.WorkflowFailure{{
				TrackerID: "ANT",
				Resource:  "upload:ANT",
				Failure: api.OperationFailure{
					Code:      failureCode,
					Operation: api.OperationKindClientInjection,
					Message:   "Client injection failed.",
					Recovery:  recoveryAction,
				},
			}},
		}},
	}
	if err := prior.Validate(); err != nil {
		t.Fatal(err)
	}
	state.UploadResults = map[api.UploadResultID]api.UploadResult{prior.ID: prior}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `UPDATE release_workflow_states SET state_json = ? WHERE owner_id = 'cli' AND workflow_id = 'retry'`, payload); err != nil {
		t.Fatal(err)
	}
	vault, err := releaseworkflow.NewPrivateArtifactVault(releaseworkflow.PrivateArtifactVaultRoot(repo.DBPath()))
	if err != nil {
		t.Fatal(err)
	}
	authority := releaseworkflow.RegisteredArtifactAuthority{
		ClientSubject: api.ClientSubject{SourcePath: state.SourcePath},
		Torrents: map[api.TrackerID]api.TorrentResult{
			"ANT": {Tracker: "ANT", Path: filepath.Join(t.TempDir(), "registered.torrent")},
		},
	}
	if err := vault.Put("cli", "retry", "registered-artifacts:"+string(prior.ID), authority, time.Now().UTC().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if failSave {
		if _, err := repo.RawDB().ExecContext(t.Context(), `CREATE TRIGGER reject_recovery_confirmation
			BEFORE UPDATE ON release_workflow_states
			WHEN NEW.owner_id = 'cli' AND NEW.workflow_id = 'retry' AND NEW.revision >= 3
			BEGIN SELECT RAISE(ABORT, 'synthetic recovery save failure'); END`); err != nil {
			t.Fatal(err)
		}
	}
	err = executeCLI(t.Context(), []string{"recover", "--config", configPath}, cliIO{in: strings.NewReader("yes\n")})
	if failSave {
		if err == nil || !strings.Contains(err.Error(), "synthetic recovery save failure") {
			t.Fatalf("injected save failure = %v", err)
		}
	} else if err != nil {
		t.Fatalf("reconcile interrupted client retry: %v", err)
	}
	record, err = repo.LoadReleaseWorkflowState(t.Context(), "cli", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(record.Payload, &state); err != nil {
		t.Fatal(err)
	}
	retainedJSON, err := json.Marshal(state.UploadResults[prior.ID])
	if err != nil {
		t.Fatal(err)
	}
	priorJSON, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	wantPending := 0
	if failSave {
		wantPending = 1
		if record.Revision != 2 {
			t.Fatalf("failed confirmation persisted workflow revision %d", record.Revision)
		}
	}
	if state.Workflow.UploadResult == nil || !bytes.Equal(retainedJSON, priorJSON) || len(state.Workflow.RequiredActions) != wantPending {
		t.Fatal("recovery changed the prior submitted result or retained its confirmation")
	}
	if publishedUnknown {
		current := state.UploadResults[state.Workflow.UploadResult.ID]
		if current.ID == prior.ID || len(current.Results) != 1 || current.Results[0].SubmissionStatus != api.StageStatusCompleted ||
			current.Results[0].RemoteID != "known-submission" || current.Results[0].ClientFailureCode != api.OperationFailureClientInjection ||
			len(current.Results[0].Failures) != 1 || current.Results[0].Failures[0].Failure.Recovery != api.OperationRecoveryRetry {
			t.Fatalf("registered tracker result lost retry authority: %#v", current)
		}
	} else if state.Workflow.UploadResult.ID != prior.ID {
		t.Fatal("interrupted retry replaced its prior submitted result")
	}
	reopened, err := releaseworkflow.NewPrivateArtifactVault(releaseworkflow.PrivateArtifactVaultRoot(repo.DBPath()))
	if err != nil {
		t.Fatal(err)
	}
	value, err := reopened.Get("cli", "retry", "registered-artifacts:"+string(state.Workflow.UploadResult.ID), time.Now().UTC())
	if err != nil {
		t.Fatalf("current result lost durable registered artifacts: %v", err)
	}
	registered, ok := value.(releaseworkflow.RegisteredArtifactAuthority)
	if !ok || registered.Torrents["ANT"].Path != authority.Torrents["ANT"].Path {
		t.Fatal("current result no longer identifies the registered tracker torrent")
	}
	if failSave {
		scopes, err := repo.ListReleaseWorkflowRecoveryScopes(t.Context())
		if err != nil || len(scopes) != 1 || scopes[0].WorkflowID != "retry" {
			t.Fatalf("failed confirmation lost its recovery scope: %#v, err=%v", scopes, err)
		}
		var status api.WorkflowEffectStatus
		if err := repo.RawDB().QueryRowContext(t.Context(),
			`SELECT status FROM release_workflow_effects WHERE workflow_id = 'retry' AND kind = 'client_injection'`).Scan(&status); err != nil ||
			status != api.WorkflowEffectStatusUnknown {
			t.Fatalf("failed confirmation resolved its effect: status=%s err=%v", status, err)
		}
		if _, err := repo.RawDB().ExecContext(t.Context(), `DROP TRIGGER reject_recovery_confirmation`); err != nil {
			t.Fatal(err)
		}
		if err := executeCLI(t.Context(), []string{"recover", "--config", configPath}, cliIO{in: strings.NewReader("yes\n")}); err != nil {
			t.Fatalf("retry failed confirmation: %v", err)
		}
	}
	if _, _, err := cliConfigActivation(t.Context(), cfg, repo.DBPath(), api.NopLogger{}); err != nil {
		t.Fatalf("same build after client retry recovery: %v", err)
	}
}

func TestRecoverCommandDeclineRetainsBlocker(t *testing.T) {
	for _, input := range []string{"no\n", ""} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			repo, cfg, configPath := newRecoveryCLIFixture(t)
			insertRecoveryCLIWorkflow(t, repo, "cli", "declined", "tracker_submission", api.WorkflowEffectStatusUnknown)
			var output bytes.Buffer
			err := executeCLI(t.Context(), []string{"recover", "--config", configPath}, cliIO{in: strings.NewReader(input), out: &output})
			wantError := "remains unresolved"
			if input == "" {
				wantError = "read recovery confirmation"
			}
			if err == nil || !strings.Contains(err.Error(), wantError) {
				t.Fatalf("declined recovery = %v", err)
			}
			if _, _, err := cliConfigActivation(t.Context(), cfg, repo.DBPath(), api.NopLogger{}); !errors.Is(err, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
				t.Fatalf("declined recovery removed activation blocker: %v", err)
			}
			slot, err := repo.LoadActiveInput(t.Context())
			if err != nil || slot.LeaseExpiresAt.After(time.Now().UTC()) {
				t.Fatalf("declined recovery retained a live lease: %#v, err=%v", slot, err)
			}
		})
	}
}

func TestRecoverCommandProtectsLiveWork(t *testing.T) {
	t.Parallel()
	repo, _, configPath := newRecoveryCLIFixture(t)
	insertRecoveryCLIWorkflow(t, repo, "cli", "live", "tracker_submission", api.WorkflowEffectStatusStarted)
	now := time.Now().UTC()
	stamp := now.Format(time.RFC3339Nano)
	if _, err := repo.RawDB().ExecContext(t.Context(), `INSERT INTO release_workflow_operations (
		owner_id, workflow_id, operation_id, expected_revision, idempotency_key,
		command_fingerprint, command_name, process_epoch, status, sequence, operation_json, started_at, updated_at
	) VALUES ('cli', 'live', 'operation', 1, '', 'synthetic', 'continue', 'live-process', 'running', 1, '{}', ?, ?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(t.Context(), `INSERT INTO release_workflow_work (
		owner_id, workflow_id, operation_id, lease_owner, lease_expires_at, checkpoint_json, updated_at
	) VALUES ('cli', 'live', 'operation', 'live-process', ?, '{}', ?)`, now.Add(time.Minute).Format(time.RFC3339Nano), stamp); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := executeCLI(t.Context(), []string{"recover", "--config", configPath}, cliIO{in: strings.NewReader("yes\n"), out: &output})
	if !errors.Is(err, api.ErrActiveInputBusy) || strings.Contains(output.String(), "Confirm it did not complete?") {
		t.Fatalf("live work recovery: err=%v output=%s", err, output.String())
	}
	var status string
	if err := repo.RawDB().QueryRowContext(t.Context(), `SELECT status FROM release_workflow_effects WHERE workflow_id = 'live'`).Scan(&status); err != nil || status != "started" {
		t.Fatalf("live effect changed: status=%q err=%v", status, err)
	}
}

func TestRecoverCommandSelectsStartupDatabase(t *testing.T) {
	for _, selection := range []string{"JSON", "Python", "implicit config", "environment fallback", "populated default"} {
		t.Run(selection, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("UA_DEFAULT_DB_PATH", "")
			repo, cfg, configPath := newRecoveryCLIFixture(t)
			provided := true
			switch selection {
			case "JSON":
				configPath = filepath.Join(t.TempDir(), "config.json")
				payload, err := config.ExportToJSON(&cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(configPath, []byte(payload), 0o600); err != nil {
					t.Fatal(err)
				}
			case "Python":
				configPath = filepath.Join(t.TempDir(), "config.py")
				payload := "config = {'DEFAULT': {'db_path': '" + filepath.ToSlash(repo.DBPath()) + "'}}"
				if err := os.WriteFile(configPath, []byte(payload), 0o600); err != nil {
					t.Fatal(err)
				}
			case "implicit config", "populated default":
				provided = false
				var err error
				configPath, err = configstore.DefaultYAMLPath()
				if err != nil {
					t.Fatal(err)
				}
				defaultPath, err := db.DefaultPath()
				if err != nil {
					t.Fatal(err)
				}
				defaultRepo, err := db.OpenContext(t.Context(), defaultPath)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = defaultRepo.Close() })
				if err := defaultRepo.MigrateContext(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := config.ExportToYAML(&cfg, configPath); err != nil {
					t.Fatal(err)
				}
				if selection == "populated default" {
					cfg.MainSettings.DBPath = defaultPath
					if err := configstore.SaveToDBPath(t.Context(), &cfg, defaultPath); err != nil {
						t.Fatal(err)
					}
					repo = defaultRepo
				}
			case "environment fallback":
				provided = false
				t.Setenv("UA_DEFAULT_DB_PATH", repo.DBPath())
			}
			insertRecoveryCLIWorkflow(t, repo, "cli", "selected", "tracker_submission", api.WorkflowEffectStatusUnknown)
			var before json.RawMessage
			if err := repo.LoadFullConfig(t.Context(), &before); err != nil {
				t.Fatal(err)
			}
			args := []string{"recover"}
			if provided {
				args = append(args, "--config", configPath)
			}
			var output bytes.Buffer
			err := executeCLI(t.Context(), args, cliIO{in: strings.NewReader("no\n"), out: &output})
			if err == nil || !strings.Contains(err.Error(), "remains unresolved") || !strings.Contains(output.String(), "Workflow selected:") {
				t.Fatalf("recovery missed selected database: err=%v output=%s", err, output.String())
			}
			var after json.RawMessage
			if err := repo.LoadFullConfig(t.Context(), &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("database selection changed stored configuration")
			}
			_, startupPath, err := configstore.Bootstrap(t.Context(), configPath, provided, false)
			if err != nil || !pathing.SamePath(startupPath, repo.DBPath()) {
				t.Fatalf("normal startup selected %q, expected %q: %v", startupPath, repo.DBPath(), err)
			}
		})
	}
}

func TestRecoverCommandRejectsMissingDatabase(t *testing.T) {
	t.Parallel()
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MainSettings.DBPath = filepath.Join(t.TempDir(), "missing.db")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.ExportToYAML(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	err = executeCLI(t.Context(), []string{"recover", "--config", configPath}, cliIO{out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "existing recovery database") {
		t.Fatalf("missing database recovery = %v", err)
	}
	if _, err := os.Stat(cfg.MainSettings.DBPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery created missing database: %v", err)
	}
}

func TestRecoverCommandRejectsNonRecoveryArguments(t *testing.T) {
	for _, args := range [][]string{
		{"recover", "--unattended"},
		{"recover", "--yes"},
		{"recover", "--debug"},
		{"recover", "Example.Release.2026-GRP.mkv"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if err := executeCLI(t.Context(), args, cliIO{}); err == nil {
				t.Fatal("recovery accepted unrelated execution arguments")
			}
		})
	}
}

func newRecoveryCLIFixture(t *testing.T) (*db.SQLiteRepository, config.Config, string) {
	t.Helper()
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "recovery.db")
	cfg, err := config.LoadEmbeddedDefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MainSettings.DBPath = dbPath
	cfg.MainSettings.TMDBAPI = "synthetic-token"
	cfg.TorrentClients = map[string]config.TorrentClientConfig{}
	if err := configstore.SaveToDBPath(ctx, cfg, dbPath); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.ExportToYAML(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	stored, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	stored.MainSettings.DBPath = ""
	payload, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	// The previously active build predates PR 809's ANT setting.
	legacyPayload := bytes.ReplaceAll(payload, []byte(`"GuruTrump":false,`), nil)
	if bytes.Equal(payload, legacyPayload) {
		t.Fatal("fixture did not remove PR 809's new setting")
	}
	fingerprint, err := api.CanonicalWorkflowFingerprint(json.RawMessage(legacyPayload))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.InitializeConfigActivationFingerprint(ctx, fingerprint); err != nil {
		t.Fatal(err)
	}
	stored.MainSettings.DBPath = dbPath
	return repo, *stored, configPath
}

func insertRecoveryCLIWorkflow(t *testing.T, repo *db.SQLiteRepository, owner, id, kind string, status api.WorkflowEffectStatus) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	state := releaseworkflow.State{
		OwnerID:    owner,
		SourcePath: filepath.Join(t.TempDir(), "Unavailable.Release.2026-GRP.mkv"),
		Workflow: api.ReleaseWorkflow{
			ID:               api.WorkflowID(id),
			Revision:         1,
			Status:           api.WorkflowStatusActive,
			CreatedAt:        now,
			UpdatedAt:        now,
			FactInstructions: api.ReleaseFactInstructionSnapshotRef{ID: "facts", Revision: 1},
		},
		FactInstructions: map[api.ReleaseFactInstructionSnapshotID]api.ReleaseFactInstructionSnapshot{
			"facts": {
				ID:         "facts",
				WorkflowID: api.WorkflowID(id),
				Revision:   1,
				CreatedAt:  now,
			},
		},
	}
	if kind == "image_hosting" {
		addRecoveryCLIMediaRefs(&state)
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateReleaseWorkflowState(ctx, api.ReleaseWorkflowStateRecord{
		OwnerID:    owner,
		WorkflowID: state.Workflow.ID,
		Revision:   1,
		Status:     api.WorkflowStatusActive,
		Payload:    payload,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatal(err)
	}
	stamp := now.Format(time.RFC3339Nano)
	scope := "ANT"
	switch kind {
	case "image_hosting":
		scope = "imgbb"
	case "client_injection":
		scope = "upload:ANT"
	}
	if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO release_workflow_effects (
		owner_id, workflow_id, operation_id, effect_id, kind, scope_id, semantic_fingerprint, status, started_at, updated_at
	) VALUES (?, ?, 'operation', 'effect', ?, ?, 'synthetic', ?, ?, ?)`, owner, id, kind, scope, status, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if kind == "client_injection" {
		if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO release_workflow_effects (
			owner_id, workflow_id, operation_id, effect_id, kind, scope_id, semantic_fingerprint, status, started_at, updated_at
		) VALUES (?, ?, 'operation', 'registered-submission', 'tracker_submission', 'ANT', 'synthetic', 'succeeded', ?, ?)`, owner, id, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO submission_fences (
			content_version, content_digest, content_scope, content_json, tracker_site,
			owner_id, workflow_id, operation_id, effect_id, status, started_at, updated_at
		) VALUES ('1', ?, 'file', '{}', 'ANT', ?, ?, 'operation', 'registered-submission', 'succeeded', ?, ?)`, id, owner, id, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if kind == "tracker_submission" {
		if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO submission_fences (
			content_version, content_digest, content_scope, content_json, tracker_site,
			owner_id, workflow_id, operation_id, effect_id, status, started_at, updated_at
		) VALUES ('1', ?, 'file', '{}', 'ANT', ?, ?, 'operation', 'effect', ?, ?, ?)`, id, owner, id, status, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
}

func addRecoveryCLIMediaRefs(state *releaseworkflow.State) {
	state.Workflow.Release = &api.ReleaseSnapshotRef{ID: "release", Revision: 1}
	state.Workflow.TrackerCatalog = &api.TrackerCatalogSnapshotRef{ID: "catalog", Revision: 1}
	state.Workflow.TrackerRuntime = &api.TrackerRuntimeSnapshotRef{ID: "runtime", Revision: 1}
	state.Workflow.Selection = &api.TrackerSelectionRef{ID: "selection", Revision: 1}
	state.Workflow.TrackerProjections = &api.TrackerReleaseProjectionSetRef{ID: "projections", Revision: 1}
	state.Workflow.Media = &api.MediaArtifactSetRef{ID: "media", Revision: 1}
	state.Media = map[api.MediaArtifactSetID]api.MediaArtifactSet{"media": {ID: "media", Revision: 1}}
}
