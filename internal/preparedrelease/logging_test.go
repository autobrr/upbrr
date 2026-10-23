// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/logging"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

type loggingCollector struct{ recordingCollector }

func (*loggingCollector) HydratePrivateResources(context.Context, preparationstate.Request) (CollectedResources, error) {
	return CollectedResources{}, nil
}

func TestPreparedSummaryUsesSavedFactsAcrossReuseAndRestart(t *testing.T) {
	logger, err := logging.New(config.LoggingConfig{Level: "debug"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger.SetConsoleOutput(&output, &output)
	collector := &loggingCollector{recordingCollector{facts: &CollectedFacts{
		Naming: api.NamingFacts{
			Type:       "ENCODE",
			Source:     "WEB",
			Resolution: "2160p",
		},
		Media: api.MediaFacts{
			Type:                     "REMUX",
			Source:                   "BluRay",
			Container:                "mkv",
			Edition:                  "",
			AudioLanguagesProvenance: api.FactProvenanceManualEmpty,
		},
	}}}
	store := newMemoryStore()
	module, err := New(store, staticIdentityResolver{}, collector, logger)
	if err != nil {
		t.Fatal(err)
	}
	input := api.PrepareInput{SourcePath: writePreparedTestFile(t, "source.mkv", "synthetic media")}
	for _, cache := range []string{"collected", "memory", "persisted"} {
		if cache == "persisted" {
			module, err = New(store, staticIdentityResolver{}, collector, logger)
			if err != nil {
				t.Fatal(err)
			}
		}
		output.Reset()
		result, prepareErr := module.Prepare(t.Context(), input)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		text := output.String()
		for _, want := range []string{"generation=1 cache=" + cache, `category="movie" type="REMUX" source="BluRay" resolution="2160p"`,
			`container="mkv"`, `edition=""`, `audio_language_source="manual_empty"`, "tmdb=1234567(provider)", "provider metadata available"} {
			if !strings.Contains(text, want) {
				t.Fatalf("summary missing %q: %s", want, text)
			}
		}
		if strings.Count(text, "prepared release:") != 4 || strings.Contains(text, "fetched") || result.Release.Generation != 1 {
			t.Fatalf("summary count or generation mismatch: %s", text)
		}
	}
	if collector.callCount() != 1 {
		t.Fatalf("reuse collected facts %d times", collector.callCount())
	}
	output.Reset()
	collector.facts.Naming.Resolution = "1080p"
	input.Instructions.ReleaseName.Resolution = new("1080p")
	result, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Release.Generation != 2 || !strings.Contains(output.String(), `resolution="1080p"`) || strings.Contains(output.String(), `resolution="2160p"`) {
		t.Fatalf("corrected summary did not use returned generation: %s", output.String())
	}
}

func TestPreparedSummaryRespectsOperationVerbosityAndConsole(t *testing.T) {
	logger, err := logging.New(config.LoggingConfig{Level: "info"}, "")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	logger.SetConsoleOutput(&output, &output)
	module := &Module{logger: logger}
	release := api.PreparedRelease{Generation: 7}
	module.logPreparedFacts(t.Context(), release, "memory", 0)
	if len(logger.Recent(20)) != 0 {
		t.Fatal("global INFO accepted DEBUG facts")
	}
	operation, err := logging.NewOperationLogger(logger, "debug")
	if err != nil {
		t.Fatal(err)
	}
	module.logPreparedFacts(logging.WithOperationLogger(t.Context(), operation), release, "memory", 0)
	if len(logger.Recent(20)) != 4 || output.Len() != 0 {
		t.Fatal("operation summary changed global console threshold")
	}
	module.logPreparedFacts(t.Context(), release, "memory", 0)
	if len(logger.Recent(20)) != 4 {
		t.Fatal("operation summary changed global threshold")
	}
}
