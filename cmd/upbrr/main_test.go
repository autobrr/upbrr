// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/configstore"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/webserver"
	"github.com/autobrr/upbrr/pkg/api"
)

type cliWorkflowCoreLifecycleFake struct {
	order                  []string
	shutdownCtxErr         error
	shutdownCtxHasDeadline bool
	shutdownErr            error
	closeErr               error
}

func (f *cliWorkflowCoreLifecycleFake) ShutdownWorkflowCoordinator(ctx context.Context) error {
	f.order = append(f.order, "shutdown")
	f.shutdownCtxErr = ctx.Err()
	_, f.shutdownCtxHasDeadline = ctx.Deadline()
	return f.shutdownErr
}

func (f *cliWorkflowCoreLifecycleFake) Close() error {
	f.order = append(f.order, "close")
	return f.closeErr
}

func TestCloseCLIWorkflowCoreShutsDownBeforeClosingRepository(t *testing.T) {
	fake := &cliWorkflowCoreLifecycleFake{shutdownErr: errors.New("synthetic shutdown")}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stderr strings.Builder
	closeCLIWorkflowCore(ctx, fake, &stderr)
	if len(fake.order) != 2 || fake.order[0] != "shutdown" || fake.order[1] != "close" {
		t.Fatalf("lifecycle order = %v", fake.order)
	}
	if fake.shutdownCtxErr != nil {
		t.Fatalf("shutdown context error = %v", fake.shutdownCtxErr)
	}
	if !fake.shutdownCtxHasDeadline {
		t.Fatal("shutdown context has no deadline")
	}
	if !strings.Contains(stderr.String(), "synthetic shutdown") {
		t.Fatalf("shutdown failure was not reported: %q", stderr.String())
	}
}

func TestCLIRejectsMultipleSourcesBeforeTrackCorrectionWork(t *testing.T) {
	for _, flags := range [][]string{
		{"--track-languages", "audio-1=English"},
		{"--reset-input", "metadata.track_languages:audio-1"},
	} {
		t.Run(flags[0], func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "must-not-create", "config.yaml")
			args := append(append([]string(nil), flags...), "--config", configPath, "first.mkv", "second.mkv")
			err := executeCLI(t.Context(), args, cliIO{})
			var exitErr *cliExitError
			if !errors.As(err, &exitErr) || exitErr.code != 2 || !strings.Contains(err.Error(), "exactly one source") {
				t.Fatalf("multi-source corrections reached setup: %v", err)
			}
			if _, err := os.Stat(filepath.Dir(configPath)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("rejected corrections initialized configuration")
			}
			args = append(append([]string(nil), flags...), "--queue", "example", "--config", configPath, "queue-root")
			if err := executeCLI(t.Context(), args, cliIO{}); err == nil || !strings.Contains(err.Error(), "exactly one source") {
				t.Fatalf("track correction allowed queue: %v", err)
			}
		})
	}
}

func TestLoadCLIConfigProvidedDoesNotOverwriteDurableConfig(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "state", "upbrr.db")
	stored := &config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "x", DBPath: dbPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	if err := configstore.SaveToDBPath(ctx, stored, dbPath); err != nil {
		t.Fatal(err)
	}
	provided := *stored
	provided.Metadata.KeepImages = true
	configPath := filepath.Join(t.TempDir(), "provided.yaml")
	if err := config.ExportToYAML(&provided, configPath); err != nil {
		t.Fatal(err)
	}
	loaded, resolvedDBPath, err := loadCLIConfig(ctx, configPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Metadata.KeepImages || resolvedDBPath != dbPath {
		t.Fatalf("provided runtime config = %#v, db path=%q", loaded.Metadata, resolvedDBPath)
	}
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	actual, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Metadata.KeepImages {
		t.Fatal("provided CLI config overwrote durable active configuration")
	}
}

func TestCLIConfigActivationRegistersFirstRunSeed(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "state", "upbrr.db")
	configPath := filepath.Join(t.TempDir(), "e2e-seed.yaml")
	seedConfig := &config.Config{
		MainSettings: config.MainSettingsConfig{
			TMDBAPI:           "e2e",
			DBPath:            dbPath,
			TrackerPassChecks: 1,
			InputHistoryLimit: 20,
		},
		ImageHosting:       config.ImageHostingConfig{Host1: "imgbb", ImgBBAPI: "e2e"},
		Metadata:           config.MetadataConfig{KeepImages: true},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1, MinSuccessfulUploads: 1},
		Logging:            config.LoggingConfig{Level: "debug", FileEnabled: false},
		Trackers:           config.TrackersConfig{Trackers: map[string]config.TrackerConfig{}},
		TorrentClients:     map[string]config.TorrentClientConfig{},
	}
	if err := config.ExportToYAML(seedConfig, configPath); err != nil {
		t.Fatal(err)
	}
	cfg, resolvedDBPath, seeded, err := loadCLIConfigWithSeed(ctx, configPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if !seeded || resolvedDBPath != dbPath {
		t.Fatalf("first-run config seeded=%t dbPath=%q", seeded, resolvedDBPath)
	}
	generation, fingerprint, err := cliConfigActivation(ctx, cfg, resolvedDBPath)
	if err != nil {
		t.Fatalf("register first-run config activation: %v", err)
	}
	if generation != 0 || fingerprint == "" {
		t.Fatalf("first-run config activation generation=%d fingerprint=%q", generation, fingerprint)
	}
}

