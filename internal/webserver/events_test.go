// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"fmt"
	"strings"
	"testing"
)

type eventHubTestLogger struct {
	messages []string
}

func (l *eventHubTestLogger) Debugf(format string, args ...any) {
	l.messages = append(l.messages, strings.TrimSpace(fmt.Sprintf(format, args...)))
}

func TestEventHubLogsDroppedEvents(t *testing.T) {
	hub := newEventHub()
	logger := &eventHubTestLogger{}
	hub.SetLogger(logger)

	sessionID := "session-1"
	ch := make(chan serverEvent)

	hub.mu.Lock()
	hub.subscribers[sessionID] = map[chan serverEvent]struct{}{ch: {}}
	hub.mu.Unlock()

	hub.Emit(sessionID, "preparation:progress", map[string]string{"status": "running"})

	if len(logger.messages) != 1 {
		t.Fatalf("expected one dropped-event log message, got %d", len(logger.messages))
	}
	if !strings.Contains(logger.messages[0], "dropped event") {
		t.Fatalf("expected dropped-event log message, got %q", logger.messages[0])
	}
}
