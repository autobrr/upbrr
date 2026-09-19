// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
)

func TestRuntimeBundleRetiresAfterBorrowerReleases(t *testing.T) {
	owner := &activationTestOwner{}
	bundle := newRuntimeBundle(owner, nil)
	release, ok := bundle.borrow()
	if !ok {
		t.Fatal("borrow current runtime bundle")
	}
	bundle.retire()
	if owner.closed.Load() {
		t.Fatal("retired borrowed runtime closed before release")
	}
	release()
	if !owner.closed.Load() {
		t.Fatal("retired runtime was not closed after borrower release")
	}

	if release, ok := bundle.borrow(); ok {
		release()
		t.Fatal("retired runtime accepted a new borrower")
	}
}

func TestBackendCloseUsesSynchronizedRuntimeSnapshot(t *testing.T) {
	backend := &Backend{runtimeBundle: newRuntimeBundle(nil, nil)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 1000 {
			backend.replaceRuntimeGeneration(AllocateRuntimeGenerationID(), config.Config{}, CoreCapabilities{}, nil, nil, nil)
		}
	}()
	for range 1000 {
		if err := backend.CloseContext(t.Context()); err != nil {
			t.Error(err)
		}
	}
	<-done
}

func clearSessionLogStopGeneration(s *Server, sessionID string) {
	sessionLogStopGenerations.mu.Lock()
	defer sessionLogStopGenerations.mu.Unlock()
	clearSessionLogStopGenerationLocked(s, sessionID)
}

func (s *Server) stopSessionLogStreamsIfIdle(sessionID string) {
	if s == nil || s.backend == nil {
		return
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return
	}
	s.scheduleStopSessionLogStreamsIfIdle(trimmedSessionID, nextSessionLogStopGeneration(s, trimmedSessionID))
}

func (b *Backend) replaceRuntime(
	cfg config.Config,
	capabilities CoreCapabilities,
	logger *logging.Logger,
) (LifecycleOwner, *logging.Logger) {
	return b.replaceRuntimeGeneration(AllocateRuntimeGenerationID(), cfg, capabilities, nil, logger, nil)
}