func TestCLISeededConfigActivationMatchesServeAndLaterCLI(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "state", "upbrr.db")
	configPath := filepath.Join(t.TempDir(), "e2e-seed.yaml")
	configYAML := strings.Join([]string{
		"main_settings:",
		"  tmdb_api: \"e2e\"",
		"  tracker_pass_checks: 1",
		"  input_history_limit: 20",
		fmt.Sprintf("  db_path: %q", dbPath),
		"image_hosting:",
		"  img_host_1: \"imgbb\"",
		"  imgbb_api: \"e2e\"",
		"metadata:",
		"  keep_images: true",
		"screenshot_handling:",
		"  screens: 1",
		"  min_successful_image_uploads: 1",
		"logging:",
		"  level: \"debug\"",
		"  file_enabled: false",
		"trackers:",
		"  default_trackers: [\"BTN\"]",
		"  BTN:",
		"    api_key: \"e2e\"",
		"    username: \"e2e\"",
		"    password: \"e2e\"",
		"    url: \"http://127.0.0.1\"",
		"    image_host: \"imgbb\"",
		"torrent_clients: {}",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	seededConfig, resolvedDBPath, seeded, err := loadCLIConfigWithSeed(ctx, configPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if !seeded || resolvedDBPath != dbPath {
		t.Fatalf("initial CLI config seeded=%t dbPath=%q", seeded, resolvedDBPath)
	}
	_, seededFingerprint, err := cliConfigActivation(ctx, seededConfig, resolvedDBPath)
	if err != nil {
		t.Fatalf("register seeded CLI config: %v", err)
	}

	serveConfig, serveDBPath, err := loadServeConfig(ctx, configPath, true)
	if err != nil {
		t.Fatalf("load serve config after CLI seed: %v", err)
	}
	if serveDBPath != dbPath {
		t.Fatalf("serve DB path = %q, want %q", serveDBPath, dbPath)
	}
	serveFingerprint, err := config.EffectiveConfigFingerprint(serveConfig)
	if err != nil {
		t.Fatal(err)
	}
	if serveFingerprint != seededFingerprint {
		t.Fatal("serve config fingerprint does not match CLI seed")
	}

	laterCLIConfig, laterDBPath, laterSeeded, err := loadCLIConfigWithSeed(ctx, configPath, true)
	if err != nil {
		t.Fatalf("load later CLI config: %v", err)
	}
	if laterSeeded || laterDBPath != dbPath {
		t.Fatalf("later CLI config seeded=%t dbPath=%q", laterSeeded, laterDBPath)
	}
	if _, laterFingerprint, err := cliConfigActivation(ctx, laterCLIConfig, laterDBPath); err != nil {
		t.Fatalf("register later CLI config: %v", err)
	} else if laterFingerprint != seededFingerprint {
		t.Fatal("later CLI fingerprint does not match seed")
	}
	if err := executeCLI(ctx, []string{"--config", configPath, "--cleanup"}, cliIO{}); err != nil {
		t.Fatalf("run later CLI cleanup: %v", err)
	}
}

func TestCLIConfigActivationReconcilesDurableUpgrade(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "activation.db")
	cfg := &config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "synthetic-key", DBPath: dbPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	if err := configstore.SaveToDBPath(ctx, cfg, dbPath); err != nil {
		t.Fatal(err)
	}
	previous, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	previousFingerprint, err := config.EffectiveConfigFingerprint(*previous)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if _, err := repo.InitializeConfigActivationFingerprint(ctx, previousFingerprint); err != nil {
		t.Fatal(err)
	}
	previous.Metadata.KeepImages = !previous.Metadata.KeepImages
	if err := configstore.SaveToDBPath(ctx, previous, dbPath); err != nil {
		t.Fatal(err)
	}
	current, err := configstore.LoadFromDBPath(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := config.EffectiveConfigFingerprint(*current)
	if err != nil {
		t.Fatal(err)
	}
	if want == previousFingerprint {
		t.Fatal("test config did not change fingerprint")
	}
	generation, fingerprint, err := cliConfigActivation(ctx, *current, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if generation != 1 || fingerprint != want {
		t.Fatalf("CLI activation = generation %d fingerprint %q, want 1 and %q", generation, fingerprint, want)
	}
	changed := *current
	changed.Metadata.OnlyID = !current.Metadata.OnlyID
	if _, _, err := cliConfigActivation(ctx, changed, dbPath); !errors.Is(err, api.ErrConfigActivationChanged) {
		t.Fatalf("CLI runtime/stored mismatch error = %v", err)
	}
	repeatGeneration, repeatFingerprint, err := cliConfigActivation(ctx, *current, dbPath)
	if err != nil || repeatGeneration != 1 || repeatFingerprint != want {
		t.Fatalf("repeated CLI activation = generation %d fingerprint %q err %v", repeatGeneration, repeatFingerprint, err)
	}
}

func TestActivateImportedConfigCommitsNewGeneration(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "import.db")
	initial := &config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "initial", DBPath: dbPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	if err := configstore.SaveToDBPath(ctx, initial, dbPath); err != nil {
		t.Fatal(err)
	}
	imported := *initial
	imported.Metadata.KeepImages = true
	activation, err := activateImportedConfig(ctx, &imported, dbPath)
	if err != nil {
		t.Fatalf("activate imported config: %v", err)
	}
	if activation.Status != api.ConfigActivationActive || activation.ActiveGeneration != 1 || activation.Fingerprint == "" {
		t.Fatalf("import activation = %#v", activation)
	}
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	persisted, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.Metadata.KeepImages {
		t.Fatal("safe import did not persist config")
	}
}

func TestImportConfigUsesActivationTransaction(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "import-command.db")
	configPath := filepath.Join(t.TempDir(), "active.yaml")
	initial := &config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "initial", DBPath: dbPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	if err := configstore.SaveToDBPath(ctx, initial, dbPath); err != nil {
		t.Fatal(err)
	}
	if err := config.ExportToYAML(initial, configPath); err != nil {
		t.Fatal(err)
	}
	imported := *initial
	imported.Metadata.KeepImages = true
	importPath := filepath.Join(t.TempDir(), "import.yaml")
	if err := config.ExportToYAML(&imported, importPath); err != nil {
		t.Fatal(err)
	}
	if err := importConfig(ctx, importPath, configPath, true, cliIO{out: io.Discard, errOut: io.Discard}); err != nil {
		t.Fatalf("import config: %v", err)
	}
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	activation, err := repo.LoadConfigActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if activation.ActiveGeneration != 1 {
		t.Fatalf("import generation = %d, want 1", activation.ActiveGeneration)
	}
}

func TestActivateImportedConfigInvalidatesIdleActiveWorkflow(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "import-idle-workflow.db")
	initial := &config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "initial", DBPath: dbPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	if err := configstore.SaveToDBPath(ctx, initial, dbPath); err != nil {
		t.Fatal(err)
	}
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	release := api.ReleaseSnapshotRef{ID: "release", Revision: 1}
	state := releaseworkflow.State{
		OwnerID: "owner",
		Workflow: api.ReleaseWorkflow{
			ID:               "workflow",
			Revision:         1,
			FactInstructions: api.ReleaseFactInstructionSnapshotRef{ID: "facts", Revision: 1},
			Release:          &release,
			Status:           api.WorkflowStatusActive,
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `INSERT INTO release_workflow_states (
		owner_id, workflow_id, revision, status, creation_key, creation_fingerprint, state_json, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"owner", "workflow", 1, api.WorkflowStatusActive, "", "", payload, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `UPDATE active_input
		SET revision = 1, fence = 1, record_json = ?
		WHERE singleton = 1`,
		`{"State":"active","Revision":1,"Fence":1,"CoordinatorID":"test","OwnerID":"owner","WorkflowID":"workflow","InputID":"input","SourceVersion":"version"}`); err != nil {
		t.Fatal(err)
	}
	imported := *initial
	imported.Metadata.KeepImages = true
	activation, err := activateImportedConfig(ctx, &imported, dbPath)
	if err != nil {
		t.Fatalf("activate imported config: %v", err)
	}
	if activation.ActiveGeneration != 1 {
		t.Fatalf("import generation = %d, want 1", activation.ActiveGeneration)
	}
	var statePayload []byte
	var revision api.WorkflowRevision
	if err := repo.RawDB().QueryRowContext(ctx, `SELECT revision, state_json FROM release_workflow_states WHERE owner_id = ? AND workflow_id = ?`, "owner", "workflow").Scan(&revision, &statePayload); err != nil {
		t.Fatal(err)
	}
	if revision <= 1 {
		t.Fatalf("workflow revision = %d, want greater than 1", revision)
	}
	var updated releaseworkflow.State
	if err := json.Unmarshal(statePayload, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Workflow.Release != nil {
		t.Fatalf("provider impact retained current release: %#v", updated.Workflow)
	}
	active, err := repo.LoadActiveInput(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if active.Revision != 2 {
		t.Fatalf("active input revision = %d, want 2", active.Revision)
	}
}

func TestActivateImportedConfigRejectsBusyWithoutPersisting(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "import-busy.db")
	initial := &config.Config{
		MainSettings:       config.MainSettingsConfig{TMDBAPI: "initial", DBPath: dbPath},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
		Logging:            config.LoggingConfig{Level: "error"},
	}
	if err := configstore.SaveToDBPath(ctx, initial, dbPath); err != nil {
		t.Fatal(err)
	}
	repo, err := db.OpenContext(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RawDB().ExecContext(ctx, `UPDATE active_input SET record_json = ? WHERE singleton = 1`,
		`{"State":"opening","Revision":1,"Fence":1,"CoordinatorID":"test","OwnerID":"owner"}`); err != nil {
		t.Fatal(err)
	}
	imported := *initial
	imported.Metadata.KeepImages = true
	if _, err := activateImportedConfig(ctx, &imported, dbPath); !errors.Is(err, api.ErrActiveInputBusy) {
		t.Fatalf("busy import error = %v", err)
	}
	persisted, err := config.LoadFromDatabase(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Metadata.KeepImages {
		t.Fatal("busy import persisted config before activation")
	}
}
func TestCLIPreparationBatchOwnsPerSourceInstructions(t *testing.T) {
	t.Parallel()

	tmdbID := 1234567
	defaults := api.Request{
		ExternalIDOverrides: api.ExternalIDOverrides{TMDBID: &tmdbID},
		PlaylistInstruction: api.PlaylistInstruction{Set: true, Selected: []string{"00001.mpls"}},
	}
	batch := newCLIPreparationBatch(defaults, []string{"one", "two"})
	*defaults.ExternalIDOverrides.TMDBID = 7654321
	defaults.PlaylistInstruction.Selected[0] = "99999.mpls"
	*batch.items[0].externalIDs.TMDBID = 1111111
	batch.items[0].playlistInstruction.Selected[0] = "00002.mpls"

	if got := *batch.items[1].externalIDs.TMDBID; got != 1234567 {
		t.Fatalf("second item TMDB ID = %d, want detached value", got)
	}
	if got := batch.items[1].playlistInstruction.Selected[0]; got != "00001.mpls" {
		t.Fatalf("second item playlist = %q, want detached value", got)
	}
}

func TestProcessCLIPathsQueueModeContinuesOnError(t *testing.T) {
	t.Parallel()

	paths := []string{"a", "b", "c"}
	attempted := make([]string, 0, len(paths))
	err := processCLIPaths(context.Background(), paths, true, time.Minute, api.NopLogger{}, func(_ context.Context, sourcePath string) error {
		attempted = append(attempted, sourcePath)
		if sourcePath == "b" {
			return errors.New("boom")
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected a summary error when a queue item fails")
	}
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if len(attempted) != len(paths) {
		t.Fatalf("expected all %d items attempted in queue mode, got %v", len(paths), attempted)
	}
}

func TestProcessCLIPathsQueueModeSucceedsWhenAllPass(t *testing.T) {
	t.Parallel()

	if err := processCLIPaths(context.Background(), []string{"a", "b"}, true, time.Minute, api.NopLogger{}, func(context.Context, string) error {
		return nil
	}); err != nil {
		t.Fatalf("expected nil error when all queue items succeed, got %v", err)
	}
}

func TestProcessCLIPathsNonQueueAbortsOnFirstError(t *testing.T) {
	t.Parallel()

	paths := []string{"a", "b", "c"}
	attempted := 0
	err := processCLIPaths(context.Background(), paths, false, time.Minute, api.NopLogger{}, func(_ context.Context, sourcePath string) error {
		attempted++
		if sourcePath == "b" {
			return errors.New("boom")
		}
		return nil
	})
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if attempted != 2 {
		t.Fatalf("expected abort after the failing item (2 attempts), got %d", attempted)
	}
}

func TestProcessCLIPathsNonQueuePreservesCLIExitCode(t *testing.T) {
	t.Parallel()

	err := processCLIPaths(context.Background(), []string{"a"}, false, time.Minute, api.NopLogger{}, func(context.Context, string) error {
		return exitError(2, errors.New("required input is missing"))
	})
	exitErr, ok := errors.AsType[*cliExitError](err)
	if !ok || exitErr.code != 2 {
		t.Fatalf("expected cliExitError with code 2, got %v", err)
	}
}

func TestProcessCLIPathsAppliesPerItemTimeout(t *testing.T) {
	t.Parallel()

	paths := []string{"slow", "fast"}
	var slowDeadline time.Time
	var fastDeadline time.Time
	err := processCLIPaths(context.Background(), paths, true, 20*time.Millisecond, api.NopLogger{}, func(itemCtx context.Context, sourcePath string) error {
		if sourcePath == "slow" {
			slowDeadline, _ = itemCtx.Deadline()
			// Exceed the per-item timeout; this item should be cancelled at
			// ~20ms rather than running to completion.
			select {
			case <-itemCtx.Done():
				return itemCtx.Err()
			case <-time.After(2 * time.Second):
				return nil
			}
		}
		// The next item must receive a fresh per-item deadline, proving the
		// timeout is not shared across the queue.
		fastDeadline, _ = itemCtx.Deadline()
		return nil
	})
	if err == nil {
		t.Fatal("expected a summary error because the slow item timed out")
	}
	if slowDeadline.IsZero() || fastDeadline.IsZero() || !fastDeadline.After(slowDeadline) {
		t.Fatalf("expected the second item to get a later per-item deadline: slow=%v fast=%v", slowDeadline, fastDeadline)
	}
}

// --- GROUP B #5: the per-item context is threaded through to core calls ---

func TestProcessCLIPathsAbortsOnParentCancel(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	paths := []string{"a", "b", "c", "d"}
	attempted := 0
	err := processCLIPaths(parent, paths, true, time.Minute, api.NopLogger{}, func(_ context.Context, sourcePath string) error {
		attempted++
		// Cancel the parent mid-run; remaining items must NOT be attempted as
		// spurious per-item failures.
		if sourcePath == "b" {
			cancel()
		}
		return nil
	})
	if err == nil {
		t.Fatal("expected an abort error after parent cancellation")
	}
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected wrapped context.Canceled, got %v", err)
	}
	if attempted != 2 {
		t.Fatalf("expected the queue to stop after the cancel (2 attempts), got %d", attempted)
	}
}

func TestProcessCLIPathsAbortsWhenLastItemCanceled(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	paths := []string{"only"}
	err := processCLIPaths(parent, paths, true, time.Minute, api.NopLogger{}, func(_ context.Context, _ string) error {
		// Parent cancellation during the final item must abort, not be recorded as
		// a normal queue failure (no next iteration runs to catch it).
		cancel()
		return context.Canceled
	})
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected wrapped context.Canceled, got %v", err)
	}
	if strings.Contains(err.Error(), "queue completed") {
		t.Fatalf("expected an abort error, not a normal queue summary, got %v", err)
	}
}

func TestProcessCLIPathsItemTimeoutDoesNotAbortQueue(t *testing.T) {
	t.Parallel()

	// A per-item timeout (on the derived itemCtx) must be treated as an ordinary
	// failure that continues the queue, not as a parent-cancellation abort.
	paths := []string{"slow", "ok"}
	attempted := 0
	err := processCLIPaths(context.Background(), paths, true, 10*time.Millisecond, api.NopLogger{}, func(itemCtx context.Context, sourcePath string) error {
		attempted++
		if sourcePath == "slow" {
			<-itemCtx.Done()
			return itemCtx.Err()
		}
		return nil
	})
	if attempted != len(paths) {
		t.Fatalf("expected all items attempted despite an item timeout, got %d", attempted)
	}
	if err == nil || !strings.Contains(err.Error(), "queue completed") {
		t.Fatalf("expected a normal queue summary error, got %v", err)
	}
	if strings.Contains(err.Error(), "aborted after") {
		t.Fatalf("item timeout must not abort the queue, got %v", err)
	}
}

func TestParseCLIOptionsCreateAuth(t *testing.T) {
	t.Parallel()

	opts, visited, paths, err := parseCLIOptions([]string{"--create-auth"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !opts.CreateAuth {
		t.Fatalf("expected create-auth to parse, got %#v", opts)
	}
	if !visited["create-auth"] {
		t.Fatalf("expected create-auth visited flag, got %#v", visited)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no positional paths, got %#v", paths)
	}
}

func TestCreateCLIAuthFileSuccess(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	input := strings.NewReader("tester\nvery-secure-password\nvery-secure-password\n")

	var output strings.Builder
	if err := createCLIAuthFile(input, &output, dbPath); err != nil {
		t.Fatalf("createCLIAuthFile: %v", err)
	}

	authPath := webserver.AuthFilePath(dbPath)
	raw, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth file: %v", err)
	}
	if !strings.Contains(string(raw), `"username": "tester"`) {
		t.Fatalf("expected username in auth file, got %s", raw)
	}
	if strings.Contains(string(raw), "very-secure-password") {
		t.Fatalf("auth file leaked plaintext password: %s", raw)
	}
	if got := output.String(); !strings.Contains(got, "Username: ") || !strings.Contains(got, "Password: ") {
		t.Fatalf("expected prompts in output, got %q", got)
	}
}

func TestCreateCLIAuthFileRefusesOverwrite(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	if err := webserver.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	input := strings.NewReader("tester\nvery-secure-password\nvery-secure-password\n")
	var output strings.Builder
	err := createCLIAuthFile(input, &output, dbPath)
	if err == nil || !strings.Contains(err.Error(), "user already exists") {
		t.Fatalf("expected existing auth file error, got %v", err)
	}
}

func TestCreateCLIAuthFileRejectsShortPassword(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	input := strings.NewReader("tester\nshortpass\nshortpass\n")

	var output strings.Builder
	err := createCLIAuthFile(input, &output, dbPath)
	if err == nil {
		t.Fatal("expected short password validation error")
	}
	if !strings.Contains(err.Error(), "create auth: password too short") {
		t.Fatalf("unexpected error for short password: %v", err)
	}
}

func TestRunCreateAuthUsesConfiguredDBPath(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "custom", "upbrr.db")
	body := "main_settings:\n  db_path: " + dbPath + "\nscreenshot_handling:\n  screens: 1\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	input := strings.NewReader("tester\nvery-secure-password\nvery-secure-password\n")
	var output strings.Builder
	if err := executeCLI(t.Context(), []string{"--create-auth", "--config", configPath}, cliIO{in: input, out: &output}); err != nil {
		t.Fatalf("run: %v", err)
	}

	if _, err := os.Stat(webserver.AuthFilePath(dbPath)); err != nil {
		t.Fatalf("expected auth file beside configured db path: %v", err)
	}
}

func TestRunRejectsCreateAuthConflicts(t *testing.T) {
	err := executeCLI(t.Context(), []string{"--create-auth", "--export-config", "out.yaml"}, cliIO{})
	var cliErr *cliExitError
	if !errors.As(err, &cliErr) {
		t.Fatalf("expected cliExitError, got %v", err)
	}
	if cliErr.code != 2 {
		t.Fatalf("expected exit code 2, got %d", cliErr.code)
	}
	if !strings.Contains(cliErr.Error(), "--create-auth and --export-config cannot be used together") {
		t.Fatalf("unexpected error: %v", cliErr)
	}
}

func TestRunHelpFlagsPrintUsageAndSucceed(t *testing.T) {
	for _, helpFlag := range []string{"-help", "--help", "-h", "--h"} {
		t.Run(helpFlag, func(t *testing.T) {
			var output strings.Builder
			if err := executeCLI(t.Context(), []string{helpFlag}, cliIO{out: &output}); err != nil {
				t.Fatalf("run: %v", err)
			}
			text := output.String()
			if !strings.Contains(text, "Usage: upbrr [options] <input path>...") {
				t.Fatalf("expected top-level usage in output, got %q", text)
			}
			for _, expected := range []string{
				"Commands:",
				"  serve [options]",
				"Start the embedded web UI server",
				"Options: --addr, --host, --port, --base-url, --persist-web-config, --dev-no-auth",
				"Config:",
				"Execution:",
				"Tracker Selection:",
				"Release Overrides:",
				"Screenshots and Images:",
				"-config, --config string",
				"-limit-queue, --limit-queue, -lq int",
				"-version, --version",
			} {
				if !strings.Contains(text, expected) {
					t.Fatalf("expected output to contain %q, got %q", expected, text)
				}
			}
		})
	}
}

func TestRunServeHelpPrintsUsageAndSucceeds(t *testing.T) {
	var output strings.Builder
	if err := executeCLI(t.Context(), []string{"serve", "--help"}, cliIO{out: &output}); err != nil {
		t.Fatalf("run: %v", err)
	}
	text := output.String()
	if !strings.Contains(text, "Usage: upbrr serve [options]") {
		t.Fatalf("expected serve usage in output, got %q", text)
	}
	for _, expected := range []string{"Config:", "Server:", "Development:", "-config, --config string", "-addr, --addr string", "-host, --host string", "-port, --port int", "-base-url, --base-url string", "-persist-listen, --persist-listen", "-persist-web-config, --persist-web-config", "-dev-no-auth, --dev-no-auth"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, text)
		}
	}
}

func TestRunServePersistListenRequiresListenOverride(t *testing.T) {
	err := executeCLI(t.Context(), []string{"serve", "--persist-listen"}, cliIO{})
	if err == nil || !strings.Contains(err.Error(), "--persist-listen requires --addr, --host, or --port") {
		t.Fatalf("expected persist-listen requirement error, got %v", err)
	}
}

func TestRunServePersistWebConfigRequiresWebConfigOverride(t *testing.T) {
	err := executeCLI(t.Context(), []string{"serve", "--persist-web-config"}, cliIO{})
	if err == nil || !strings.Contains(err.Error(), "--persist-web-config requires --addr, --host, --port, --base-url, or UPBRR_WEB_* env") {
		t.Fatalf("expected persist-web-config requirement error, got %v", err)
	}
}

func TestServePersistConfigListenOnlyClearsStoredBaseURL(t *testing.T) {
	stored := webserver.CLIConfig{
		Host:           "localhost",
		Port:           7480,
		OpenBrowser:    true,
		TrustedProxies: []string{"127.0.0.1"},
		BaseURL:        "/stored/",
		SessionTTL:     1440,
	}
	runtime := stored
	runtime.Host = "0.0.0.0"
	runtime.Port = 9090
	runtime.BaseURL = "/temporary/"

	persisted := servePersistConfig(stored, runtime, map[string]bool{
		"persist-listen": true,
		"addr":           true,
		"base-url":       true,
	})
	if persisted.Host != "0.0.0.0" || persisted.Port != 9090 {
		t.Fatalf("listen settings not persisted: %#v", persisted)
	}
	if persisted.BaseURL != "" {
		t.Fatalf("base url persisted during listen-only save: %#v", persisted)
	}
	if !persisted.OpenBrowser || persisted.SessionTTL != 1440 || len(persisted.TrustedProxies) != 1 || persisted.TrustedProxies[0] != "127.0.0.1" {
		t.Fatalf("unrelated web config changed: %#v", persisted)
	}
}

func TestServePersistConfigWebConfigPersistsBaseURL(t *testing.T) {
	stored := webserver.CLIConfig{
		Host:    "localhost",
		Port:    7480,
		BaseURL: "/stored/",
	}
	runtime := stored
	runtime.Host = "0.0.0.0"
	runtime.Port = 9090
	runtime.BaseURL = "/explicit/"

	persisted := servePersistConfig(stored, runtime, map[string]bool{
		"persist-listen":     true,
		"persist-web-config": true,
		"addr":               true,
		"base-url":           true,
	})
	if persisted.Host != "0.0.0.0" || persisted.Port != 9090 || persisted.BaseURL != "/explicit/" {
		t.Fatalf("explicit web config not persisted: %#v", persisted)
	}
}

func TestServePersistConfigListenOnlyPersistsExplicitListenFields(t *testing.T) {
	stored := webserver.CLIConfig{
		Host:    "localhost",
		Port:    7480,
		BaseURL: "/stored/",
	}
	runtime := webserver.CLIConfig{
		Host:    "0.0.0.0",
		Port:    9090,
		BaseURL: "/env/",
	}

	persisted := servePersistConfig(stored, runtime, map[string]bool{"persist-listen": true, "port": true})
	if persisted.Host != "localhost" || persisted.Port != 9090 || persisted.BaseURL != "" {
		t.Fatalf("listen persistence included env/transient fields: %#v", persisted)
	}
}

func TestServePersistConfigListenOnlyIgnoresInvalidStoredBaseURLWhenSaving(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	stored := webserver.CLIConfig{
		Host:    "localhost",
		Port:    7480,
		BaseURL: "javascript:alert(1)",
	}
	runtime := webserver.CLIConfig{
		Host:    "127.0.0.1",
		Port:    9090,
		BaseURL: "javascript:alert(1)",
	}

	persisted := servePersistConfig(stored, runtime, map[string]bool{"persist-listen": true, "host": true})
	if err := webserver.SaveCLIConfig(dbPath, persisted); err != nil {
		t.Fatalf("SaveCLIConfig with listen-only config: %v", err)
	}

	saved, err := webserver.LoadCLIConfig(dbPath)
	if err != nil {
		t.Fatalf("LoadCLIConfig: %v", err)
	}
	if saved.Host != "127.0.0.1" || saved.Port != 7480 || saved.BaseURL != "" {
		t.Fatalf("saved listen-only config = %#v", saved)
	}
}

func TestRunServeRejectedDevelopmentNoAuthHostDoesNotPersistListenOverride(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	body := "main_settings:\n  db_path: " + filepath.ToSlash(dbPath) + "\nscreenshot_handling:\n  screens: 1\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := executeCLI(t.Context(), []string{"serve", "--config", configPath, "--dev-no-auth", "--host", "0.0.0.0", "--persist-listen"}, cliIO{})
	if err == nil || !strings.Contains(err.Error(), "--dev-no-auth requires a loopback host") {
		t.Fatalf("expected dev-no-auth loopback error, got %v", err)
	}

	cfg, err := webserver.LoadCLIConfig(dbPath)
	if err != nil {
		t.Fatalf("load web config: %v", err)
	}
	if cfg.Host != "localhost" || cfg.Port != 7480 {
		t.Fatalf("rejected listen override persisted: %#v", cfg)
	}
}

func TestRunServePersistListenBindFailureDoesNotWriteWebConfig(t *testing.T) {
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fixture: %v", err)
	}
	defer listener.Close()

	tmpDir := t.TempDir()
	distPath := filepath.Join(tmpDir, "webui", "dist")
	if err := os.MkdirAll(distPath, 0o755); err != nil {
		t.Fatalf("create web assets fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(distPath, "index.html"), []byte("<!doctype html><title>upbrr</title>"), 0o600); err != nil {
		t.Fatalf("write web assets fixture: %v", err)
	}
	t.Chdir(tmpDir)

	configPath := filepath.Join(tmpDir, "config.yaml")
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	body := "main_settings:\n  db_path: " + filepath.ToSlash(dbPath) + "\nscreenshot_handling:\n  screens: 1\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	err = executeCLI(t.Context(), []string{"serve", "--config", configPath, "--dev-no-auth", "--host", "127.0.0.1", "--port", port, "--persist-listen"}, cliIO{})
	if err == nil || !strings.Contains(err.Error(), "webserver: listen") {
		t.Fatalf("expected listen error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dbPath), "web-config.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no persisted web config after bind failure, stat error: %v", err)
	}
}

func TestRunWithoutArgsStillRequiresInputPath(t *testing.T) {
	err := executeCLI(t.Context(), []string{}, cliIO{})
	var cliErr *cliExitError
	if !errors.As(err, &cliErr) {
		t.Fatalf("expected cliExitError, got %v", err)
	}
	if cliErr.code != 2 {
		t.Fatalf("expected exit code 2, got %d", cliErr.code)
	}
	if !strings.Contains(cliErr.Error(), "at least one input path is required") {
		t.Fatalf("unexpected error: %v", cliErr)
	}
}

func TestRunExportConfigPlaintextExportsPlainSecrets(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "state", "upbrr.db")
	configPath := filepath.Join(tmpDir, "config.yaml")
	outputPath := filepath.Join(tmpDir, "export.yaml")

	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(context.Background()); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	if err := webserver.BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}

	cfg := &config.Config{
		MainSettings: config.MainSettingsConfig{
			DBPath:  dbPath,
			TMDBAPI: "plain-tmdb-token",
		},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1},
	}
	if err := config.ExportToYAML(cfg, configPath); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := config.SaveToDatabase(context.Background(), cfg, repo); err != nil {
		t.Fatalf("save config: %v", err)
	}

	if err := executeCLI(t.Context(), []string{"--config", configPath, "--export-config", outputPath, "--export-config-plaintext"}, cliIO{}); err != nil {
		t.Fatalf("run: %v", err)
	}

	raw, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	exported := string(raw)
	if !strings.Contains(exported, "plain-tmdb-token") {
		t.Fatalf("expected plaintext secret in export, got %s", exported)
	}
	if strings.Contains(exported, "upbrr-enc:v1:") {
		t.Fatalf("expected plaintext export without encrypted envelopes, got %s", exported)
	}
}

// --- GROUP D #7/#8: queue summary error names quoted paths and wraps first cause ---

func TestProcessCLIPathsQueueModeErrorIncludesQuotedFailedPathsAndWrapsCause(t *testing.T) {
	t.Parallel()

	boomFirst := errors.New("first boom")
	boomThird := errors.New("third boom")
	paths := []string{"a,b", "good", "c d"}
	err := processCLIPaths(context.Background(), paths, true, time.Minute, api.NopLogger{}, func(_ context.Context, sourcePath string) error {
		switch sourcePath {
		case "a,b":
			return boomFirst
		case "c d":
			return boomThird
		default:
			return nil
		}
	})
	if err == nil {
		t.Fatal("expected a summary error when queue items fail")
	}
	var exitErr *cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 1 {
		t.Fatalf("expected cliExitError with code 1, got %v", err)
	}
	if !errors.Is(err, boomFirst) {
		t.Fatalf("expected wrapped error to be the FIRST cause, got %v", err)
	}
	if errors.Is(err, boomThird) {
		t.Fatalf("did not expect the later cause to be wrapped, got %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, `"a,b"`) || !strings.Contains(msg, `"c d"`) {
		t.Fatalf("expected quoted failed paths in error message, got %q", msg)
	}
	if !strings.Contains(msg, "2 of 3") {
		t.Fatalf("expected count substring in error message, got %q", msg)
	}
}

// --- GROUP B #5: runCLIPathWithTimeout per-item deadline and parent cancel propagation ---

func TestRunCLIPathWithTimeoutAppliesPerItemDeadline(t *testing.T) {
	t.Parallel()

	err := runCLIPathWithTimeout(context.Background(), 20*time.Millisecond, "p", func(itemCtx context.Context, _ string) error {
		<-itemCtx.Done()
		return itemCtx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestRunCLIPathWithTimeoutPropagatesParentCancel(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	cancel()
	err := runCLIPathWithTimeout(parent, time.Hour, "p", func(itemCtx context.Context, _ string) error {
		return itemCtx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected Canceled to propagate from parent, got %v", err)
	}
}

// --- GROUP A: upload-only queue continue-on-error + bounded-timeout batch ---
