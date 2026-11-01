// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/filelock"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/pathing"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

// cliQueueInventory is a private, versioned checkpoint, independent of workflow
// authority. Neither its progress nor retry selection authorizes remote actions.
type cliQueueInventory struct {
	Version int            `json:"version"`
	Name    string         `json:"name"`
	Root    string         `json:"root"`
	Items   []cliQueueItem `json:"items"`
}

type cliQueueItem struct {
	Path             string          `json:"path"`
	Status           string          `json:"status"`
	Attempts         int             `json:"attempts"`
	ConfirmedUploads []api.TrackerID `json:"confirmedUploads,omitempty"`
	WorkflowID       api.WorkflowID  `json:"workflowId,omitempty"`
	UpdatedAt        time.Time       `json:"updatedAt"`
}

type cliQueueOutcome struct {
	Status           string
	ConfirmedUploads []api.TrackerID
	WorkflowID       api.WorkflowID
}

type cliNamedQueue struct {
	path                  string
	lock                  *os.File
	inventory             cliQueueInventory
	syncCheckpoint        func(string) error
	durabilityUnconfirmed bool
}

// openCLINamedQueue is called only by explicit --queue invocations. Refresh adds
// discovered paths; it never resets existing progress or removes missing members.
func openCLINamedQueue(ctx context.Context, dbPath, name, root string, refresh bool, output io.Writer) (*cliNamedQueue, error) {
	return openCLINamedQueueWithSync(ctx, dbPath, name, root, refresh, output, cliQueueSync{
		hierarchy:  syncCLIQueueHierarchy,
		checkpoint: syncCLIQueueCheckpoint,
	})
}

func openCLINamedQueueWithSync(
	ctx context.Context,
	dbPath, name, root string,
	refresh bool,
	output io.Writer,
	syncs cliQueueSync,
) (_ *cliNamedQueue, result error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("--queue requires a non-empty queue root path")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve queue root: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, cliQueueGatherTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open saved queue canceled: %w", err)
	}
	dir, err := db.Subdir(dbPath, "queues")
	if err != nil {
		return nil, fmt.Errorf("queue directory: %w", err)
	}
	if err := syncs.hierarchy(dir); err != nil {
		return nil, fmt.Errorf("sync queue directory hierarchy before opening queue: %w", err)
	}
	key := sha256.Sum256([]byte(name))
	path := filepath.Join(dir, hex.EncodeToString(key[:])+".json")
	lock, err := filelock.Acquire(path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("lock saved queue: %w", err)
	}
	queue := &cliNamedQueue{
		path:           path,
		lock:           lock,
		syncCheckpoint: syncs.checkpoint,
	}
	defer func() {
		if result != nil {
			_ = queue.Close()
		}
	}()
	payload, err := os.ReadFile(path) //nolint:gosec // Trusted database directory; the queue name is SHA-256 encoded.
	created := errors.Is(err, os.ErrNotExist)
	if err != nil && !created {
		return nil, fmt.Errorf("read saved queue: %w", err)
	}
	if created {
		queue.inventory = cliQueueInventory{
			Version: 1,
			Name:    name,
			Root:    root,
			Items:   []cliQueueItem{},
		}
	} else {
		if err := json.Unmarshal(payload, &queue.inventory); err != nil {
			return nil, fmt.Errorf("decode saved queue; file left unchanged: %w", err)
		}
		if err := queue.validate(ctx, name, root); err != nil {
			return nil, err
		}
		for index := range queue.inventory.Items {
			if queue.inventory.Items[index].Status == "running" {
				queue.inventory.Items[index].Status = "interrupted"
			}
		}
	}
	if created || refresh {
		paths, err := filesystem.GatherQueuePaths(ctx, root)
		if err != nil {
			return nil, fmt.Errorf("discover queue members: %w", err)
		}
		for _, path := range paths {
			index, err := cliQueueItemIndex(ctx, queue.inventory.Items, path)
			if err != nil {
				return nil, fmt.Errorf("merge queue members: %w", err)
			}
			if index < 0 {
				queue.inventory.Items = append(queue.inventory.Items, cliQueueItem{
					Path:      path,
					Status:    "pending",
					UpdatedAt: time.Now().UTC(),
				})
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open saved queue canceled: %w", err)
	}
	if err := queue.save(); err != nil {
		return nil, err
	}
	verb := "Resuming"
	if created {
		verb = "Created"
	} else if refresh {
		verb = "Refreshed"
	}
	//logpolicy:allow named queue discovery must report the owner-requested inspectable checkpoint location
	fmt.Fprintf(output, "%s queue %q: %q\n", verb, name, path)
	return queue, nil
}

func (q *cliNamedQueue) validate(ctx context.Context, name, root string) error {
	if q.inventory.Version != 1 || q.inventory.Name != name || !pathing.SamePath(q.inventory.Root, root) {
		return errors.New("saved queue version, name or root differs; use the original root or a different queue name")
	}
	for index, item := range q.inventory.Items {
		previous, err := cliQueueItemIndex(ctx, q.inventory.Items[:index], item.Path)
		if err != nil {
			return fmt.Errorf("validate queue members: %w", err)
		}
		if !filepath.IsAbs(item.Path) || previous >= 0 {
			return errors.New("saved queue has invalid or repeated members; file left unchanged")
		}
		switch item.Status {
		case "pending", "running", "completed", "failed", "interrupted", "skipped":
		default:
			return errors.New("saved queue has an unknown progress state; file left unchanged")
		}
	}
	return nil
}

func cliQueueItemIndex(ctx context.Context, items []cliQueueItem, path string) (int, error) {
	if err := ctx.Err(); err != nil {
		return -1, fmt.Errorf("queue membership check canceled: %w", err)
	}
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return -1, fmt.Errorf("queue membership check canceled: %w", err)
		}
		if pathing.SamePath(item.Path, path) {
			return index, nil
		}
	}
	return -1, nil
}

