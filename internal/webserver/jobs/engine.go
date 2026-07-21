// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	completedJobRetention = 24 * time.Hour
	maxCompletedJobs      = 200
)

var (
	// ErrEngineClosed indicates that the engine has begun shutdown and rejects new jobs.
	ErrEngineClosed = errors.New("engine closed")
	// ErrOwnerClosed indicates that an owner generation is unknown, draining, or closed.
	ErrOwnerClosed = errors.New("job owner closed")
	// ErrOwnerDraining indicates that a textual owner ID cannot be reused until blocking removal completes.
	ErrOwnerDraining = errors.New("job owner draining")
	// ErrDupeNotFound indicates that no dupe job is visible to the supplied owner.
	ErrDupeNotFound = errors.New("dupe job not found")
	// ErrUploadNotFound indicates that no upload job is visible to the supplied owner.
	ErrUploadNotFound = errors.New("upload job not found")
)

// SnapshotSink receives copied job snapshots after state locks are released.
// Implementations route each snapshot within the supplied owner scope.
type SnapshotSink interface {
	// EmitDupe publishes the latest dupe-check snapshot for ownerID.
	EmitDupe(ownerID string, snapshot DupeCheckSnapshot)
	// EmitUpload publishes the latest tracker-upload snapshot for ownerID.
	EmitUpload(ownerID string, snapshot TrackerUploadSnapshot)
}

type diagnosticSink interface {
	WarnJob(message string)
}

// UploadProgressPolicy controls how frequently upload progress snapshots are emitted.
// State is updated for every progress event even when snapshot emission is throttled.
type UploadProgressPolicy struct {
	// MinInterval is the minimum delay between ordinary progress emissions; zero disables interval throttling.
	MinInterval time.Duration
	// HashRateDeltaMiB emits early when the MiB/s rate changes by at least this amount; zero makes every update qualify.
	HashRateDeltaMiB float64
}

// Config controls kind-specific presentation behavior. Lifecycle retention is
// deliberately fixed module policy: 24 hours and 200 terminal Jobs per owner.
type Config struct {
	// UploadProgress controls upload snapshot throttling without dropping state updates.
	UploadProgress UploadProgressPolicy
}

type timer interface{ Stop() bool }

// engineDeps supplies deterministic time, timer, ID, retention, and diagnostic seams for package tests.
type engineDeps struct {
	now          func() time.Time
	newTimer     func(time.Duration, func()) timer
	newID        func() string
	retentionTTL time.Duration
	retentionMax int
}

// OwnerHandle is opaque authority for exactly one live owner generation.
// Stale handles remain closed even after the same textual owner ID is registered again.
type OwnerHandle struct {
	engine     *Engine
	id         string
	generation uint64
	closed     atomic.Bool
}

// ID returns the normalized textual owner identity used only for scoped event routing.
func (h *OwnerHandle) ID() string {
	if h == nil {
		return ""
	}
	return h.id
}

type jobKind uint8

const (
	jobKindDupe jobKind = iota + 1
	jobKindUpload
)

type ownerState struct {
	handle   *OwnerHandle
	draining bool
	records  map[string]*jobRecord
	terminal []*jobRecord
}

// jobRecord contains only lifecycle facts shared by all Job kinds.
type jobRecord struct {
	id        string
	kind      jobKind
	owner     *OwnerHandle
	accepted  time.Time
	finished  time.Time
	cancel    context.CancelFunc
	done      chan struct{}
	resources resourceSet
	timer     timer
	terminal  bool
}

// Engine owns owner-scoped upload and duplicate-check lifecycles.
// Its exported methods are safe for concurrent use, and the zero value is not usable.
type Engine struct {
	mu         sync.Mutex
	closing    bool
	closed     bool
	closedDone chan struct{}
	wg         sync.WaitGroup
	sink       SnapshotSink
	config     Config
	deps       engineDeps
	generation uint64
	owners     map[string]*ownerState
	records    map[string]*jobRecord
	dupes      map[string]*dupeJob
	uploads    map[string]*uploadJob
}

// New creates a job engine that emits snapshots through sink according to config.
// A nil sink is allowed and disables snapshot delivery.
func New(sink SnapshotSink, config Config) *Engine {
	return newEngine(sink, config, engineDeps{
		now:          time.Now,
		newTimer:     func(delay time.Duration, callback func()) timer { return time.AfterFunc(delay, callback) },
		newID:        randomID,
		retentionTTL: completedJobRetention,
		retentionMax: maxCompletedJobs,
	})
}

