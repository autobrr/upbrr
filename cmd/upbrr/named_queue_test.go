// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func queueFixture(t *testing.T) (string, string, []string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "sources")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 4)
	for i, name := range []string{"a.mkv", "b.mkv", "c.mkv", "d.mkv"} {
		paths[i] = filepath.Join(root, name)
		if err := os.WriteFile(paths[i], []byte("synthetic media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(base, "db.sqlite"), root, paths
}

func TestNamedQueueProcessKill(t *testing.T) {
	if dbPath := os.Getenv("UPBRR_QUEUE_CHILD_DB"); dbPath != "" {
		queue, err := openCLINamedQueue(t.Context(), dbPath, "killed", os.Getenv("UPBRR_QUEUE_CHILD_ROOT"), false, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		batch := newCLIPreparationBatch(api.Request{}, queue.paths(false, 0))
		batch.queue = queue
		calls := 0
		err = processCLIPreparationItems(t.Context(), batch, true, time.Minute, cliIO{}, api.NopLogger{}, func(context.Context, cliPreparationItem) (cliQueueOutcome, error) {
			calls++
			if calls == 3 {
				fmt.Fprintln(os.Stdout, "ready")
				_, _ = io.Copy(io.Discard, os.Stdin)
			}
			return cliQueueOutcome{Status: "completed"}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	dbPath, root, paths := queueFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNamedQueueProcessKill$")
	child.Env = append(os.Environ(), "UPBRR_QUEUE_CHILD_DB="+dbPath, "UPBRR_QUEUE_CHILD_ROOT="+root)
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	if ready, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || ready != "ready\n" {
		_ = child.Process.Kill()
		_ = child.Wait()
		t.Fatalf("child did not reach checkpoint: %q %v %s", ready, err, stderr.String())
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("child was not terminated")
	}
	queue, err := openCLINamedQueue(t.Context(), dbPath, "killed", root, false, io.Discard)
	if err != nil {
		t.Fatalf("crashed process retained lock or corrupt checkpoint: %v", err)
	}
	defer queue.Close()
	want := []string{"completed", "completed", "interrupted", "pending"}
	for i, item := range queue.inventory.Items {
		if item.Status != want[i] {
			t.Fatalf("item %d: %+v", i, item)
		}
	}
	if got := queue.paths(false, 1); !reflect.DeepEqual(got, paths[3:]) {
		t.Fatalf("restart did not advance: %v", got)
	}
}

func TestNamedQueueCheckpointFailureStopsBeforeMoreWork(t *testing.T) {
	for _, phase := range []string{"before item", "after item"} {
		t.Run(phase, func(t *testing.T) {
			dbPath, root, paths := queueFixture(t)
			queue, err := openCLINamedQueue(t.Context(), dbPath, "save-failure", root, false, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer queue.Close()
			batch := newCLIPreparationBatch(api.Request{}, paths)
			batch.queue = queue
			// A failed replacement must leave the previous checkpoint intact.
			savedPath := queue.path
			blockSave := func() {
				queue.path = filepath.Dir(savedPath)
			}
			if phase == "before item" {
				blockSave()
			}
			calls := 0
			err = processCLIPreparationItems(t.Context(), batch, true, time.Minute, cliIO{}, api.NopLogger{}, func(context.Context, cliPreparationItem) (cliQueueOutcome, error) {
				calls++
				blockSave()
				return cliQueueOutcome{Status: "completed"}, nil
			})
			if _, ok := errors.AsType[*cliQueueCheckpointError](err); !ok {
				t.Fatalf("save failure not terminal: %v", err)
			}
			if calls > 1 || phase == "before item" && calls != 0 {
				t.Fatalf("checkpoint failure allowed %d operations", calls)
			}
			payload, err := os.ReadFile(savedPath)
			if err != nil {
				t.Fatal(err)
			}
			var retained cliQueueInventory
			if err := json.Unmarshal(payload, &retained); err != nil {
				t.Fatalf("failed save corrupted prior checkpoint: %v", err)
			}
			if len(retained.Items) != len(paths) || retained.Items[1].Status != "pending" {
				t.Fatalf("failed save lost progress: %+v", retained)
			}
			if got, want := queue.inventory.Items[0], retained.Items[0]; got.Status != want.Status || got.Attempts != want.Attempts || !got.UpdatedAt.Equal(want.UpdatedAt) {
				t.Fatalf("failed replacement left unpersisted progress in memory: got=%+v want=%+v", got, want)
			}
		})
	}
}

func TestNamedQueueSyncFailuresRetainPublishedStateAndStopWork(t *testing.T) {
	for _, phase := range []string{"hierarchy", "initial checkpoint", "before item", "after item"} {
		t.Run(phase, func(t *testing.T) {
			dbPath, root, paths := queueFixture(t)
			dbPath = filepath.Join(filepath.Dir(dbPath), "new-data", "nested", "db.sqlite")
			fault := errors.New("injected sync failure")
			var events []string
			var savedPath string
			checks := 0
			syncs := cliQueueSync{
				hierarchy: func(dir string) error {
					events = append(events, "hierarchy")
					if info, err := os.Stat(dir); err != nil || !info.IsDir() {
						t.Fatalf("hierarchy sync preceded directory creation: %v", err)
					}
					if phase == "hierarchy" {
						return fault
					}
					return syncCLIQueueHierarchy(dir)
				},
				checkpoint: func(path string) error {
					checks++
					savedPath = path
					payload, err := os.ReadFile(path)
					if err != nil {
						t.Fatalf("sync preceded replacement: %v", err)
					}
					var current cliQueueInventory
					if err := json.Unmarshal(payload, &current); err != nil {
						t.Fatalf("sync observed incomplete checkpoint: %v", err)
					}
					events = append(events, current.Items[0].Status)
					if phase == "initial checkpoint" && checks == 1 || phase == "before item" && checks == 2 || phase == "after item" && checks == 3 {
						return fault
					}
					return syncCLIQueueCheckpoint(path)
				},
			}
			var output bytes.Buffer
			queue, err := openCLINamedQueueWithSync(t.Context(), dbPath, "sync-failure", root, false, &output, syncs)
			if phase == "hierarchy" || phase == "initial checkpoint" {
				if !errors.Is(err, fault) || queue != nil || output.Len() != 0 {
					t.Fatalf("failed initialization admitted queue: queue=%v err=%v output=%s", queue, err, output.String())
				}
				if phase == "hierarchy" {
					if checks != 0 || !reflect.DeepEqual(events, []string{"hierarchy"}) {
						t.Fatalf("failed hierarchy sync published a checkpoint: %v", events)
					}
					return
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				bridge := newCLITUIBridge()
				streams := cliIO{out: &cliPresentationWriter{Writer: io.Discard, presenter: bridge}, presenter: bridge}
				batch := newCLIPreparationBatch(api.Request{}, paths)
				batch.queue = queue
				calls := 0
				err = processCLIPreparationItems(t.Context(), batch, true, time.Minute, streams, api.NopLogger{}, func(context.Context, cliPreparationItem) (cliQueueOutcome, error) {
					calls++
					events = append(events, "work")
					return cliQueueOutcome{Status: "completed"}, nil
				})
				if _, ok := errors.AsType[*cliQueueCheckpointError](err); !ok || !errors.Is(err, fault) {
					t.Fatalf("sync failure not terminal: %v", err)
				}
				wantCalls := 0
				if phase == "after item" {
					wantCalls = 1
				}
				if calls != wantCalls || !bridge.latest.ItemFailed || strings.Contains(bridge.latest.Result, "Item completed") {
					t.Fatalf("sync failure admitted work or success: calls=%d result=%+v", calls, bridge.latest)
				}
				var progress bytes.Buffer
				queue.printProgress(&progress)
				if !strings.Contains(progress.String(), "durability is unconfirmed") || strings.Contains(progress.String(), " completed") {
					t.Fatalf("sync failure advertised durable progress: %s", progress.String())
				}
				if err := queue.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if !strings.Contains(err.Error(), "replaced") || !strings.Contains(err.Error(), "durability is unconfirmed") {
				t.Fatalf("post-rename failure implied no write: %v", err)
			}
			wantEvents := []string{"hierarchy", "pending"}
			wantStatus := "pending"
			if phase != "initial checkpoint" {
				wantEvents = append(wantEvents, "running")
				wantStatus = "interrupted"
			}
			if phase == "after item" {
				wantEvents = append(wantEvents, "work", "completed")
				wantStatus = "completed"
			}
			if !reflect.DeepEqual(events, wantEvents) {
				t.Fatalf("publication was reordered or retried: %v", events)
			}
			if _, err := os.Stat(savedPath); err != nil {
				t.Fatalf("post-rename failure removed destination: %v", err)
			}
			resumed, err := openCLINamedQueue(t.Context(), dbPath, "sync-failure", root, false, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer resumed.Close()
			if resumed.inventory.Items[0].Status != wantStatus {
				t.Fatalf("resume discarded published state: %+v", resumed.inventory.Items[0])
			}
			if got := resumed.paths(false, 1); phase != "initial checkpoint" && !reflect.DeepEqual(got, paths[1:2]) {
				t.Fatalf("resume blindly retried published work: %v", got)
			}
		})
	}
}

func TestCLIQueueAncestorSyncIncludesPreviouslyCreatedParents(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "data", "nested", "queues")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var visited []string
	fault := errors.New("parent sync failed")
	err = syncCLIQueueAncestors(dir, func(path string) error {
		visited = append(visited, path)
		if path == base {
			return fault
		}
		return nil
	})
	want := []string{dir, filepath.Dir(dir), filepath.Join(base, "data"), base}
	if !errors.Is(err, fault) || !reflect.DeepEqual(visited, want) {
		t.Fatalf("ancestor sync lost an earlier-created parent or continued after failure: visited=%v err=%v", visited, err)
	}
}

func TestCLIQueueAncestorSyncFollowsDataRootSymlink(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	physical := filepath.Join(base, "physical")
	dir := filepath.Join(physical, "data", "queues")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Join(physical, "data"), alias); err != nil {
		t.Skipf("filesystem does not permit the symlink fixture: %v", err)
	}
	var visited []string
	fault := errors.New("physical parent sync failed")
	err = syncCLIQueueAncestors(filepath.Join(alias, "queues"), func(path string) error {
		visited = append(visited, path)
		if path == physical {
			return fault
		}
		return nil
	})
	if !errors.Is(err, fault) || !reflect.DeepEqual(visited, []string{dir, filepath.Join(physical, "data"), physical}) {
		t.Fatalf("physical ancestor was not synchronized: visited=%v err=%v", visited, err)
	}
}

func TestNamedQueueCLIControlsAndCompletedMissingRoot(t *testing.T) {
	for _, args := range [][]string{{"--refresh-queue", "source"}, {"--retry-queue=false", "source"}, {"--queue=q", "--delete-tmp", "source"}} {
		result := executeCLIForTest(t.Context(), t, args)
		if result.code != 2 || !strings.Contains(result.stderr, "queue") {
			t.Fatalf("invalid queue controls: %+v", result)
		}
	}
	dbPath, root, _ := queueFixture(t)
	queue, err := openCLINamedQueue(t.Context(), dbPath, "done", root, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	for i := range queue.inventory.Items {
		queue.inventory.Items[i].Status = "completed"
	}
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		MainSettings: config.MainSettingsConfig{
			DBPath:            dbPath,
			TMDBAPI:           "synthetic",
			InputHistoryLimit: 20,
			TrackerPassChecks: 1,
		},
		ImageHosting:       config.ImageHostingConfig{Host1: "imgbb", ImgBBAPI: "synthetic"},
		ScreenshotHandling: config.ScreenshotHandlingConfig{Screens: 1, MinSuccessfulUploads: 1},
		Logging:            config.LoggingConfig{Level: "error"},
		Trackers:           config.TrackersConfig{Trackers: map[string]config.TrackerConfig{}},
		TorrentClients:     map[string]config.TorrentClientConfig{},
	}
	configPath := filepath.Join(filepath.Dir(dbPath), "config.yaml")
	if err := config.ExportToYAML(cfg, configPath); err != nil {
		t.Fatal(err)
	}
	result := executeCLIForTest(t.Context(), t, []string{"--config", configPath, "--ui=plain", "--queue=done", root})
	if result.err != nil || !strings.Contains(result.stdout, "Queue complete") || !strings.Contains(result.stdout, fmt.Sprintf("%q", queue.path)) {
		t.Fatalf("completed queue required missing source: %+v", result)
	}
}

func TestNamedQueueResumesRemainingLimitsAndRefreshesExplicitly(t *testing.T) {
	dbPath, root, paths := queueFixture(t)
	var output bytes.Buffer
	open := func(name string, refresh bool) *cliNamedQueue {
		t.Helper()
		queue, err := openCLINamedQueue(t.Context(), dbPath, name, root, refresh, &output)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = queue.Close() })
		return queue
	}
	queue := open("daily / queue", false)
	if filepath.Dir(queue.path) != filepath.Join(filepath.Dir(dbPath), "queues") || !strings.Contains(output.String(), fmt.Sprintf("%q", queue.path)) {
		t.Fatalf("queue location not discoverable: %s %s", queue.path, output.String())
	}
	if got := queue.paths(false, 2); !reflect.DeepEqual(got, paths[:2]) {
		t.Fatalf("first limit: %v", got)
	}
	batch := newCLIPreparationBatch(api.Request{}, queue.paths(false, 2))
	batch.queue = queue
	err := processCLIPreparationItems(t.Context(), batch, true, time.Minute, cliIO{}, api.NopLogger{}, func(context.Context, cliPreparationItem) (cliQueueOutcome, error) {
		return cliQueueOutcome{Status: "completed"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	added := filepath.Join(root, "e.mkv")
	if err := os.WriteFile(added, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	queue = open("daily / queue", false)
	if got := queue.paths(false, 2); !reflect.DeepEqual(got, paths[2:]) {
		t.Fatalf("resume reapplied limit before progress: %v", got)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	queue = open("daily / queue", true)
	if got := queue.paths(false, 0); !reflect.DeepEqual(got, append(paths[2:], added)) {
		t.Fatalf("refresh lost progress: %v", got)
	}
	independent := open("daily _ queue", false)
	if independent.path == queue.path || len(independent.paths(false, 0)) != 5 {
		t.Fatal("queue names shared progress")
	}
	var saved cliQueueInventory
	payload, err := os.ReadFile(queue.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Name != "daily / queue" || saved.Root != root || len(saved.Items) != 5 || saved.Items[0].Status != "completed" {
		t.Fatalf("saved inventory lost membership or progress: %+v", saved)
	}
}

func TestNamedQueueMixedOutcomesAndInterruptedRestart(t *testing.T) {
	dbPath, root, paths := queueFixture(t)
	queue, err := openCLINamedQueue(t.Context(), dbPath, "mixed", root, false, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	batch := newCLIPreparationBatch(api.Request{}, paths)
	batch.queue = queue
	failed := errors.New("synthetic failure")
	err = processCLIPreparationItems(parent, batch, true, time.Minute, cliIO{}, api.NopLogger{}, func(_ context.Context, item cliPreparationItem) (cliQueueOutcome, error) {
		switch item.originalPath {
		case paths[0]:
			return cliQueueOutcome{Status: "completed", ConfirmedUploads: []api.TrackerID{"ALPHA"}}, failed
		case paths[1]:
			return cliQueueOutcome{Status: "skipped"}, nil
		case paths[2]:
			return cliQueueOutcome{Status: "completed"}, nil
		default:
			cancel()
			return cliQueueOutcome{}, nil
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation did not abort: %v", err)
	}
	want := []string{"failed", "skipped", "completed", "interrupted"}
	for i, item := range queue.inventory.Items {
		if item.Status != want[i] || item.Attempts != 1 {
			t.Fatalf("item %d: %+v", i, item)
		}
	}
	if !reflect.DeepEqual(queue.inventory.Items[0].ConfirmedUploads, []api.TrackerID{"ALPHA"}) {
		t.Fatal("partial confirmed upload lost")
	}
	// Simulate termination after the durable running checkpoint, before a result.
	queue.inventory.Items[3].Status = "running"
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	queue, err = openCLINamedQueue(t.Context(), dbPath, "mixed", root, false, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	if got := queue.paths(false, 0); len(got) != 0 {
		t.Fatalf("resume automatically retried uncertain/failed work: %v", got)
	}
	if got := queue.paths(true, 0); !reflect.DeepEqual(got, []string{paths[0], paths[1], paths[3]}) {
		t.Fatalf("retry inventory: %v", got)
	}
	if queue.inventory.Items[3].Status != "interrupted" {
		t.Fatal("unfinished attempt was not retained as interrupted")
	}
}

func TestNamedQueueRejectsConcurrentOwnerAndCorruptInventory(t *testing.T) {
	dbPath, root, _ := queueFixture(t)
	queue, err := openCLINamedQueue(t.Context(), dbPath, "locked", root, false, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if other, err := openCLINamedQueue(t.Context(), dbPath, "locked", root, true, &bytes.Buffer{}); err == nil {
		_ = other.Close()
		t.Fatal("concurrent queue writer admitted")
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if other, err := openCLINamedQueue(t.Context(), dbPath, "locked", t.TempDir(), true, &bytes.Buffer{}); err == nil {
		_ = other.Close()
		t.Fatal("same name silently switched roots")
	}
	corrupt := []byte(`{"version":1,"name":`)
	if err := os.WriteFile(queue.path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if other, err := openCLINamedQueue(t.Context(), dbPath, "locked", root, true, &bytes.Buffer{}); err == nil {
		_ = other.Close()
		t.Fatal("refresh replaced corrupt progress")
	}
	payload, err := os.ReadFile(queue.path)
	if err != nil || !bytes.Equal(payload, corrupt) {
		t.Fatal("failed load overwrote progress")
	}
}

func TestNamedQueueRejectsEmptyRoot(t *testing.T) {
	for _, root := range []string{"", " \t "} {
		dbPath := filepath.Join(t.TempDir(), "db.sqlite")
		queue, err := openCLINamedQueue(t.Context(), dbPath, "empty-root", root, false, io.Discard)
		if err == nil {
			_ = queue.Close()
			t.Fatal("empty queue root selected the current working directory")
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(dbPath), "queues")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid root created queue state: %v", err)
		}
	}
}

func TestNamedQueuePreservesRawRoot(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	for _, root := range []string{" media", "media"} {
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "episode.mkv"), []byte("synthetic media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	queue, err := openCLINamedQueue(t.Context(), filepath.Join(base, "db.sqlite"), "raw-root", " media", false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	if queue.inventory.Root != filepath.Join(base, " media") || !reflect.DeepEqual(queue.paths(false, 0), []string{filepath.Join(base, " media", "episode.mkv")}) {
		t.Fatalf("wrong saved root: %+v", queue.inventory)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := openCLINamedQueue(t.Context(), filepath.Join(base, "db.sqlite"), "raw-root", "media", false, io.Discard); err == nil || !strings.Contains(err.Error(), "root differs") {
		if reopened != nil {
			_ = reopened.Close()
		}
		t.Fatalf("whitespace-distinct sibling root was accepted: %v", err)
	}
}

func TestNamedQueueSkippedOutcomeIsPresentedAsSkipped(t *testing.T) {
	dbPath, root, paths := queueFixture(t)
	queue, err := openCLINamedQueue(t.Context(), dbPath, "skipped", root, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	bridge := newCLITUIBridge()
	streams := cliIO{out: &cliPresentationWriter{Writer: io.Discard, presenter: bridge}, presenter: bridge}
	batch := newCLIPreparationBatch(api.Request{}, paths[:1])
	batch.queue = queue
	err = processCLIPreparationItems(t.Context(), batch, true, time.Minute, streams, api.NopLogger{}, func(context.Context, cliPreparationItem) (cliQueueOutcome, error) {
		return cliQueueOutcome{Status: "skipped"}, nil
	})
	if err != nil || bridge.latest.ItemFailed || !strings.Contains(bridge.latest.Result, "Item skipped") || queue.inventory.Items[0].Status != "skipped" {
		t.Fatalf("skipped item looked completed: view=%+v item=%+v err=%v", bridge.latest, queue.inventory.Items[0], err)
	}
}

func TestNamedQueueRetainsWhitespaceDistinctMembers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ordinary Windows paths do not preserve trailing spaces")
	}
	base := t.TempDir()
	root := filepath.Join(base, "sources")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(root, "movie.mkv"), filepath.Join(root, "movie.mkv ")}
	for _, path := range want {
		if err := os.WriteFile(path, []byte("synthetic media"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	queue, err := openCLINamedQueue(t.Context(), filepath.Join(base, "db.sqlite"), "literal-members", root, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	if got := queue.paths(false, 0); !reflect.DeepEqual(got, want) {
		t.Fatalf("literal members collapsed: %v", got)
	}
	batch := newCLIPreparationBatch(api.Request{}, want)
	batch.queue = queue
	var processed []string
	err = processCLIPreparationItems(t.Context(), batch, true, time.Minute, cliIO{}, api.NopLogger{}, func(_ context.Context, item cliPreparationItem) (cliQueueOutcome, error) {
		processed = append(processed, item.originalPath)
		return cliQueueOutcome{Status: "completed"}, nil
	})
	if err != nil || !reflect.DeepEqual(processed, want) || queue.inventory.Items[0].Status != "completed" || queue.inventory.Items[1].Status != "completed" {
		t.Fatalf("distinct members did not advance independently: processed=%v items=%+v err=%v", processed, queue.inventory.Items, err)
	}
}

func TestQueueWorkflowOutcomeRequiresConfirmedSubmission(t *testing.T) {
	for _, test := range []struct {
		name     string
		session  *cliWorkflowSession
		prepared bool
		status   string
		uploads  int
	}{
		{
			name:    "no result",
			session: &cliWorkflowSession{},
			status:  "skipped",
		},
		{
			name:     "input only",
			session:  &cliWorkflowSession{},
			prepared: true,
			status:   "completed",
		},
		{
			name:    "dry run",
			session: &cliWorkflowSession{submissionSuppressed: true, current: releaseworkflow.CommandResult{DryRun: &api.UploadDryRunResult{}}},
			status:  "completed",
		},
		{
			name: "confirmed with client failure",
			session: &cliWorkflowSession{current: releaseworkflow.CommandResult{UploadResult: &api.UploadResult{Results: []api.UploadTrackerResult{{
				TrackerID:             "ALPHA",
				SubmissionStatus:      api.StageStatusCompleted,
				ClientInjectionStatus: api.StageStatusFailed,
			}}}}},
			status:  "completed",
			uploads: 1,
		},
		{
			name:    "unknown submission",
			session: &cliWorkflowSession{current: releaseworkflow.CommandResult{UploadResult: &api.UploadResult{Results: []api.UploadTrackerResult{{TrackerID: "ALPHA", SubmissionStatus: api.StageStatusInterrupted}}}}},
			status:  "interrupted",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := cliQueueWorkflowOutcome(test.session, test.prepared)
			if got.Status != test.status || len(got.ConfirmedUploads) != test.uploads {
				t.Fatalf("outcome=%+v", got)
			}
		})
	}
}

func TestNamedQueueRetryRetainsWorkflowAuthorityChecks(t *testing.T) {
	for _, barrier := range []error{api.ErrReleaseWorkflowEffectOutcomeUnknown, api.ErrActiveInputLeaseLost} {
		t.Run(barrier.Error(), func(t *testing.T) {
			dbPath, root, paths := queueFixture(t)
			queue, err := openCLINamedQueue(t.Context(), dbPath, "fenced", root, false, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer queue.Close()
			queue.inventory.Items[0].Status = "interrupted"
			for i := 1; i < len(queue.inventory.Items); i++ {
				queue.inventory.Items[i].Status = "completed"
			}
			coreSvc := &cliWorkflowCoreFake{}
			coreSvc.continueFn = func(api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
				return releaseworkflow.CommandResult{}, barrier
			}
			for range 2 {
				batch := newCLIPreparationBatch(api.Request{Options: api.UploadOptions{InteractionMode: api.InteractionModeUnattended}}, queue.paths(true, 0))
				batch.queue = queue
				err := runCLIWorkflowUploadOnly(t.Context(), coreSvc, batch, false, true, true, config.Config{}, cliIO{}, api.NopLogger{})
				if !errors.Is(err, barrier) || len(coreSvc.uploadRequests) != 0 {
					t.Fatalf("queue retry bypassed authority: err=%v uploads=%d", err, len(coreSvc.uploadRequests))
				}
			}
			item := queue.inventory.Items[0]
			if item.Path != paths[0] || item.Attempts != 2 || len(item.ConfirmedUploads) != 0 || len(coreSvc.continuations) != 2 {
				t.Fatalf("fenced retries changed authority: %+v", item)
			}
			if errors.Is(barrier, api.ErrReleaseWorkflowEffectOutcomeUnknown) && item.Status != "interrupted" {
				t.Fatalf("unknown effect lost interruption: %+v", item)
			}
		})
	}
}

func TestNamedQueueAlreadyUploadedIsSkippedWithoutInventingNewSubmission(t *testing.T) {
	dbPath, root, _ := queueFixture(t)
	queue, err := openCLINamedQueue(t.Context(), dbPath, "confirmed", root, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	current := requiredFactsCLIResult()
	current.Workflow.Status = api.WorkflowStatusCompleted
	current.Workflow.SubmissionExclusions = []api.SubmissionExclusion{{
		TrackerID:   "ALPHA",
		Reason:      "already_uploaded",
		ConfirmedAt: time.Now().UTC(),
	}}
	coreSvc := &cliWorkflowCoreFake{current: current}
	coreSvc.continueFn = func(api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) { return current, nil }
	coreSvc.startUploadFn = func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
		return current, nil
	}
	batch := newCLIPreparationBatch(api.Request{}, queue.paths(false, 1))
	batch.queue = queue
	var output bytes.Buffer
	if err := runCLIWorkflowUploadOnly(t.Context(), coreSvc, batch, false, true, true, config.Config{}, cliIO{out: &output}, api.NopLogger{}); err != nil {
		t.Fatal(err)
	}
	item := queue.inventory.Items[0]
	if item.Status != "skipped" || len(item.ConfirmedUploads) != 0 || !strings.Contains(output.String(), "Already uploaded") || len(coreSvc.uploadRequests) != 1 {
		t.Fatalf("history exclusion became new upload: %+v %s", item, output.String())
	}
}

func TestNamedQueueCheckpointFailureReachesTUI(t *testing.T) {
	dbPath, root, _ := queueFixture(t)
	queue, err := openCLINamedQueue(t.Context(), dbPath, "tui-checkpoint", root, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	prepared := requiredFactsCLIResult()
	coreSvc := &cliWorkflowCoreFake{current: prepared}
	coreSvc.continueFn = func(api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) { return prepared, nil }
	coreSvc.startUploadFn = func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
		queue.path = filepath.Dir(queue.path)
		completed := prepared
		completed.UploadResult = &api.UploadResult{Results: []api.UploadTrackerResult{{TrackerID: "ALPHA", SubmissionStatus: api.StageStatusCompleted}}}
		return completed, nil
	}
	bridge := newCLITUIBridge()
	streams := cliIO{out: &cliPresentationWriter{Writer: io.Discard, presenter: bridge}, presenter: bridge}
	batch := newCLIPreparationBatch(api.Request{}, queue.paths(false, 2))
	batch.queue = queue
	err = runCLIWorkflowUploadOnly(t.Context(), coreSvc, batch, false, true, true, config.Config{}, streams, api.NopLogger{})
	if _, ok := errors.AsType[*cliQueueCheckpointError](err); !ok {
		t.Fatalf("checkpoint failure not returned: %v", err)
	}
	if !bridge.latest.ItemFailed || strings.Contains(bridge.latest.Result, "Item completed") || !strings.Contains(bridge.summary(err), "1 failed") {
		t.Fatalf("TUI hid checkpoint failure: %+v %s", bridge.latest, bridge.summary(err))
	}
}

func TestNamedQueueFailedDryRunsRemainRetryable(t *testing.T) {
	for _, status := range []api.StageStatus{api.StageStatusCompleted, api.StageStatusFailed, api.StageStatusPartial} {
		t.Run(string(status), func(t *testing.T) {
			dbPath, root, paths := queueFixture(t)
			queue, err := openCLINamedQueue(t.Context(), dbPath, "dry-run", root, false, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			defer queue.Close()
			prepared := requiredFactsCLIResult()
			coreSvc := &cliWorkflowCoreFake{current: prepared}
			coreSvc.continueFn = func(api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) { return prepared, nil }
			coreSvc.startUploadFn = func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
				result := prepared
				result.DryRun = &api.UploadDryRunResult{
					Status:         status,
					SucceededCount: 1,
					Reports:        []api.TrackerDryRunReport{{TrackerID: "ALPHA", Status: api.StageStatusCompleted}},
				}
				if status != api.StageStatusCompleted {
					result.DryRun.FailedCount = 1
					result.DryRun.Reports = append(result.DryRun.Reports, api.TrackerDryRunReport{TrackerID: "BETA", Status: api.StageStatusFailed})
					if status == api.StageStatusFailed {
						result.DryRun.SucceededCount = 0
						result.DryRun.Reports = result.DryRun.Reports[1:]
					}
				}
				return result, nil
			}
			batch := newCLIPreparationBatch(api.Request{}, queue.paths(false, 1))
			batch.queue = queue
			err = runCLIWorkflowUploadOnly(t.Context(), coreSvc, batch, true, true, true, config.Config{}, cliIO{}, api.NopLogger{})
			want := "failed"
			if status == api.StageStatusCompleted {
				want = "completed"
			}
			item := queue.inventory.Items[0]
			if item.Status != want || len(item.ConfirmedUploads) != 0 || (err != nil) != (want == "failed") {
				t.Fatalf("dry run outcome=%+v err=%v", item, err)
			}
			if got := queue.paths(true, 1); want == "failed" && !reflect.DeepEqual(got, paths[:1]) || want == "completed" && reflect.DeepEqual(got, paths[:1]) {
				t.Fatalf("dry-run retry=%v", got)
			}
		})
	}
}

func TestNamedQueueCanceledLoadDoesNotRewriteCheckpoint(t *testing.T) {
	dbPath, root, _ := queueFixture(t)
	queue, err := openCLINamedQueue(t.Context(), dbPath, "canceled-load", root, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	queue.inventory.Items[0].Status = "running"
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(queue.path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if opened, err := openCLINamedQueue(ctx, dbPath, "canceled-load", root, false, io.Discard); !errors.Is(err, context.Canceled) {
		if opened != nil {
			_ = opened.Close()
		}
		t.Fatalf("canceled queue load proceeded: %v", err)
	}
	after, err := os.ReadFile(queue.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("canceled load rewrote interrupted progress")
	}
}

func TestNamedQueueRejectsEquivalentSavedMembers(t *testing.T) {
	dbPath, root, paths := queueFixture(t)
	queue, err := openCLINamedQueue(t.Context(), dbPath, "aliases", root, false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	// Retain the dot segment that filepath.Join would clean away.
	alias := root + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(paths[0])
	queue.inventory.Items = []cliQueueItem{{Path: paths[0], Status: "pending"}, {Path: alias, Status: "pending"}}
	if err := queue.save(); err != nil {
		t.Fatal(err)
	}
	if err := queue.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(queue.path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened, err := openCLINamedQueue(t.Context(), dbPath, "aliases", root, false, io.Discard); err == nil {
		_ = reopened.Close()
		t.Fatal("equivalent saved members were accepted")
	}
	after, err := os.ReadFile(queue.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected inventory was rewritten")
	}
}

func TestNamedQueueRetainsConfirmedUploadsWhenCompositeFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dbPath, root, _ := queueFixture(t)
		queue, err := openCLINamedQueue(t.Context(), dbPath, "partial", root, false, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		defer queue.Close()
		prepared := requiredFactsCLIResult()
		coreSvc := &cliWorkflowCoreFake{current: prepared}
		coreSvc.continueFn = func(api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) { return prepared, nil }
		coreSvc.startUploadFn = func(api.CreateReleaseWorkflowUploadRequest) (releaseworkflow.CommandResult, error) {
			operation := api.WorkflowOperationStatus{
				ID:         "partial-upload",
				WorkflowID: prepared.Workflow.ID,
				Command:    "composite_upload",
				Operation:  api.OperationKindUploadExecute,
				Status:     api.StageStatusFailed,
			}
			latest := prepared
			latest.Operation = &operation
			latest.UploadResult = &api.UploadResult{Results: []api.UploadTrackerResult{
				{TrackerID: "ALPHA", SubmissionStatus: api.StageStatusCompleted},
				{TrackerID: "BETA", SubmissionStatus: api.StageStatusFailed},
			}}
			coreSvc.current, coreSvc.operation = latest, operation
			initial := prepared
			queued := operation
			queued.Status = api.StageStatusQueued
			initial.Operation = &queued
			return initial, nil
		}
		batch := newCLIPreparationBatch(api.Request{}, queue.paths(false, 1))
		batch.queue = queue
		err = runCLIWorkflowUploadOnly(t.Context(), coreSvc, batch, false, true, true, config.Config{}, cliIO{}, api.NopLogger{})
		if err == nil {
			t.Fatal("partial failure was hidden")
		}
		item := queue.inventory.Items[0]
		if item.Status != "failed" || !reflect.DeepEqual(item.ConfirmedUploads, []api.TrackerID{"ALPHA"}) {
			t.Fatalf("partial failure lost confirmed submission: %+v", item)
		}
	})
}
