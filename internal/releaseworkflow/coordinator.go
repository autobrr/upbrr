// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/autobrr/upbrr/pkg/api"
)

// Coordinator owns process-lifetime workflow coordination. Config-scoped
// Modules share it while retaining their own immutable builders and adapters.
type Coordinator struct {
	shuttingDown       atomic.Bool
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
	recoveryWorkers    map[<-chan struct{}]operationWorker // Guarded by operationWorkersMu, including replacement timers.
	// Recovery state is shared across config-scoped modules and guarded by operationRecoveryMu.
	operationRecoveryMu      sync.Mutex
	operationRecovered       bool
	startupRecoveryRequested bool
	startupRecoveryCompleted bool
	// Startup reset state is guarded by activeMu so a failed reset can resume.
	startupResetPending bool
	startupResetDone    bool
}

// Shutdown permanently closes worker admission, cancels the active-input
// heartbeat, operation workers, and recovery timers, and waits for their cleanup.
// A nil receiver succeeds; a nil context is rejected. Context cancellation ends
// the wait but retains unfinished handles so another call can finish shutdown.
// Config runtime replacement must not call Shutdown.
func (c *Coordinator) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("release workflow: shutdown context is required")
	}
	c.shuttingDown.Store(true)
	c.activeMu.Lock()
	cancel, done := c.activeCancel, c.activeDone
	c.activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	c.operationWorkersMu.Lock()
	workers := make([]operationWorker, 0, len(c.operationWorkers)+len(c.recoveryWorkers))
	for _, worker := range c.operationWorkers {
		workers = append(workers, worker)
	}
	for _, worker := range c.recoveryWorkers {
		workers = append(workers, worker)
	}
	c.operationWorkersMu.Unlock()
	for _, worker := range workers {
		if worker.cancel != nil {
			worker.cancel()
		}
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return fmt.Errorf("release workflow coordinator shutdown: %w", ctx.Err())
		}
		c.activeMu.Lock()
		if c.activeDone == done {
			c.activeCancel, c.activeDone = nil, nil
		}
		c.activeMu.Unlock()
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
		recoveryWorkers:  make(map[<-chan struct{}]operationWorker),
	}, nil
}