func newEngine(sink SnapshotSink, config Config, deps engineDeps) *Engine {
	if deps.now == nil {
		deps.now = time.Now
	}
	if deps.newTimer == nil {
		deps.newTimer = func(delay time.Duration, callback func()) timer { return time.AfterFunc(delay, callback) }
	}
	if deps.newID == nil {
		deps.newID = randomID
	}
	if deps.retentionTTL == 0 {
		deps.retentionTTL = completedJobRetention
	}
	if deps.retentionMax == 0 {
		deps.retentionMax = maxCompletedJobs
	}
	return &Engine{
		sink:       sink,
		config:     config,
		deps:       deps,
		closedDone: make(chan struct{}),
		owners:     make(map[string]*ownerState),
		records:    make(map[string]*jobRecord),
		dupes:      make(map[string]*dupeJob),
		uploads:    make(map[string]*uploadJob),
	}
}

// RegisterOwner returns the live generation for ownerID or creates one. An ID
// cannot be reused while its prior generation is draining.
func (e *Engine) RegisterOwner(ownerID string) (*OwnerHandle, error) {
	if e == nil {
		return nil, ErrEngineClosed
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return nil, errors.New("owner id is required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closing {
		return nil, ErrEngineClosed
	}
	if owner := e.owners[ownerID]; owner != nil {
		if owner.draining || owner.handle.closed.Load() {
			return nil, ErrOwnerDraining
		}
		return owner.handle, nil
	}
	e.generation++
	handle := &OwnerHandle{
		engine:     e,
		id:         ownerID,
		generation: e.generation,
	}
	e.owners[ownerID] = &ownerState{handle: handle, records: make(map[string]*jobRecord)}
	return handle, nil
}

func (e *Engine) validateOwnerLocked(handle *OwnerHandle) (*ownerState, error) {
	if handle == nil || handle.engine != e || handle.closed.Load() {
		return nil, ErrOwnerClosed
	}
	owner := e.owners[handle.id]
	if owner == nil || owner.handle != handle || owner.handle.generation != handle.generation || owner.draining {
		return nil, ErrOwnerClosed
	}
	return owner, nil
}

// RemoveOwner blocks new enrollment, cancels and drains only handle's Jobs,
// releases their resources, and removes all retained snapshots for that generation.
func (e *Engine) RemoveOwner(handle *OwnerHandle) error {
	if e == nil || handle == nil || handle.engine != e {
		return ErrOwnerClosed
	}
	e.mu.Lock()
	owner := e.owners[handle.id]
	if owner == nil || owner.handle != handle || owner.handle.generation != handle.generation {
		e.mu.Unlock()
		return ErrOwnerClosed
	}
	if owner.draining {
		records := append([]*jobRecord(nil), ownerRecords(owner)...)
		e.mu.Unlock()
		waitRecords(records)
		return nil
	}
	owner.draining = true
	handle.closed.Store(true)
	records := append([]*jobRecord(nil), ownerRecords(owner)...)
	for _, record := range records {
		if record.timer != nil {
			record.timer.Stop()
			record.timer = nil
		}
	}
	e.mu.Unlock()

	for _, record := range records {
		if record.cancel != nil {
			record.cancel()
		}
	}
	waitRecords(records)

	e.mu.Lock()
	if current := e.owners[handle.id]; current == owner {
		for _, record := range ownerRecords(owner) {
			e.deleteRecordLocked(record)
		}
		delete(e.owners, handle.id)
	}
	e.mu.Unlock()
	return nil
}

func ownerRecords(owner *ownerState) []*jobRecord {
	if owner == nil {
		return nil
	}
	records := make([]*jobRecord, 0, len(owner.records))
	for _, record := range owner.records {
		records = append(records, record)
	}
	return records
}

func waitRecords(records []*jobRecord) {
	for _, record := range records {
		if record != nil && record.done != nil {
			<-record.done
		}
	}
}

func (e *Engine) enroll(parent context.Context, handle *OwnerHandle, kind jobKind, resources Resources) (context.Context, *jobRecord, error) {
	if e == nil {
		return nil, nil, ErrEngineClosed
	}
	if handle == nil || handle.engine != e || handle.closed.Load() {
		return nil, nil, ErrOwnerClosed
	}
	jobCtx, cancel := context.WithCancel(parent)
	record := &jobRecord{
		id:        e.deps.newID(),
		kind:      kind,
		owner:     handle,
		accepted:  e.deps.now().UTC(),
		cancel:    cancel,
		done:      make(chan struct{}),
		resources: resourceSet{resources: resources},
	}
	e.mu.Lock()
	if e.closing {
		e.mu.Unlock()
		cancel()
		return nil, nil, ErrEngineClosed
	}
	owner, err := e.validateOwnerLocked(handle)
	if err != nil {
		e.mu.Unlock()
		cancel()
		return nil, nil, err
	}
	e.records[record.id] = record
	owner.records[record.id] = record
	e.wg.Add(1)
	e.mu.Unlock()
	return jobCtx, record, nil
}

func (e *Engine) start(jobCtx context.Context, record *jobRecord, run func(context.Context), fail func(string), finalize func(), emit func()) {
	emit()
	go func() {
		defer e.wg.Done()
		defer record.cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				fail("job worker panicked: " + sanitizeMessage(fmt.Sprint(recovered)))
			}
			if err := record.resources.close(); err != nil {
				e.warn("job resource cleanup failed: " + sanitizeMessage(err.Error()))
			}
			finalize()
			emit()
			e.complete(record)
			close(record.done)
		}()
		run(jobCtx)
	}()
}

