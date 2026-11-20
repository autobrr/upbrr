// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/torrent"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowMatchingSubtitles(t *testing.T) {
	t.Parallel()

	subject, policy := workflowSubtitleFixture(t)
	want := []string{
		filepath.Join(subject.SourcePath, "Movie.mkv"),
		filepath.Join(subject.SourcePath, "Movie.en.srt"),
		filepath.Join(subject.SourcePath, "Movie.pt-BR.srt"),
	}
	selected := workflowSubmissionTorrentSubjectWithSubtitles(subject, policy)
	if !slices.Equal(selected.FileList, want) {
		t.Fatalf("torrent files = %v, want %v", selected.FileList, want)
	}
	identity, err := workflowSubmissionContentIdentity(selected, subject.SourceIdentity)
	if err != nil || len(identity.Files) != len(want) {
		t.Fatalf("submission content files = %v, err = %v", identity.Files, err)
	}

	for _, tc := range []struct {
		name     string
		trackers []string
		category api.CanonicalCategory
		keep     bool
	}{
		{"other tracker", []string{"PTP"}, api.CanonicalCategoryMovie, true},
		{"mixed selection", []string{"ASC", "PTP"}, api.CanonicalCategoryMovie, true},
		{"single file", []string{"ASC"}, api.CanonicalCategoryMovie, false},
		{"episode", []string{"ASC"}, api.CanonicalCategoryTV, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := subject
			candidate.Trackers = tc.trackers
			candidate.Identity.Category = tc.category
			candidate.KeepFolder = tc.keep
			if got := workflowSubmissionTorrentSubjectWithSubtitles(candidate, policy).FileList; !slices.Equal(got, want[:1]) {
				t.Fatalf("torrent files = %v, want %v", got, want[:1])
			}
		})
	}
}

func TestWorkflowUploadPlanGeneratesTorrentWithMatchingSubtitles(t *testing.T) {
	t.Parallel()

	subject, registry := workflowSubtitleFixture(t)
	inspection, err := preparedrelease.VerifyInputSource(t.Context(), api.PrepareInput{SourcePath: subject.SourcePath})
	if err != nil {
		t.Fatal(err)
	}
	subject.SourceManifest = inspection.Manifest
	retained := &workflowRetainedUploadServiceFake{}
	builder := workflowUploadPlanBuilder{
		resolver: workflowUploadResolverFixed{subject: subject},
		trackers: retained,
		registry: registry,
		torrents: torrent.NewService(nil, t.TempDir()),
	}
	projections := api.TrackerReleaseProjectionSet{
		ExecutionMode: api.WorkflowExecutionModeNormal,
		Projections: []api.TrackerReleaseProjection{{
			TrackerID:         "ASC",
			DisplayName:       "ASC",
			UploadReleaseName: "Movie",
			Readiness:         api.ReadinessStatusReady,
			UploadReady:       true,
		}},
	}
	dupes := api.DupeAssessment{Results: []api.TrackerDupeAssessment{{
		TrackerID: "ASC",
		Status:    api.StageStatusCompleted,
		Decision:  api.DupeDecisionNoMatch,
	}}}
	ctx := api.WithActiveInputAuthority(t.Context(), api.ActiveInputAuthority{CoordinatorID: "coordinator", Fence: 1})
	_, result, err := builder.Build(
		ctx, projections, dupes, workflowDupePrivateEvidence{},
		api.MediaArtifactSet{}, workflowMediaPrivateArtifacts{}, api.DescriptionSet{},
		api.DescriptionInstructions{}, releaseworkflow.UploadPlanBuildOptions{
			NoSeed:     true,
			DryRun:     true,
			ReviewOnly: true,
		},
		time.Now(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = result.Release() }()
	artifact, err := metainfo.LoadFromFile(retained.subject.TorrentPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := artifact.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(info.Files))
	var payload []byte
	for _, file := range info.Files {
		name := filepath.Join(file.Path...)
		paths = append(paths, name)
		payload = append(payload, name...)
	}
	want := []string{"Movie.en.srt", "Movie.mkv", "Movie.pt-BR.srt"}
	if !slices.Equal(paths, want) {
		t.Fatalf("torrent files = %v, want %v", paths, want)
	}
	piece := sha1.Sum(payload)
	if !bytes.Equal(info.Pieces, piece[:]) {
		t.Fatalf("torrent piece hash = %x, want %x", info.Pieces, piece)
	}
	execution, ok := result.(*workflowUploadExecution)
	if !ok {
		t.Fatalf("unexpected upload plan execution %T", result)
	}
	clientFiles := execution.clientSubject.FileList
	selected := workflowSubmissionTorrentSubjectWithSubtitles(subject, registry)
	if !slices.Equal(clientFiles, selected.FileList) {
		t.Fatalf("client scope = %v, want %v", clientFiles, selected.FileList)
	}
	base := workflowSubmissionTorrentSubject(subject)
	fence, err := workflowSubmissionContentIdentity(base, subject.SourceIdentity)
	if err != nil || fence.Digest != retained.subject.SubmissionContentIdentity.Digest {
		t.Fatalf("submission fence changed with subtitle packaging: got %v, want %v, err = %v",
			retained.subject.SubmissionContentIdentity.Digest, fence.Digest, err)
	}
}

func workflowSubtitleFixture(t *testing.T) (api.UploadSubject, workflowSubmissionTrackerRegistryFake) {
	t.Helper()
	root := t.TempDir()
	video := filepath.Join(root, "Movie.mkv")
	subject := api.UploadSubject{
		SourcePath: root,
		FileList:   []string{video},
		KeepFolder: true,
		Trackers:   []string{"ASC"},
		Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
		SourceManifest: api.SourceManifest{SourcePath: root, Entries: []api.SourceManifestEntry{
			{Path: root, Type: api.SourceEntryTypeDirectory},
			{Path: filepath.Join(root, "Movie.link.srt"), Type: api.SourceEntryTypeUnknown},
			{Path: filepath.Join(root, "Subs", "Movie.nested.srt"), Type: api.SourceEntryTypeFile},
		}},
		SourceIdentity: api.SourceContentIdentity{
			Version: api.SourceContentIdentityVersion,
			Digest:  strings.Repeat("a", 64),
		},
	}
	for _, name := range []string{"Movie.mkv", "Movie.pt-BR.srt", "Other.srt", "Movie.en.srt", "Movie.nfo"} {
		path := filepath.Join(root, name)
		contents := []byte(name)
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			t.Fatal(err)
		}
		subject.SourceManifest.Entries = append(subject.SourceManifest.Entries, api.SourceManifestEntry{
			Path: path,
			Type: api.SourceEntryTypeFile,
			Size: int64(len(contents)),
		})
		subject.SourceIdentity.Files = append(subject.SourceIdentity.Files, api.VerifiedSourceFile{
			LocalPath: path,
			Size:      int64(len(contents)),
			SHA256:    fmt.Sprintf("%x", sha256.Sum256(contents)),
		})
	}
	return subject, workflowSubmissionTrackerRegistryFake{
		"ASC": {
			Name:           "ASC",
			BaseURL:        "https://asc.example.invalid/",
			UploadArtifact: &trackers.UploadArtifactPolicy{IncludeMatchingSubtitles: true},
		},
		"PTP": {Name: "PTP", BaseURL: "https://ptp.example.invalid/"},
	}
}
