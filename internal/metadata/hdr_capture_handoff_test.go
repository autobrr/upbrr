// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/clientdiscovery"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/bdinfo"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

type hdrNativePreparationFixture struct {
	module   *preparedrelease.Module
	service  *Service
	pipeline *recordingEvidencePipeline
	input    api.PrepareInput
	tmpRoot  string
}

func newHDRNativePreparationFixture(t *testing.T, codec byte) hdrNativePreparationFixture {
	t.Helper()
	var payload []byte
	if codec == 0x24 {
		var err error
		payload, err = os.ReadFile(filepath.Join("testdata", "synthetic-hdr-absent.hevc"))
		if err != nil {
			t.Fatal(err)
		}
	}
	root := hdrDiscFixture(t, codec, payload)
	dbPath := filepath.Join(t.TempDir(), "fixture.db")
	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	bdService := bdinfo.New(api.NopLogger{})
	bdService.SetHDRService(hdranalysis.New(hdranalysis.NewAdmission(), api.NopLogger{}))
	service := NewService(repo,
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		WithBDInfoService(bdService), WithMediaInfoExporter(&stubMediaInfo{}), WithSceneDetector(stubSceneDetector{}),
		WithTMDBClient(&stubTMDB{metadata: tmdb.MetadataResult{Title: "Synthetic Capture", Year: 2026}}),
		WithIMDBClient(&stubIMDB{}), WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
	pipeline := &recordingEvidencePipeline{service: service}
	collector, err := preparedrelease.NewEvidenceCollector(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := externalidentity.NewWithCandidateSource(repo, collector)
	if err != nil {
		t.Fatal(err)
	}
	module, err := preparedrelease.New(repo, identity, collector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { module.Invalidate(root) })
	return hdrNativePreparationFixture{
		module:   module,
		service:  service,
		pipeline: pipeline,
		input: api.PrepareInput{
			SourcePath: root,
			Instructions: api.ReleaseFactInstructions{
				Category: new(api.CanonicalCategoryMovie),
				Identity: api.ExternalIDOverrides{TMDBID: new(1234567)},
				Playlist: api.PlaylistInstruction{Set: true, UseAll: true},
			},
			Controls: api.PreparationControls{CaptureHDRMetadata: true, Interaction: api.InteractionModeUnattended},
		},
		tmpRoot: filepath.Join(filepath.Dir(dbPath), "tmp"),
	}
}

func hdrProvisionalSidecars(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.Name() == "metadata.json" && filepath.Base(filepath.Dir(filepath.Dir(path))) == "hdr-provisional" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestHDRNativeCaptureSurvivesPreparedHandoffAndSeedRetirement(t *testing.T) {
	fixture := newHDRNativePreparationFixture(t, 0x24)
	prepared, err := fixture.module.Prepare(t.Context(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	ref := api.ReleaseRef{SourcePath: fixture.input.SourcePath, Generation: prepared.Release.Generation}
	instructions := api.HDRAnalysisInstructions{Release: ref, TargetIDs: []string{api.HDRTargetID(fixture.pipeline.state.Discs[0].Root, "00001.MPLS")}}
	_, err = fixture.module.ResolveHDRAnalysisSubject(t.Context(), instructions)
	failure, ok := api.AsHDRAnalysisFailure(err)
	if !ok || failure.Code != api.HDRAnalysisFailureUnsupportedInput {
		t.Fatalf("verified absence granted analysis authority: %v", err)
	}
	paths := hdrProvisionalSidecars(t, fixture.tmpRoot)
	if len(paths) != 1 {
		t.Fatalf("native absence capture not published: %v", paths)
	}
	path := paths[0]
	fixture.pipeline.state = preparationstate.State{}
	for range 10 {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
		if count, err := preparationstate.CleanupHDRCaptures(t.Context(), fixture.tmpRoot); err != nil || count != 0 {
			t.Fatalf("published preparation lost its native lease: count=%d err=%v", count, err)
		}
	}
	seed, err := fixture.module.Export(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	target := newHDRNativePreparationFixture(t, 0x24)
	imported, err := target.module.Import(t.Context(), seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.module.Purge(t.Context(), imported.SourcePath); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || len(data) == 0 {
		t.Fatalf("seed retirement deleted the source owner's capture: %v", err)
	}
	if count, err := preparationstate.CleanupHDRCaptures(t.Context(), fixture.tmpRoot); err != nil || count != 0 {
		t.Fatalf("source owner lost lease after seed retirement: count=%d err=%v", count, err)
	}
	if err := fixture.module.Purge(t.Context(), ref.SourcePath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owning preparation did not retire capture: %v", err)
	}
}

func TestHDRNativeFailedCaptureSurvivesSeedTransfer(t *testing.T) {
	fixture := newHDRNativePreparationFixture(t, 0x1b)
	prepared, err := fixture.module.Prepare(t.Context(), fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	ref := api.ReleaseRef{SourcePath: fixture.input.SourcePath, Generation: prepared.Release.Generation}
	targetID := api.HDRTargetID(fixture.pipeline.state.Discs[0].Root, "00001.MPLS")
	seed, err := fixture.module.Export(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	target := newHDRNativePreparationFixture(t, 0x24)
	imported, err := target.module.Import(t.Context(), seed)
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.module.ResolveHDRAnalysisSubject(t.Context(), api.HDRAnalysisInstructions{
		Release: imported, TargetIDs: []string{targetID},
	})
	failure, ok := api.AsHDRAnalysisFailure(err)
	if !ok || failure.Code != api.HDRAnalysisFailureUnsupportedInput {
		t.Fatalf("typed native failure lost at seed handoff: %v", err)
	}
}

func TestHDRNativeCaptureRetiredAfterSourceCancellation(t *testing.T) {
	fixture := newHDRNativePreparationFixture(t, 0x24)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	staged := false
	ctx = api.WithPreparationProgressReporter(ctx, func(update api.PreparationProgressUpdate) {
		if update.Phase == api.PreparationPhaseBDInfo && update.Status == api.PreparationProgressCompleted {
			staged = len(hdrProvisionalSidecars(t, fixture.tmpRoot)) == 1
			cancel()
		}
	})
	request := testCollectionRequest(t, api.Request{SourcePath: fixture.input.SourcePath})
	request.Input, request.SourceFingerprint = fixture.input, "source"
	_, err := fixture.service.collectSourceEvidence(ctx, request)
	if !staged || !errors.Is(err, context.Canceled) {
		t.Fatalf("source cancellation did not follow native publication: staged=%v err=%v", staged, err)
	}
	if paths := hdrProvisionalSidecars(t, fixture.tmpRoot); len(paths) != 0 {
		t.Fatalf("failed source handoff retained %d native sidecars", len(paths))
	}
}

type hdrCancelClient struct {
	cancel context.CancelFunc
}

func (hdrCancelClient) Inject(context.Context, api.ClientSubject, api.TorrentResult) error {
	return nil
}

func (client hdrCancelClient) SearchPathedTorrents(context.Context, api.ClientSubject) (api.ClientSearchResult, error) {
	client.cancel()
	return api.ClientSearchResult{}, context.Canceled
}

func TestHDRNativeCaptureRetiredAfterHydrationClientCancellation(t *testing.T) {
	fixture := newHDRNativePreparationFixture(t, 0x24)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	staged := false
	fixture.service.clients = clientdiscovery.New(hdrCancelClient{cancel: func() {
		staged = len(hdrProvisionalSidecars(t, fixture.tmpRoot)) == 1
		cancel()
	}}, api.NopLogger{})
	request := testCollectionRequest(t, api.Request{SourcePath: fixture.input.SourcePath})
	request.Input, request.SourceFingerprint = fixture.input, "source"
	_, err := fixture.service.HydratePrivateResources(ctx, request)
	if !staged || !errors.Is(err, context.Canceled) {
		t.Fatalf("client cancellation did not follow native publication: staged=%v err=%v", staged, err)
	}
	if paths := hdrProvisionalSidecars(t, fixture.tmpRoot); len(paths) != 0 {
		t.Fatalf("failed hydration retained %d native sidecars", len(paths))
	}
}
