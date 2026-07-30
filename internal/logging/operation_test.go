// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
)

func TestOperationLoggerUsesRootSinkWithoutMutatingRootThreshold(t *testing.T) {
	t.Parallel()

	root, err := New(config.LoggingConfig{Level: "info"}, "")
	if err != nil {
		t.Fatalf("new root logger: %v", err)
	}
	root.SetConsoleOutput(io.Discard, io.Discard)
	subscriptionID, entries := root.Subscribe(8)
	defer root.Unsubscribe(subscriptionID)

	scoped, err := NewOperationLogger(root, "trace")
	if err != nil {
		t.Fatalf("new operation logger: %v", err)
	}
	ctx := WithOperationLogger(context.Background(), scoped)
	localPath := filepath.Join(t.TempDir(), "Example.Release.2026.1080p-GRP.mkv")

	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		FromContext(ctx, root).Tracef("operation trace api_key=secret-value source=%s", localPath)
	}()
	go func() {
		defer workers.Done()
		root.Tracef("unscoped trace must remain filtered")
	}()
	workers.Wait()

	recent := root.Recent(10)
	if len(recent) != 1 || recent[0].Level != "trace" {
		t.Fatalf("recent entries = %#v", recent)
	}
	if strings.Contains(recent[0].Message, "secret-value") || strings.Contains(recent[0].Message, localPath) {
		t.Fatalf("scoped entry was not sanitized: %q", recent[0].Message)
	}
	select {
	case entry := <-entries:
		if entry.ID != recent[0].ID {
			t.Fatalf("subscriber entry = %#v, recent = %#v", entry, recent[0])
		}
	default:
		t.Fatal("scoped entry did not reach root subscriber")
	}

	root.Debugf("root remains info-filtered")
	root.Infof("root remains usable")
	if got := root.Recent(10); len(got) != 2 || got[1].Level != "info" {
		t.Fatalf("root entries after scope = %#v", got)
	}
}

func TestOperationLoggerEmptyOverrideUsesRootThreshold(t *testing.T) {
	t.Parallel()

	root, err := New(config.LoggingConfig{Level: "warn"}, "")
	if err != nil {
		t.Fatalf("new root logger: %v", err)
	}
	root.SetConsoleOutput(io.Discard, io.Discard)
	scoped, err := NewOperationLogger(root, "")
	if err != nil {
		t.Fatalf("new operation logger: %v", err)
	}
	scoped.Infof("filtered")
	scoped.Warnf("retained")
	if entries := root.Recent(10); len(entries) != 1 || entries[0].Level != "warn" {
		t.Fatalf("entries = %#v", entries)
	}
}