// StartUpload validates and clones spec, then enrolls it for asynchronous work.
// Once accepted, the job is detached from caller cancellation and runs until
// explicit cancellation, owner removal, or engine shutdown. Resource ownership
// transfers to the engine only when this method succeeds.
func (e *Engine) StartUpload(ctx context.Context, owner *OwnerHandle, raw UploadSpec) (string, error) {
	if ctx == nil {
		return "", errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("start upload: %w", err)
	}
	spec, err := normalizeUploadSpec(raw)
	if err != nil {
		return "", err
	}
	jobCtx, record, err := e.enroll(context.WithoutCancel(ctx), owner, jobKindUpload, spec.Resources)
	if err != nil {
		return "", err
	}
	job := newUploadJob(record.id, spec, record.accepted, e.config.UploadProgress)
	e.mu.Lock()
	e.uploads[record.id] = job
	e.mu.Unlock()
	e.start(
		jobCtx,
		record,
		func(runCtx context.Context) { e.runUpload(runCtx, job) },
		func(message string) { e.failUpload(job, message) },
		func() { e.finalizeUpload(job) },
		func() { e.emitUpload(record, job) },
	)
	return record.id, nil
}

// StartDupe validates and clones spec, then enrolls it for asynchronous work.
// Once accepted, the job is detached from caller cancellation and runs until
// explicit cancellation, owner removal, or engine shutdown. Resource ownership
// transfers to the engine only when this method succeeds.
func (e *Engine) StartDupe(ctx context.Context, owner *OwnerHandle, raw DupeSpec) (string, error) {
	if ctx == nil {
		return "", errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("start dupe: %w", err)
	}
	spec, err := normalizeDupeSpec(raw)
	if err != nil {
		return "", err
	}
	jobCtx, record, err := e.enroll(context.WithoutCancel(ctx), owner, jobKindDupe, spec.Resources)
	if err != nil {
		return "", err
	}
	job := newDupeJob(record.id, spec, record.accepted)
	e.mu.Lock()
	e.dupes[record.id] = job
	e.mu.Unlock()
	e.start(
		jobCtx,
		record,
		func(runCtx context.Context) { e.runDupe(runCtx, job) },
		func(message string) { e.failDupe(job, message) },
		func() { e.finalizeDupe(job) },
		func() { e.emitDupe(record, job) },
	)
	return record.id, nil
}

