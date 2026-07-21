// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

type barrierPlanDefinition struct {
	name        string
	started     chan<- string
	releasePrep <-chan struct{}
	submitted   chan<- string
}

func (d barrierPlanDefinition) Name() string { return d.name }

func (barrierPlanDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (barrierPlanDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func (d barrierPlanDefinition) Prepare(ctx context.Context, _ PreparationInput) (TrackerPlan, *PreparationFailure) {
	d.started <- d.name
	select {
	case <-d.releasePrep:
	case <-ctx.Done():
		return TrackerPlan{}, NewPreparationFailure(d.name, "canceled", "preparation canceled", ctx.Err())
	}
	return NewUploadPlan(d.name, api.TrackerDryRunEntry{Tracker: d.name, Status: "ready"}, func(context.Context) (api.UploadSummary, error) {
		d.submitted <- d.name
		return api.UploadSummary{Uploaded: 1}, nil
	}, nil), nil
}

func TestUploadPreparationHasBoundedPoolAndFullBarrier(t *testing.T) {
	t.Parallel()
	names := []string{"AITHER", "BHD", "BLU", "HDB", "MTV", "PTP"}
	started := make(chan string, len(names))
	submitted := make(chan string, len(names))
	releasePrep := make(chan struct{})
	registry := NewRegistry()
	for _, name := range names {
		if err := registry.Register(barrierPlanDefinition{
			name:        name,
			started:     started,
			releasePrep: releasePrep,
			submitted:   submitted,
		}); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	svc := NewServiceWithRegistry(config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList(names)}}, nil, nil, registry)
	done := make(chan error, 1)
	go func() {
		summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "Example.Release.2026"})
		if err == nil && summary.Uploaded != len(names) {
			err = fmt.Errorf("uploaded=%d", summary.Uploaded)
		}
		done <- err
	}()

	for range defaultMaxConcurrentTrackerPreparations {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("preparation pool did not fill")
		}
	}
	select {
	case tracker := <-started:
		t.Fatalf("preparation exceeded bound: %s", tracker)
	case tracker := <-submitted:
		t.Fatalf("submission crossed preparation barrier: %s", tracker)
	default:
	}
	close(releasePrep)
	for range names {
		select {
		case <-submitted:
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent submission did not finish")
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("upload: %v", err)
	}
}

type readyReleasePlanDefinition struct {
	name      string
	releases  *atomic.Int32
	submitted *atomic.Int32
	prepared  chan<- struct{}
}

func (d readyReleasePlanDefinition) Name() string { return d.name }

func (readyReleasePlanDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (readyReleasePlanDefinition) DefaultBaseURL() string { return "https://tracker.example.invalid" }

func (d readyReleasePlanDefinition) Prepare(context.Context, PreparationInput) (TrackerPlan, *PreparationFailure) {
	if d.prepared != nil {
		d.prepared <- struct{}{}
	}
	return NewUploadPlan(d.name, api.TrackerDryRunEntry{}, func(context.Context) (api.UploadSummary, error) {
		d.submitted.Add(1)
		return api.UploadSummary{Uploaded: 1}, nil
	}, func() error {
		d.releases.Add(1)
		return nil
	}), nil
}

type canceledPreparationDefinition struct {
	name    string
	started chan<- struct{}
	wait    <-chan struct{}
}

func (d canceledPreparationDefinition) Name() string { return d.name }

func (canceledPreparationDefinition) UploadContentMode() UploadContentMode {
	return UploadContentModeDescription
}

func (canceledPreparationDefinition) DefaultBaseURL() string {
	return "https://tracker.example.invalid"
}

func (d canceledPreparationDefinition) Prepare(ctx context.Context, _ PreparationInput) (TrackerPlan, *PreparationFailure) {
	if d.wait != nil {
		<-d.wait
	}
	d.started <- struct{}{}
	<-ctx.Done()
	return TrackerPlan{}, NewPreparationFailure(d.name, "canceled", "preparation canceled", ctx.Err())
}

func TestUploadCancellationBeforeBarrierSubmitsNoneAndReleasesReadyPlans(t *testing.T) {
	t.Parallel()
	var releases atomic.Int32
	var submits atomic.Int32
	started := make(chan struct{}, 1)
	ready := make(chan struct{}, 1)
	registry := NewRegistry()
	if err := registry.Register(readyReleasePlanDefinition{
		name:      "AITHER",
		releases:  &releases,
		submitted: &submits,
		prepared:  ready,
	}); err != nil {
		t.Fatalf("register ready: %v", err)
	}
	if err := registry.Register(canceledPreparationDefinition{
		name:    "BLU",
		started: started,
		wait:    ready,
	}); err != nil {
		t.Fatalf("register blocking: %v", err)
	}
	svc := NewServiceWithRegistry(config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER", "BLU"}}}, nil, nil, registry)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.Upload(ctx, api.UploadSubject{SourcePath: "Example.Release.2026"})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("blocking preparation did not start")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("upload error = %v", err)
	}
	if submits.Load() != 0 || releases.Load() != 1 {
		t.Fatalf("submits=%d releases=%d", submits.Load(), releases.Load())
	}
}

type warningLogger struct {
	api.NopLogger
	mu       sync.Mutex
	warnings []string
}

func (l *warningLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	l.warnings = append(l.warnings, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func TestUploadReleaseFailureAfterRemoteSuccessIsSanitizedWarningOnly(t *testing.T) {
	t.Parallel()
	logger := &warningLogger{}
	registry := NewRegistry()
	definition := readyReleasePlanDefinition{
		name:      "AITHER",
		releases:  &atomic.Int32{},
		submitted: &atomic.Int32{},
	}
	if err := registry.Register(releaseErrorDefinition{readyReleasePlanDefinition: definition}); err != nil {
		t.Fatalf("register: %v", err)
	}
	svc := NewServiceWithRegistry(config.Config{Trackers: config.TrackersConfig{DefaultTrackers: config.CSVList{"AITHER"}}}, logger, nil, registry)
	summary, err := svc.Upload(context.Background(), api.UploadSubject{SourcePath: "Example.Release.2026"})
	if err != nil || summary.Uploaded != 1 {
		t.Fatalf("upload = %#v, %v", summary, err)
	}
	logger.mu.Lock()
	joined := strings.Join(logger.warnings, "\n")
	logger.mu.Unlock()
	if !strings.Contains(joined, "plan release failed") || strings.Contains(joined, "secret-value") {
		t.Fatalf("warnings = %q", joined)
	}
}

type releaseErrorDefinition struct{ readyReleasePlanDefinition }

func (d releaseErrorDefinition) Prepare(context.Context, PreparationInput) (TrackerPlan, *PreparationFailure) {
	return NewUploadPlan(d.name, api.TrackerDryRunEntry{}, func(context.Context) (api.UploadSummary, error) {
		d.submitted.Add(1)
		return api.UploadSummary{Uploaded: 1}, nil
	}, func() error {
		d.releases.Add(1)
		return errors.New("token=secret-value cleanup failed")
	}), nil
}
