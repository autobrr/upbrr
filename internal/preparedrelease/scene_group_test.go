// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/metadata/imdb"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedSceneGroupCasingSurvivesRestart(t *testing.T) {
	t.Parallel()
	repo, input, detector, restart := sceneGroupPreparation(t)
	detector.name = "Example.Movie.2026.1080p.WEB-DL.H.264-GrP"
	module := restart()
	prepared, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	assertPreparedSceneGroup(t, module, prepared.Release)
	persisted, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	assertSceneGroupNaming(t, persisted.Naming)

	module = restart()
	reused, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Release.Generation != prepared.Release.Generation || detector.calls != 1 {
		t.Fatalf("restart generation=%d detector calls=%d, want %d/1", reused.Release.Generation, detector.calls, prepared.Release.Generation)
	}
	assertPreparedSceneGroup(t, module, reused.Release)
}

func TestPrepareRecomputesV18SceneGroupAfterRestart(t *testing.T) {
	t.Parallel()
	repo, input, detector, restart := sceneGroupPreparation(t)
	detector.name = "Example.Movie.2026.1080p.WEB-DL.H.264-grp"
	prepared, err := restart().Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	previous := prepared.Release
	previous.Compatibility.ContractVersion = "prepared-release-v18"
	// v18 retained the authoritative scene name alongside lowercase group facts.
	previous.Naming.SceneName = "Example.Movie.2026.1080p.WEB-DL.H.264-GrP"
	if err := repo.CommitPreparedRelease(t.Context(), previous); err != nil {
		t.Fatal(err)
	}
	detector.name = previous.Naming.SceneName
	module := restart()
	refreshed, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Release.Generation != previous.Generation+1 || detector.calls != 2 ||
		refreshed.Release.Compatibility.ContractVersion != ContractVersion {
		t.Fatalf("refresh generation=%d detector calls=%d contract=%q", refreshed.Release.Generation, detector.calls,
			refreshed.Release.Compatibility.ContractVersion)
	}
	assertPreparedSceneGroup(t, module, refreshed.Release)
	persisted, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	assertSceneGroupNaming(t, persisted.Naming)

	reused, err := module.Prepare(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Release.Generation != refreshed.Release.Generation || detector.calls != 2 {
		t.Fatalf("reuse generation=%d detector calls=%d, want %d/2", reused.Release.Generation, detector.calls, refreshed.Release.Generation)
	}
}

// sceneGroupPreparation builds a SQLite-backed pipeline whose module can be
// restarted while retaining persisted facts and a counted synthetic scene lookup.
func sceneGroupPreparation(t *testing.T) (*db.SQLiteRepository, api.PrepareInput, *preparedSceneDetector, func() *Module) {
	t.Helper()
	sourcePath := writePreparedTestFile(t, "Example.Movie.2026.1080p.WEB-DL.H.264-grp.mkv", "synthetic media")
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	repo, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.Migrate(); err != nil {
		t.Fatal(err)
	}
	detector := &preparedSceneDetector{}
	var cfg config.Config
	cfg.MainSettings.DBPath = dbPath
	restart := func() *Module {
		service := metadata.NewService(repo,
			metadata.WithConfig(cfg),
			metadata.WithTagsPathFromDB(dbPath),
			metadata.WithSRRDBPaths(dbPath),
			metadata.WithMediaInfoExporter(sceneGroupMediaInfo{}),
			metadata.WithSceneDetector(detector),
			metadata.WithIMDBClient(sceneGroupIMDB{}),
		)
		collector, err := NewEvidenceCollector(service)
		if err != nil {
			t.Fatal(err)
		}
		return newTestModule(t, repo, collector)
	}
	return repo, api.PrepareInput{SourcePath: sourcePath}, detector, restart
}

// assertPreparedSceneGroup checks that one exact prepared generation projects
// the corrected scene group into both upload and duplicate-check subjects.
func assertPreparedSceneGroup(t *testing.T, module *Module, release api.PreparedRelease) {
	t.Helper()
	assertSceneGroupNaming(t, release.Naming)
	ref := api.ReleaseRef{SourcePath: release.Source.SourcePath, Generation: release.Generation}
	upload, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
	if err != nil {
		t.Fatal(err)
	}
	if upload.Tag != "-GrP" || upload.Release.Group != "GrP" || upload.ArrReleaseGroup != "GrP" ||
		!strings.HasSuffix(upload.ReleaseName, "-GrP") {
		t.Fatalf("upload tag=%q group=%q arr group=%q name=%q", upload.Tag, upload.Release.Group, upload.ArrReleaseGroup, upload.ReleaseName)
	}
	dupe, err := module.ResolveDuplicateSubject(t.Context(), api.DuplicateCheckInput{Release: ref})
	if err != nil {
		t.Fatal(err)
	}
	if dupe.Tag != "-GrP" || dupe.Release.Group != "GrP" || !strings.HasSuffix(dupe.ReleaseName, "-GrP") {
		t.Fatalf("duplicate tag=%q group=%q name=%q", dupe.Tag, dupe.Release.Group, dupe.ReleaseName)
	}
}

// assertSceneGroupNaming checks the persisted group, structured name component,
// and both generated-name variants against the synthetic scene spelling.
func assertSceneGroupNaming(t *testing.T, naming api.NamingFacts) {
	t.Helper()
	if !naming.Scene || naming.Tag != "-GrP" || naming.Group != "GrP" || !strings.HasSuffix(naming.ReleaseName, "-GrP") {
		t.Fatalf("scene=%t tag=%q group=%q name=%q", naming.Scene, naming.Tag, naming.Group, naming.ReleaseName)
	}
	group, ok := naming.GeneratedName.Component(api.NameRoleGroup)
	if !ok || group.Value != "-GrP" || group.AvailableValue != "-GrP" || !group.Present {
		t.Fatalf("generated group=%#v, exists=%t", group, ok)
	}
	for _, variant := range []api.ReleaseNameVariant{naming.GeneratedReleaseNames.IncludeEpisodeTitle, naming.GeneratedReleaseNames.OmitEpisodeTitle} {
		if !strings.HasSuffix(variant.Name, "-GrP") {
			t.Fatalf("generated variant=%q", variant.Name)
		}
	}
}

type preparedSceneDetector struct {
	name  string
	calls int
}

// Detect counts fresh scene lookups so restart tests can distinguish recomputation
// from persisted-generation reuse without contacting SRRDB.
func (d *preparedSceneDetector) Detect(context.Context, preparationstate.State) (metadata.SceneResult, error) {
	d.calls++
	return metadata.SceneResult{IsScene: true, SceneName: d.name}, nil
}

type sceneGroupMediaInfo struct{}

// Export leaves technical metadata empty so synthetic media needs no external
// MediaInfo process and filename-derived naming remains under test.
func (sceneGroupMediaInfo) Export(context.Context, mediainfo.Request) (mediainfo.Result, error) {
	return mediainfo.Result{}, nil
}

type sceneGroupIMDB struct{}

// Search keeps identity enrichment deterministic without live IMDb requests.
func (sceneGroupIMDB) Search(context.Context, imdb.SearchInput) (imdb.SearchResult, error) {
	return imdb.SearchResult{}, nil
}

// GetInfo supplies no provider overrides, preserving the scene naming fixture.
func (sceneGroupIMDB) GetInfo(context.Context, string, string, bool) (imdb.Info, error) {
	return imdb.Info{}, nil
}