func (e *Engine) lookupRecord(owner *OwnerHandle, jobID string, kind jobKind) *jobRecord {
	if e == nil || owner == nil || owner.engine != e || owner.closed.Load() {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	current, err := e.validateOwnerLocked(owner)
	if err != nil {
		return nil
	}
	record := current.records[strings.TrimSpace(jobID)]
	if record == nil || record.kind != kind || record.owner != owner {
		return nil
	}
	return record
}

// UploadSnapshot returns a defensive snapshot when jobID belongs to owner.
func (e *Engine) UploadSnapshot(owner *OwnerHandle, jobID string) (TrackerUploadSnapshot, error) {
	record := e.lookupRecord(owner, jobID, jobKindUpload)
	if record == nil {
		return TrackerUploadSnapshot{}, ErrUploadNotFound
	}
	e.mu.Lock()
	job := e.uploads[record.id]
	e.mu.Unlock()
	if job == nil {
		return TrackerUploadSnapshot{}, ErrUploadNotFound
	}
	return job.snapshot(), nil
}

// DupeSnapshot returns a defensive snapshot when jobID belongs to owner.
func (e *Engine) DupeSnapshot(owner *OwnerHandle, jobID string) (DupeCheckSnapshot, error) {
	record := e.lookupRecord(owner, jobID, jobKindDupe)
	if record == nil {
		return DupeCheckSnapshot{}, ErrDupeNotFound
	}
	e.mu.Lock()
	job := e.dupes[record.id]
	e.mu.Unlock()
	if job == nil {
		return DupeCheckSnapshot{}, ErrDupeNotFound
	}
	return job.snapshot(), nil
}

// List returns immutable retained Jobs for one owner in acceptance order.
func (e *Engine) List(owner *OwnerHandle) ([]OwnerJobSnapshot, error) {
	if e == nil {
		return nil, ErrEngineClosed
	}
	e.mu.Lock()
	state, err := e.validateOwnerLocked(owner)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	records := append([]*jobRecord(nil), ownerRecords(state)...)
	e.mu.Unlock()

	sort.Slice(records, func(left, right int) bool {
		if records[left].accepted.Equal(records[right].accepted) {
			return records[left].id < records[right].id
		}
		return records[left].accepted.Before(records[right].accepted)
	})
	result := make([]OwnerJobSnapshot, 0, len(records))
	for _, record := range records {
		switch record.kind {
		case jobKindDupe:
			snapshot, snapshotErr := e.DupeSnapshot(owner, record.id)
			if snapshotErr != nil {
				continue
			}
			result = append(result, OwnerJobSnapshot{
				Kind:          KindDuplicateCheck,
				JobID:         snapshot.JobID,
				CorrelationID: snapshot.CorrelationID,
				Release:       snapshot.Release,
				Status:        snapshot.Status,
				StartedAt:     snapshot.StartedAt,
				FinishedAt:    snapshot.FinishedAt,
				Dupe:          &snapshot,
			})
		case jobKindUpload:
			snapshot, snapshotErr := e.UploadSnapshot(owner, record.id)
			if snapshotErr != nil {
				continue
			}
			result = append(result, OwnerJobSnapshot{
				Kind:          KindTrackerUpload,
				JobID:         snapshot.JobID,
				CorrelationID: snapshot.CorrelationID,
				RetryOf:       snapshot.RetryOf,
				Release:       snapshot.Release,
				Status:        snapshot.Status,
				StartedAt:     snapshot.StartedAt,
				FinishedAt:    snapshot.FinishedAt,
				Upload:        &snapshot,
			})
		}
	}
	return result, nil
}

// CancelUpload requests cancellation of an owner-scoped upload Job.
func (e *Engine) CancelUpload(owner *OwnerHandle, jobID string) error {
	record := e.lookupRecord(owner, jobID, jobKindUpload)
	if record == nil {
		return ErrUploadNotFound
	}
	record.cancel()
	return nil
}

// CancelDupe requests cancellation of an owner-scoped duplicate-check Job.
func (e *Engine) CancelDupe(owner *OwnerHandle, jobID string) error {
	record := e.lookupRecord(owner, jobID, jobKindDupe)
	if record == nil {
		return ErrDupeNotFound
	}
	record.cancel()
	return nil
}

// UploadRetry returns cloned input for retry-eligible failures without retaining resources or runners.
func (e *Engine) UploadRetry(owner *OwnerHandle, jobID string) (UploadRetry, error) {
	record := e.lookupRecord(owner, jobID, jobKindUpload)
	if record == nil {
		return UploadRetry{}, ErrUploadNotFound
	}
	e.mu.Lock()
	job := e.uploads[record.id]
	e.mu.Unlock()
	if job == nil {
		return UploadRetry{}, ErrUploadNotFound
	}
	return job.retry()
}

func (e *Engine) emitUpload(record *jobRecord, job *uploadJob) {
	if e.sink == nil || record == nil || job == nil {
		return
	}
	snapshot := job.snapshot()
	e.safeSink(func() { e.sink.EmitUpload(record.owner.id, snapshot) })
}

func (e *Engine) emitUploadJob(job *uploadJob) {
	if job == nil {
		return
	}
	e.mu.Lock()
	record := e.records[job.id]
	e.mu.Unlock()
	e.emitUpload(record, job)
}

func (e *Engine) emitDupe(record *jobRecord, job *dupeJob) {
	if e.sink == nil || record == nil || job == nil {
		return
	}
	snapshot := job.snapshot()
	e.safeSink(func() { e.sink.EmitDupe(record.owner.id, snapshot) })
}

func (e *Engine) emitDupeJob(job *dupeJob) {
	if job == nil {
		return
	}
	e.mu.Lock()
	record := e.records[job.id]
	e.mu.Unlock()
	e.emitDupe(record, job)
}

func (e *Engine) safeSink(emit func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			e.warn("job snapshot sink panicked: " + sanitizeMessage(fmt.Sprint(recovered)))
		}
	}()
	emit()
}

