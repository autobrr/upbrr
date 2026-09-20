// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"reflect"
	"runtime/debug"
	"testing"
)

func TestApplicationModuleVersion(t *testing.T) {
	for _, test := range []struct{ version, want string }{
		{"v0.3.4", "v0.3.4"},
		{"v0.3.4-rc.1", "v0.3.4-rc.1"},
		{"v0.0.0-20260911072119-0b32d930ae1f", "0b32d930ae1f (2026-09-11 07:21:19 UTC)"},
		{"v0.8.1-0.20260911072119-0b32d930ae1f", "0b32d930ae1f (2026-09-11 07:21:19 UTC)"},
		{"v0.8.1-rc.1.0.20260911072119-0b32d930ae1f+incompatible", "0b32d930ae1f (2026-09-11 07:21:19 UTC)"},
		{"v0.8.1-0.invalid-date-0b32d930ae1f", "v0.8.1-0.invalid-date-0b32d930ae1f"},
		{"local build", "local build"},
	} {
		t.Run(test.version, func(t *testing.T) {
			if got := applicationModuleVersion(test.version); got != test.want {
				t.Fatalf("version label = %q, want %q", got, test.want)
			}
		})
	}
}

func TestApplicationDependencies(t *testing.T) {
	build := &debug.BuildInfo{Deps: []*debug.Module{
		{Path: "github.com/autobrr/go-bdinfo", Version: "v0.4.2"},
		{Path: "github.com/autobrr/go-mediainfo", Version: "v0.8.1-0.20260911072119-0b32d930ae1f"},
		{
			Path:    "github.com/autobrr/rls",
			Version: "v0.9.0",
			Replace: &debug.Module{Version: "v0.9.1"},
		},
		{
			Path:    "github.com/autobrr/mkbrr",
			Version: "v1.25.1",
			Replace: &debug.Module{Path: "private-local-checkout"},
		},
		{Path: "example.org/unrelated", Version: "v1.0.0"},
	}}
	want := []ApplicationDependency{
		{Path: "github.com/autobrr/go-bdinfo", Version: "v0.4.2"},
		{Path: "github.com/autobrr/go-mediainfo", Version: "0b32d930ae1f (2026-09-11 07:21:19 UTC)"},
		{Path: "github.com/autobrr/rls", Version: "v0.9.0 (replacement: v0.9.1)"},
		{Path: "github.com/autobrr/mkbrr", Version: "v1.25.1 (replacement: local build)"},
	}
	if got := applicationDependencies(build); !reflect.DeepEqual(got, want) {
		t.Fatalf("dependencies = %#v, want %#v", got, want)
	}
	if got := applicationDependencies(nil); got == nil || len(got) != 0 {
		t.Fatalf("missing build metadata must produce an empty array, got %#v", got)
	}
}