// Close releases process ownership without removing the stable lock file.
func (q *cliNamedQueue) Close() error {
	if q.lock == nil {
		return nil
	}
	err := q.lock.Close()
	q.lock = nil
	if err != nil {
		return fmt.Errorf("close queue lock: %w", err)
	}
	return nil
}

func (q *cliNamedQueue) paths(retry bool, limit int) []string {
	var paths []string
	for _, item := range q.inventory.Items {
		if item.Status == "pending" || retry && (item.Status == "failed" || item.Status == "interrupted" || item.Status == "skipped") {
			paths = append(paths, item.Path)
		}
	}
	return filesystem.LimitQueuePaths(paths, limit)
}

func (q *cliNamedQueue) start(ctx context.Context, path string) (int, error) {
	index, err := cliQueueItemIndex(ctx, q.inventory.Items, path)
	if err != nil {
		return 0, err
	}
	if index < 0 {
		return 0, errors.New("queue item is outside saved membership")
	}
	item := &q.inventory.Items[index]
	previous := *item
	item.Status = "running"
	item.Attempts++
	item.UpdatedAt = time.Now().UTC()
	return index, q.saveItem(index, previous)
}

func (q *cliNamedQueue) finish(index int, outcome cliQueueOutcome, attemptErr error) error {
	item := &q.inventory.Items[index]
	previous := *item
	item.Status = outcome.Status
	if item.Status == "" {
		item.Status = "skipped"
	}
	if attemptErr != nil {
		item.Status = "failed"
		if errors.Is(attemptErr, context.Canceled) || errors.Is(attemptErr, context.DeadlineExceeded) ||
			errors.Is(attemptErr, api.ErrReleaseWorkflowEffectOutcomeUnknown) {
			item.Status = "interrupted"
		}
	}
	for _, tracker := range outcome.ConfirmedUploads {
		if !slices.Contains(item.ConfirmedUploads, tracker) {
			item.ConfirmedUploads = append(item.ConfirmedUploads, tracker)
		}
	}
	if outcome.WorkflowID != "" {
		item.WorkflowID = outcome.WorkflowID
	}
	item.UpdatedAt = time.Now().UTC()
	return q.saveItem(index, previous)
}