func (e *Engine) warn(message string) {
	message = sanitizeMessage(message)
	if sink, ok := e.sink.(diagnosticSink); ok {
		defer func() { _ = recover() }()
		sink.WarnJob(message)
	}
}

func (e *Engine) complete(record *jobRecord) {
	e.mu.Lock()
	defer e.mu.Unlock()
	current := e.records[record.id]
	if current != record {
		return
	}
	record.terminal = true
	record.finished = e.deps.now().UTC()
	owner := e.owners[record.owner.id]
	if owner == nil || owner.handle != record.owner || owner.draining || e.closing {
		return
	}
	owner.terminal = append(owner.terminal, record)
	e.pruneOwnerLocked(owner)
	if e.deps.retentionTTL > 0 {
		var ownedTimer timer
		ownedTimer = e.deps.newTimer(e.deps.retentionTTL, func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			if e.closing || e.records[record.id] != record || record.timer != ownedTimer || !record.terminal {
				return
			}
			e.deleteRecordLocked(record)
		})
		record.timer = ownedTimer
	}
}

func (e *Engine) pruneOwnerLocked(owner *ownerState) {
	limit := e.deps.retentionMax
	if owner == nil || limit <= 0 {
		return
	}
	for len(owner.terminal) > limit {
		oldest := owner.terminal[0]
		owner.terminal = owner.terminal[1:]
		e.deleteRecordLocked(oldest)
	}
}

func (e *Engine) deleteRecordLocked(record *jobRecord) {
	if record == nil || e.records[record.id] != record {
		return
	}
	if record.timer != nil {
		record.timer.Stop()
		record.timer = nil
	}
	delete(e.records, record.id)
	delete(e.dupes, record.id)
	delete(e.uploads, record.id)
	if owner := e.owners[record.owner.id]; owner != nil && owner.handle == record.owner {
		delete(owner.records, record.id)
		for idx, terminal := range owner.terminal {
			if terminal == record {
				owner.terminal = append(owner.terminal[:idx], owner.terminal[idx+1:]...)
				break
			}
		}
	}
}

// Close atomically rejects enrollment, cancels all work, blocks through worker
// and resource release, and discards all owner generations and snapshots.
func (e *Engine) Close() {
	if e == nil {
		return
	}
	e.mu.Lock()
	if e.closed {
		done := e.closedDone
		e.mu.Unlock()
		<-done
		return
	}
	if e.closing {
		done := e.closedDone
		e.mu.Unlock()
		<-done
		return
	}
	e.closing = true
	records := make([]*jobRecord, 0, len(e.records))
	for _, record := range e.records {
		if record.timer != nil {
			record.timer.Stop()
			record.timer = nil
		}
		records = append(records, record)
	}
	for _, owner := range e.owners {
		owner.draining = true
		owner.handle.closed.Store(true)
	}
	e.mu.Unlock()

	for _, record := range records {
		if record.cancel != nil {
			record.cancel()
		}
	}
	e.wg.Wait()
	waitRecords(records)

	e.mu.Lock()
	e.records = make(map[string]*jobRecord)
	e.dupes = make(map[string]*dupeJob)
	e.uploads = make(map[string]*uploadJob)
	e.owners = make(map[string]*ownerState)
	e.closed = true
	close(e.closedDone)
	e.mu.Unlock()
}

func randomID() string {
	value, err := rand.Int(rand.Reader, new(big.Int).SetUint64(^uint64(0)))
	if err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), value.Uint64())
}
