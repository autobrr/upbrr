// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitEditorCommandPreservesQuotedWindowsPath(t *testing.T) {
	got, err := splitEditorCommand(`"C:\Program Files\Notepad++\notepad++.exe" -multiInst`)
	if err != nil {
		t.Fatalf("splitEditorCommand: %v", err)
	}
	want := []string{`C:\Program Files\Notepad++\notepad++.exe`, "-multiInst"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestSplitEditorCommandParsesEditorWithWaitArg(t *testing.T) {
	got, err := splitEditorCommand(`code --wait`)
	if err != nil {
		t.Fatalf("splitEditorCommand: %v", err)
	}
	want := []string{"code", "--wait"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestLimitedDescriptionPreviewCapsOutput(t *testing.T) {
	raw := strings.Join([]string{
		"one",
		"two",
		"three",
		"four",
		"five",
		"six",
		"seven",
		"eight",
		"nine",
	}, "\n")
	got := limitedDescriptionPreview(raw)
	if strings.Contains(got, "nine") {
		t.Fatalf("expected preview to omit line after cap, got %q", got)
	}
	if !strings.HasSuffix(got, "\n...") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}
