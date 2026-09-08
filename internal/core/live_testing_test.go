// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestLiveTestOverrideClientCannotBypassProcessPolicy(t *testing.T) {
	t.Parallel()
	policy, err := api.NewLiveTestPolicy("override-run", filepath.Join(t.TempDir(), "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	remote := &dryRunClientService{}
	client := clientdiscovery.WithLiveTestPolicy(remote, policy)
	if err := client.Inject(t.Context(), api.ClientSubject{}, api.TorrentResult{}); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
		t.Fatalf("override injection = %v", err)
	}
	if _, err := client.SearchPathedTorrents(t.Context(), api.ClientSubject{ClientOverrides: api.ClientOverrides{ForceRecheck: new(true)}}); !errors.Is(err, api.ErrLiveTestMutationDisabled) {
		t.Fatalf("override recheck = %v", err)
	}
	if len(remote.injections) != 0 || remote.searches != 0 {
		t.Fatalf("override received prohibited calls: %#v", remote)
	}
	for _, forceRecheck := range []*bool{nil, new(false)} {
		if _, err := client.SearchPathedTorrents(t.Context(), api.ClientSubject{ClientOverrides: api.ClientOverrides{ForceRecheck: forceRecheck}}); err != nil {
			t.Fatalf("override read-only lookup = %v", err)
		}
	}
	if remote.searches != 2 || len(remote.injections) != 0 {
		t.Fatalf("override preservation = %#v", remote)
	}
	if got := policy.Snapshot().ClientMutation; got != (api.LiveTestEffectCounts{MutationCallsDenied: 2}) {
		t.Fatalf("override receipt = %#v", got)
	}
	ordinary := clientdiscovery.WithLiveTestPolicy(remote, nil)
	if err := ordinary.Inject(t.Context(), api.ClientSubject{}, api.TorrentResult{}); err != nil {
		t.Fatalf("ordinary override injection = %v", err)
	}
	if _, err := ordinary.SearchPathedTorrents(t.Context(), api.ClientSubject{ClientOverrides: api.ClientOverrides{ForceRecheck: new(true)}}); err != nil {
		t.Fatalf("ordinary override recheck = %v", err)
	}
	if len(remote.injections) != 1 || remote.searches != 3 {
		t.Fatalf("nil policy swallowed ordinary calls: %#v", remote)
	}
}

func TestLiveTestUploadBuilderRejectsSubmitAndBindsNoSeed(t *testing.T) {
	t.Parallel()
	policy, err := api.NewLiveTestPolicy("builder-run", filepath.Join(t.TempDir(), "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	builder := workflowUploadPlanBuilder{liveTest: policy}
	// Missing downstream dependencies deliberately prove rejection precedes preparation.
	_, execution, err := builder.Build(t.Context(), api.TrackerReleaseProjectionSet{}, api.DupeAssessment{}, nil,
		api.MediaArtifactSet{}, nil, api.DescriptionSet{}, nil, releaseworkflow.UploadPlanBuildOptions{}, time.Now())
	if !errors.Is(err, api.ErrLiveTestMutationDisabled) || execution != nil {
		t.Fatalf("unsafe build = %v, execution = %#v", err, execution)
	}
	fingerprint := func(builder workflowUploadPlanBuilder, noSeed bool) api.WorkflowFingerprint {
		value, err := builder.Fingerprint(t.Context(), api.TrackerReleaseProjectionSet{}, api.DupeAssessment{},
			api.MediaArtifactSet{}, api.DescriptionSet{}, releaseworkflow.UploadPlanBuildOptions{NoSeed: noSeed})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	ordinary := workflowUploadPlanBuilder{}
	if fingerprint(builder, false) != fingerprint(ordinary, true) || fingerprint(builder, false) == fingerprint(ordinary, false) {
		t.Fatal("live-test fingerprint did not bind effective no-seed policy")
	}
}

func TestLiveTestUploadBuilderPreparesWithoutInjecting(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	policy, err := api.NewLiveTestPolicy("builder-dry-run", filepath.Join(root, "images.jsonl"), 0)
	if err != nil {
		t.Fatal(err)
	}
	torrentPath := filepath.Join(root, "Example.Release.2026-GRP.torrent")
	if err := os.WriteFile(torrentPath, []byte("synthetic prepared torrent"), 0o600); err != nil {
		t.Fatal(err)
	}
	remote := &dryRunClientService{}
	service := &workflowRetainedUploadServiceFake{torrentPaths: map[api.TrackerID]string{"ALPHA": torrentPath}}
	builder := workflowUploadPlanBuilder{
		liveTest: policy,
		resolver: workflowUploadResolverFixed{subject: api.UploadSubject{SourcePath: filepath.Join(root, "Example.Release.2026-GRP")}},
		trackers: service,
		clients:  remote,
	}
	plan, execution, err := builder.Build(t.Context(), api.TrackerReleaseProjectionSet{
		ExecutionMode: api.WorkflowExecutionModeNormal,
		Projections: []api.TrackerReleaseProjection{{
			TrackerID:   "ALPHA",
			Readiness:   api.ReadinessStatusReady,
			UploadReady: true,
		}},
	}, api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
		TrackerID: "ALPHA",
		Decision:  api.DupeDecisionNoMatch,
		Status:    api.StageStatusCompleted,
	}}},
		workflowDupePrivateEvidence{}, api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{}, api.DescriptionInstructions{},
		releaseworkflow.UploadPlanBuildOptions{DryRun: true}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = execution.Release() })
	if len(service.projections) != 1 || plan.Status != api.StageStatusReady || len(plan.Trackers) != 1 ||
		plan.Trackers[0].ClientInjectionStatus != api.StageStatusSkipped || len(remote.injections) != 0 {
		t.Fatalf("dry-run plan=%#v, preparations=%d, injections=%d", plan, len(service.projections), len(remote.injections))
	}
}
