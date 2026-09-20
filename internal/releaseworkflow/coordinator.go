// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/autobrr/upbrr/pkg/api"
)

// Coordinator owns process-lifetime workflow coordination. Config-scoped
// Modules share it while retaining their own immutable builders and adapters.
type Coordinator struct {
	activeMu           sync.Mutex
	activeCancel       context.CancelFunc
	activeDone         <-chan struct{}
	processEpoch       string
	locksMu            sync.Mutex
	locks              map[string]*sync.Mutex
	operationLocksMu   sync.Mutex
	operationLocks     map[api.WorkflowOperationID]*sync.Mutex
	operationWorkersMu sync.Mutex
	operationWorkers   map[api.WorkflowOperationID]operationWorker
	operationRecovery  sync.Once
	recoverError       error
}

// Shutdown stops the process-owned active-input heartbeat and operation
// workers. Config runtime replacement must not call Shutdown.
func (c *Coordinator) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("release workflow: shutdown context is required")
	}
	c.activeMu.Lock()
	cancel, done := c.activeCancel, c.activeDone
	c.activeCancel, c.activeDone = nil, nil
	c.activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf("release workflow coordinator shutdown: %w", ctx.Err())
		}
	}
	c.operationWorkersMu.Lock()
	workers := make([]operationWorker, 0, len(c.operationWorkers))
	for _, worker := range c.operationWorkers {
		workers = append(workers, worker)
	}
	c.operationWorkersMu.Unlock()
	for _, worker := range workers {
		if worker.cancel != nil {
			worker.cancel()
		}
	}
	for _, worker := range workers {
		if worker.done == nil {
			continue
		}
		select {
		case <-worker.done:
		case <-ctx.Done():
			return fmt.Errorf("release workflow coordinator shutdown: %w", ctx.Err())
		}
	}
	return nil
}

// NewCoordinator creates process-lifetime coordination state.
func NewCoordinator() (*Coordinator, error) {
	epoch, err := (randomIDGenerator{}).NewID("process")
	if err != nil {
		return nil, fmt.Errorf("release workflow: process epoch: %w", err)
	}
	return newCoordinator(epoch)
}

func newCoordinator(epoch string) (*Coordinator, error) {
	if epoch == "" {
		return nil, errors.New("release workflow: process epoch is required")
	}
	return &Coordinator{
		processEpoch:     epoch,
		locks:            make(map[string]*sync.Mutex),
		operationLocks:   make(map[api.WorkflowOperationID]*sync.Mutex),
		operationWorkers: make(map[api.WorkflowOperationID]operationWorker),
	}, nil
}
