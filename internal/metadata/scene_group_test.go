// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDeriveMediaFactsSceneGroupCasing(t *testing.T) {
	t.Parallel()
	const prefix = "Example.Movie.2026.1080p.BluRay.x264"
	for _, test := range []struct {
		name          string
		fileGroup     string
		sceneName     string
		confirmed     bool
		detectorError error
		tag           string
		overrides     api.ReleaseNameOverrides
		wantGroup     string
		wantTag       string
	}{
		{
			name:      "mixed case",
			fileGroup: "mixed",
			sceneName: prefix + "-MiXeD",
			confirmed: true,
			wantGroup: "MiXeD",
			wantTag:   "-MiXeD",
		},
		{
			name:      "uppercase",
			fileGroup: "grp",
			sceneName: prefix + "-GRP",
			confirmed: true,
			wantGroup: "GRP",
			wantTag:   "-GRP",
		},
		{
			name:      "already correct",
			fileGroup: "MiXeD",
			sceneName: prefix + "-MiXeD",
			confirmed: true,
			wantGroup: "MiXeD",
			wantTag:   "-MiXeD",
		},
		{
			name:      "intentionally lowercase",
			fileGroup: "lower",
			sceneName: prefix + "-lower",
			confirmed: true,
			wantGroup: "lower",
			wantTag:   "-lower",
		},
		{
			name:      "restore lowercase",
			fileGroup: "LOWER",
			sceneName: prefix + "-lower",
			confirmed: true,
			wantGroup: "lower",
			wantTag:   "-lower",
		},
		{
			name:      "different group",
			fileGroup: "mixed",
			sceneName: prefix + "-OTHER",
			confirmed: true,
			wantGroup: "mixed",
			wantTag:   "-mixed",
		},
		{
			name:      "unconfirmed name",
			fileGroup: "mixed",
			sceneName: prefix + "-MiXeD",
			wantGroup: "mixed",
			wantTag:   "-mixed",
		},
		{
			name:      "no scene name",
			fileGroup: "mixed",
			confirmed: true,
			wantGroup: "mixed",
			wantTag:   "-mixed",
		},
		{
			name:      "no scene group",
			fileGroup: "mixed",
			sceneName: prefix,
			confirmed: true,
			wantGroup: "mixed",
			wantTag:   "-mixed",
		},
		{
			name:      "no filename group",
			sceneName: prefix + "-MiXeD",
			confirmed: true,
		},
		{
			name:      "different effective tag",
			fileGroup: "mixed",
			tag:       "-OTHER",
			sceneName: prefix + "-MiXeD",
			confirmed: true,
			wantGroup: "MiXeD",
			wantTag:   "-OTHER",
		},
		{
			name:      "explicit tag",
			fileGroup: "mixed",
			sceneName: prefix + "-MiXeD",
			confirmed: true,
			overrides: api.ReleaseNameOverrides{Tag: new("UserGroup")},
			wantGroup: "UserGroup",
			wantTag:   "-UserGroup",
		},
		{
			name:      "explicit same-group casing",
			fileGroup: "mixed",
			sceneName: prefix + "-MiXeD",
			confirmed: true,
			overrides: api.ReleaseNameOverrides{Tag: new("mIxEd")},
			wantGroup: "mIxEd",
			wantTag:   "-mIxEd",
		},
		{
			name:      "explicit empty tag",
			fileGroup: "mixed",
			sceneName: prefix + "-MiXeD",
			confirmed: true,
			overrides: api.ReleaseNameOverrides{Tag: new("")},
		},
		{
			name:      "omit tag",
			fileGroup: "mixed",
			sceneName: prefix + "-MiXeD",
			confirmed: true,
			overrides: api.ReleaseNameOverrides{NoTag: new(true)},
		},
		{
			name:          "recoverable NFO error",
			fileGroup:     "mixed",
			sceneName:     prefix + "-MiXeD",
			confirmed:     true,
			detectorError: newSceneNFOError(errors.New("NFO unavailable")),
			wantGroup:     "MiXeD",
			wantTag:       "-MiXeD",
		},
		{
			name:          "failed detection",
			fileGroup:     "mixed",
			sceneName:     prefix + "-MiXeD",
			confirmed:     true,
			detectorError: errors.New("lookup unavailable"),
			wantGroup:     "mixed",
			wantTag:       "-mixed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			filename := prefix
			if test.fileGroup != "" {
				filename += "-" + test.fileGroup
			}
			filename += ".mkv"
			release := ParseReleaseInfo(filename)
			if release.Group != test.fileGroup {
				t.Fatalf("filename group = %q, want %q", release.Group, test.fileGroup)
			}
			tag := DetectTag(filename)
			if test.tag != "" {
				tag = test.tag
			}
			service := NewService(&fakeRepo{}, WithSceneDetector(staticSceneDetector{
				result: SceneResult{IsScene: test.confirmed, SceneName: test.sceneName},
				err:    test.detectorError,
			}))
			meta, err := service.deriveMediaFacts(t.Context(), preparationstate.State{
				SourcePath:           filename,
				VideoPath:            filename,
				Release:              release,
				Tag:                  tag,
				ReleaseNameOverrides: test.overrides,
			})
			if err != nil {
				t.Fatal(err)
			}
			if meta.Release.Group != test.wantGroup || meta.Tag != test.wantTag {
				t.Fatalf("resolved group/tag = %q/%q, want %q/%q", meta.Release.Group, meta.Tag, test.wantGroup, test.wantTag)
			}
			for _, name := range []string{
				meta.ReleaseName,
				meta.ReleaseNameClean,
				meta.GeneratedName.Render().Name,
				meta.GeneratedReleaseNames.IncludeEpisodeTitle.Name,
				meta.GeneratedReleaseNames.OmitEpisodeTitle.Name,
			} {
				if name == "" || name != meta.ReleaseNameNoTag+test.wantTag {
					t.Fatalf("generated name = %q, want %q", name, meta.ReleaseNameNoTag+test.wantTag)
				}
			}
		})
	}
}