// saveItem restores only changes known not to have replaced the checkpoint.
// After replacement, retained progress must be reloaded instead of rolled back.
func (q *cliNamedQueue) saveItem(index int, previous cliQueueItem) error {
	err := q.save()
	if err != nil && !q.durabilityUnconfirmed {
		q.inventory.Items[index] = previous
	}
	return err
}

func (q *cliNamedQueue) printProgress(output io.Writer) {
	if q.durabilityUnconfirmed {
		fmt.Fprintln(
			output,
			"Queue progress durability is unconfirmed; the saved file may already contain newer progress. Reopen the named queue before retrying work.",
		)
		return
	}
	counts := make(map[string]int)
	for _, item := range q.inventory.Items {
		counts[item.Status]++
	}
	fmt.Fprintf(output, "Queue progress: %d pending, %d completed, %d failed, %d interrupted, %d skipped.\n",
		counts["pending"], counts["completed"], counts["failed"], counts["interrupted"], counts["skipped"])
}

// save syncs a mode-0600 temporary file, replaces the checkpoint, then applies
// the platform metadata barrier. A post-replacement error retains the published
// checkpoint with durability unconfirmed; callers stop without retrying the write.
func (q *cliNamedQueue) save() error {
	payload, err := json.MarshalIndent(q.inventory, "", "  ")
	if err != nil {
		return fmt.Errorf("encode queue checkpoint: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(q.path), ".queue-*.tmp")
	if err != nil {
		return fmt.Errorf("create queue checkpoint: %w", err)
	}
	tmpPath := file.Name()
	defer func() {
		_ = file.Close()
		// #nosec G703 -- OS-generated temporary name under the trusted queue directory.
		_ = os.Remove(tmpPath)
	}()
	if _, err := file.Write(append(payload, '\n')); err != nil {
		return fmt.Errorf("write queue checkpoint: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync queue checkpoint: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close queue checkpoint: %w", err)
	}
	// #nosec G703 -- OS-generated temporary name and hashed destination under the trusted queue directory.
	if err := os.Rename(tmpPath, q.path); err != nil {
		return fmt.Errorf("replace queue checkpoint: %w", err)
	}
	q.durabilityUnconfirmed = true
	if err := q.syncCheckpoint(q.path); err != nil {
		return fmt.Errorf("queue checkpoint replaced, but durability is unconfirmed; reopen the saved queue before retrying work: %w", err)
	}
	q.durabilityUnconfirmed = false
	return nil
}

// cliQueueWorkflowOutcome records confirmed submissions separately from processing.
// Nil results, duplicate skips and declined actions never establish upload success.
func cliQueueWorkflowOutcome(session *cliWorkflowSession, inputOnly bool) cliQueueOutcome {
	outcome := cliQueueOutcome{Status: "skipped"}
	if session == nil {
		return outcome
	}
	outcome.WorkflowID = session.current.Workflow.ID
	if inputOnly {
		outcome.Status = "completed"
	}
	if session.submissionSuppressed && session.current.DryRun != nil {
		outcome.Status = "completed"
		if status := session.current.DryRun.Status; status == api.StageStatusFailed || status == api.StageStatusPartial {
			outcome.Status = "failed"
		}
	}
	if !session.submissionSuppressed && session.current.UploadResult != nil {
		for _, tracker := range session.current.UploadResult.Results {
			status := tracker.EffectiveSubmissionStatus()
			if status == api.StageStatusCompleted {
				outcome.ConfirmedUploads = append(outcome.ConfirmedUploads, tracker.TrackerID)
			}
			if status == api.StageStatusInterrupted || status == api.StageStatusCanceled || status == api.StageStatusRunning {
				outcome.Status = "interrupted"
			}
			if outcome.Status != "interrupted" && (status == api.StageStatusFailed || status == api.StageStatusUnavailable) {
				outcome.Status = "failed"
			}
		}
		if len(outcome.ConfirmedUploads) > 0 && len(outcome.ConfirmedUploads) == len(session.current.UploadResult.Results) {
			outcome.Status = "completed"
		}
	}
	return outcome
}

type cliQueueCheckpointError struct{ err error }

func (e *cliQueueCheckpointError) Error() string { return e.err.Error() }
func (e *cliQueueCheckpointError) Unwrap() error { return e.err }
