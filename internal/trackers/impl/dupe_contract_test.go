// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestEveryTrackerExposesOneStructuralDuplicateFactory(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range registry.Names() {
		t.Run(name, func(t *testing.T) {
			definition, ok := registry.Lookup(name)
			if !ok {
				t.Fatal("definition missing")
			}
			factory, ok := definition.(dupe.Factory)
			if !ok {
				t.Fatalf("%s does not expose dupe.Factory", name)
			}
			adapter := dupe.NewAdapter(factory, name, config.Config{}, http.DefaultClient, api.NopLogger{}, registry)
			if adapter == nil {
				t.Fatal("factory returned nil adapter")
			}
			result := adapter.Search(context.Background(), api.DuplicateSubject{
				SourcePath:  "C:/media/Example.Release.2026.1080p-GRP.mkv",
				ReleaseName: "Example.Release.2026.1080p-GRP",
			})
			switch result.Disposition() {
			case dupe.DispositionInvalid:
				t.Fatalf("invalid disposition %v", result.Disposition())
			case dupe.DispositionResolved:
			case dupe.DispositionNotRun:
				if strings.TrimSpace(result.Code()) == "" || strings.TrimSpace(result.SafeMessage()) == "" {
					t.Fatalf("invalid not-run result code=%q message=%q", result.Code(), result.SafeMessage())
				}
			case dupe.DispositionFailed:
				if strings.TrimSpace(result.Code()) == "" || strings.TrimSpace(result.SafeMessage()) == "" {
					t.Fatalf("invalid failed result code=%q message=%q", result.Code(), result.SafeMessage())
				}
			}
		})
	}
}