func TestSceneGroupCasingUsesSRRDBReleaseName(t *testing.T) {
	t.Parallel()
	const filename = "example.movie.2026.1080p.bluray.x264-mixed.mkv"
	const sceneName = "Example.Movie.2026.1080p.BluRay.x264-MiXeD"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		//pathpolicy:allow SRRDB HTTP route, not a local filesystem path.
		case "/v1/search/r:" + strings.TrimSuffix(filename, ".mkv"):
			if err := json.NewEncoder(w).Encode(srrdbResponse{
				ResultsCount: 1,
				Results: []srrdbSearchResult{{
					Release: sceneName,
					IMDBID:  "tt1234567",
					HasNFO:  "no",
				}},
			}); err != nil {
				t.Errorf("write scene response: %v", err)
			}
		case "/v1/details/" + sceneName:
			if err := json.NewEncoder(w).Encode(srrdbDetailsResponse{ArchivedFiles: []srrdbArchivedFile{{Name: filename}}}); err != nil {
				t.Errorf("write archive response: %v", err)
			}
		default:
			t.Errorf("unexpected scene request %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	base := t.TempDir()
	tagsPath := filepath.Join(base, "tags.json")
	if err := os.WriteFile(tagsPath, []byte(`{"mixed":{"template":"scene-template"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(base, filename)
	if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(&stubRepo{},
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(base, "db.sqlite")}}),
		WithMediaInfoExporter(&stubMediaInfo{}),
		WithSceneDetector(newSRRDBDetector(server.Client(), server.URL, filepath.Join(base, "cache"), filepath.Join(base, "nfo"))),
	)
	service.tagsPath = tagsPath
	meta, err := service.collectSourceEvidence(t.Context(), testCollectionRequest(t, api.Request{SourcePath: source}))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Tag != "-mixed" || meta.Release.Group != "mixed" {
		t.Fatalf("source group/tag = %q/%q, want lowercase filename evidence", meta.Release.Group, meta.Tag)
	}
	meta, err = service.deriveMediaFacts(t.Context(), meta)
	if err != nil {
		t.Fatal(err)
	}
	if meta.TagOverride == nil || meta.TagOverride.Template != "scene-template" {
		t.Fatalf("tag metadata override lost: %#v", meta.TagOverride)
	}
	if !meta.Scene || meta.SceneName != sceneName || meta.SceneRenamed {
		t.Fatalf("unexpected scene facts: scene=%t name=%q renamed=%t", meta.Scene, meta.SceneName, meta.SceneRenamed)
	}
	if meta.Tag != "-MiXeD" || meta.Release.Group != "MiXeD" || !strings.HasSuffix(meta.ReleaseName, "-MiXeD") {
		t.Fatalf("scene group/tag/name = %q/%q/%q", meta.Release.Group, meta.Tag, meta.ReleaseName)
	}
}
