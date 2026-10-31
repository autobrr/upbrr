// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mediainfo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestExportBindsReportsToSourceAndContent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(t.TempDir(), "Example.Release.mkv")
	if err := os.WriteFile(source, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	analyzer := &fakeAnalyzer{text: "General", json: `{"media":{"track":[{"@type":"General"}]}}`}
	service := NewService(nil, analyzer)
	req := Request{
SourcePath: source,
 VideoPath: source,
 TempRoot: root,
}
	first, err := service.Export(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.JSONPath != filepath.Join(root, filepath.Base(source), "MediaInfo.json") {
		t.Fatal(first)
	}
	// A new service (restart) must reuse only a coherent report/identity pair.
	service = NewService(nil, panicAnalyzer{t: t})
	if _, err := service.Export(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("changed source"), 0o600); err != nil {
		t.Fatal(err)
	}
	service = NewService(nil, &fakeAnalyzer{text: "Changed", json: analyzer.json})
	next, err := service.Export(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(next.TextPath)
	if err != nil || string(text) != "Changed" {
		t.Fatalf("stale report reused: %q %v", text, err)
	}
	// A partial write must not count as the committed pair.
	if err := os.WriteFile(next.JSONPath, []byte(`{"media":{"track":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service = NewService(nil, &fakeAnalyzer{text: "Recovered", json: analyzer.json})
	if _, err := service.Export(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	text, _ = os.ReadFile(next.TextPath)
	if string(text) != "Recovered" {
		t.Fatal("partial report accepted")
	}
	if err := os.Remove(filepath.Join(filepath.Dir(next.TextPath), "mediainfo.identity.json")); err != nil {
		t.Fatal(err)
	}
	service = NewService(nil, &fakeAnalyzer{text: "Identity restored", json: analyzer.json})
	if _, err := service.Export(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	text, _ = os.ReadFile(next.TextPath)
	if string(text) != "Identity restored" {
		t.Fatal("unidentified report accepted")
	}
}

func TestExportOverlappingWriterAndCancellation(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.Release.mkv")
	if err := os.WriteFile(source, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	req := Request{SourcePath: source, TempRoot: t.TempDir()}
	entered, finish := make(chan struct{}), make(chan struct{})
	service := NewService(nil, analyzerFunc(func(context.Context, string) (string, []byte, error) {
		close(entered)
		<-finish
		return "General", []byte(`{"media":{"track":[{"@type":"General"}]}}`), nil
	}))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := service.Export(ctx, req); done <- err }()
	<-entered
	_, overlapErr := NewService(nil, panicAnalyzer{t: t}).Export(t.Context(), req)
	cancel()
	close(finish)
	err := <-done
	if overlapErr == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("overlap=%v canceled writer=%v", overlapErr, err)
	}
	dir := filepath.Join(req.TempRoot, filepath.Base(source))
	if _, err := os.Stat(filepath.Join(dir, "mediainfo.identity.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled writer committed identity: %v", err)
	}
	if _, err := NewService(nil, &fakeAnalyzer{text: "retry", json: `{"media":{"track":[{"@type":"General"}]}}`}).Export(t.Context(), req); err != nil {
		t.Fatalf("writer lock was not released: %v", err)
	}
	req.ArtifactDir = filepath.Join(req.TempRoot, "independent")
	if _, err := NewService(nil, panicAnalyzer{t: t}).Export(t.Context(), req); err == nil {
		t.Fatal("independent artifact root accepted")
	}
}

type analyzerFunc func(context.Context, string) (string, []byte, error)

func (f analyzerFunc) Analyze(ctx context.Context, path string) (string, []byte, error) {
	return f(ctx, path)
}
