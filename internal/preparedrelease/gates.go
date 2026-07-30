// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"fmt"
	"sync"
)

type sourceGates struct {
	mu      sync.Mutex
	sources map[string]*sourceGate
}

type sourceGate struct {
	waiters []*sourceWaiter
}

type sourceWaiter struct {
	ready chan struct{}
}

func (g *sourceGates) acquire(ctx context.Context, source string) (func(), error) {
	waiter := &sourceWaiter{ready: make(chan struct{})}
	g.mu.Lock()
	if g.sources == nil {
		g.sources = make(map[string]*sourceGate)
	}
	gate := g.sources[source]
	if gate == nil {
		gate = &sourceGate{}
		g.sources[source] = gate
	}
	gate.waiters = append(gate.waiters, waiter)
	if len(gate.waiters) == 1 {
		close(waiter.ready)
	}
	g.mu.Unlock()

	select {
	case <-waiter.ready:
		if err := ctx.Err(); err != nil {
			g.remove(source, waiter)
			return nil, fmt.Errorf("prepared release: acquire source gate: %w", err)
		}
	case <-ctx.Done():
		g.remove(source, waiter)
		return nil, fmt.Errorf("prepared release: wait for source gate: %w", ctx.Err())
	}
	var once sync.Once
	return func() { once.Do(func() { g.remove(source, waiter) }) }, nil
}

func (g *sourceGates) remove(source string, waiter *sourceWaiter) {
	g.mu.Lock()
	defer g.mu.Unlock()
	gate := g.sources[source]
	if gate == nil {
		return
	}
	for idx, queued := range gate.waiters {
		if queued != waiter {
			continue
		}
		wasHead := idx == 0
		gate.waiters = append(gate.waiters[:idx], gate.waiters[idx+1:]...)
		if wasHead && len(gate.waiters) > 0 {
			close(gate.waiters[0].ready)
		}
		break
	}
	if len(gate.waiters) == 0 {
		delete(g.sources, source)
	}
}
