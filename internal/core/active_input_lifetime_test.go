// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
)

// activeInputFixtureLifetimes joins preparation without retiring the module or its active lease.
// All acquisitions must finish before waiting begins; the fixture opens no further operations.
type activeInputFixtureLifetimes struct {
	workers sync.WaitGroup
}

func (l *activeInputFixtureLifetimes) acquire() (func(), bool) {
	l.workers.Add(1)
	return l.workers.Done, true
}

func (l *activeInputFixtureLifetimes) wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		l.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for active input fixture operations: %w", ctx.Err())
	}
}

func TestActiveInputFixtureLifetimesCompletedBorrowers(t *testing.T) {
	var lifetimes activeInputFixtureLifetimes
	release, admitted := lifetimes.acquire()
	if !admitted {
		t.Fatal("fixture admission denied")
	}
	release()
	if err := lifetimes.wait(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestActiveInputFixtureLifetimesJoinEveryBorrower(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var lifetimes activeInputFixtureLifetimes
		syncRelease, _ := lifetimes.acquire()
		syncRelease()
		first, _ := lifetimes.acquire()
		second, _ := lifetimes.acquire()
		result := make(chan error, 1)
		go func() { result <- lifetimes.wait(t.Context()) }()
		first()
		synctest.Wait()
		select {
		case err := <-result:
			second()
			t.Fatalf("joined before last worker released: %v", err)
		default:
		}
		second()
		synctest.Wait()
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	})
}

func TestActiveInputFixtureLifetimesCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var lifetimes activeInputFixtureLifetimes
		release, _ := lifetimes.acquire()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := lifetimes.wait(ctx); !errors.Is(err, context.Canceled) {
			release()
			t.Fatalf("wait cancellation: %v", err)
		}
		// The fixture's Shutdown cleanup releases outstanding workers after a failed wait.
		release()
		synctest.Wait()
	})
}
