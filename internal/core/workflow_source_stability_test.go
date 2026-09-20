// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

type sourceMutationTorrentService struct {
	workflowTorrentServiceCapture
	mutate func()
}

func (s *sourceMutationTorrentService) Create(ctx context.Context, subject api.TorrentSubject) (api.TorrentResult, error) {
	s.mutate()
	return s.workflowTorrentServiceCapture.Create(ctx, subject)
}

func TestUploadPlanRejectsSourceMutationDuringTorrentCreation(t *testing.T) {
	source, verified := sourceStabilityFixture(t)
	retained := &workflowRetainedUploadServiceFake{}
	torrents := &sourceMutationTorrentService{mutate: func() { mutateVerifiedSource(t, source) }}
	builder := workflowUploadPlanBuilder{
		resolver: workflowUploadResolverFixed{subject: api.UploadSubject{
			SourcePath:     source,
			SourceIdentity: verified.Identity,
			SourceManifest: verified.Manifest,
		}},
		trackers: retained,
		torrents: torrents,
	}
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "coordinator", Fence: 1})
	_, _, err := builder.Build(ctx,
		api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{{
			TrackerID:   "PTP",
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		}}},
		api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
			TrackerID: "PTP",
			Decision:  api.DupeDecisionNoMatch,
			Status:    api.StageStatusCompleted,
		}}},
		workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{},
		api.DescriptionSet{}, api.DescriptionInstructions{}, releaseworkflow.UploadPlanBuildOptions{}, time.Now())
	if !errors.Is(err, preparedrelease.ErrSourceChanged) || torrents.calls != 1 || retained.subject.SourcePath != "" {
		t.Fatalf("mutation admitted: err=%v torrent_calls=%d retained=%t", err, torrents.calls, retained.subject.SourcePath != "")
	}
}

type sourceStabilityReporter struct{ begins int }

func (r *sourceStabilityReporter) Begin(context.Context, api.WorkflowExternalEffect) (api.WorkflowExternalEffectReceipt, error) {
	r.begins++
	return api.WorkflowExternalEffectReceipt{EffectID: "effect"}, nil
}

func (*sourceStabilityReporter) Complete(context.Context, api.WorkflowExternalEffectReceipt, bool) error {
	return nil
}

type sourceStabilityUploadPlan struct {
	workflowRetainedUploadPlanFake
	submits int
}

func (p *sourceStabilityUploadPlan) Execute(ctx context.Context) ([]trackers.RetainedTrackerResult, error) {
	_, err := api.BeginWorkflowExternalEffect(ctx, api.WorkflowExternalEffect{
		Kind:                api.WorkflowExternalEffectTrackerSubmission,
		ScopeID:             "PTP",
		SemanticFingerprint: "exact-plan",
	})
	if err != nil {
		return nil, fmt.Errorf("begin test submission: %w", err)
	}
	p.submits++
	return nil, nil
}

func TestRetainedUploadRejectsSourceMutationBeforeFinalFence(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "stable", true: "same-size mutation"}[changed], func(t *testing.T) {
			source, verified := sourceStabilityFixture(t)
			plan := &sourceStabilityUploadPlan{}
			execution := &workflowUploadExecution{sourceManifest: &verified.Manifest, plan: plan}
			if changed {
				mutateVerifiedSource(t, source)
			}
			reporter := &sourceStabilityReporter{}
			ctx := api.WithWorkflowExternalEffectReporter(t.Context(), reporter)
			_, err := execution.Execute(ctx, nil)
			if changed {
				if !errors.Is(err, preparedrelease.ErrSourceChanged) || reporter.begins != 0 || plan.submits != 0 {
					t.Fatalf("changed source reached fence: err=%v begins=%d submits=%d", err, reporter.begins, plan.submits)
				}
			} else if err != nil || reporter.begins != 1 || plan.submits != 1 {
				t.Fatalf("stable source rejected: err=%v begins=%d submits=%d", err, reporter.begins, plan.submits)
			}
		})
	}
}

func sourceStabilityFixture(t *testing.T) (string, api.VerifiedInputSource) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "Example.Release.2026.mkv")
	content := append(bytes.Repeat([]byte("a"), int(api.SourceContentIdentitySampleBytes)), []byte("original")...)
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatal(err)
	}
	verified, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	return source, verified
}

func mutateVerifiedSource(t *testing.T, source string) {
	t.Helper()
	file, err := os.OpenFile(source, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("modified"), api.SourceContentIdentitySampleBytes); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	modified := time.Now().Add(time.Minute)
	if err := os.Chtimes(source, modified, modified); err != nil {
		t.Fatal(err)
	}
}
