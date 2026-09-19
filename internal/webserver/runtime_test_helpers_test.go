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
