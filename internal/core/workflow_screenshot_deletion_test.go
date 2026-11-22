// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/services/screenshots"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowDeletesConsecutiveImportedScreenshots(t *testing.T) {
	for _, tc := range []struct {
		name string
		commands [][]api.PublicResourceID
	}{
		{name: "forward", commands: [][]api.PublicResourceID{{"1"}, {"2"}}},
		{name: "reverse", commands: [][]api.PublicResourceID{{"2"}, {"1"}}},
		{name: "batch", commands: [][]api.PublicResourceID{{"1", "2"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			repo, err := db.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			sourcePath := filepath.Join(t.TempDir(), "sample.mkv")
			if err := os.WriteFile(sourcePath, []byte("synthetic video"), 0o600); err != nil {
				t.Fatal(err)
			}
			urls := []string{"https://one.example/shared.png", "https://two.example/shared.png", "https://three.example/shared.png"}
			if err := repo.SaveTrackerMetadata(ctx, api.TrackerMetadata{
				SourcePath: sourcePath,
				Tracker:    "AITHER",
				ImageURLs:  urls,
			}); err != nil {
				t.Fatal(err)
			}
			tmpRoot := t.TempDir()
			releaseDir, _, err := paths.ReleaseTempDirFor(tmpRoot, sourcePath, api.ReleaseInfo{})
			if err != nil {
				t.Fatal(err)
			}
			trackerDir := filepath.Join(releaseDir, "aither")
			if err := os.MkdirAll(trackerDir, 0o700); err != nil {
				t.Fatal(err)
			}

			service := screenshots.NewServiceWithRepo(config.Config{}, api.NopLogger{}, tmpRoot, nil, repo)
			resource := workflowMediaPrivateArtifacts{
				screenshotService: service,
				screenshotSubject: api.ScreenshotSubject{
					SourcePath: sourcePath,
					MediaBinding: api.PreparedMediaBinding{
						SourcePath:               sourcePath,
						PreparedMediaFingerprint: "test-prepared-media",
						PreparedGeneration:       1,
					},
				},
			}
			var snapshot api.MediaArtifactSet
			artifacts := make([]string, len(urls))
			for i, rawURL := range urls {
				digest := sha256.Sum256([]byte(rawURL))
				artifacts[i] = filepath.Join(trackerDir, fmt.Sprintf("shared_%02d_%x.png", i+1, digest[:6]))
				file, err := os.OpenFile(artifacts[i], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				if err := errors.Join(png.Encode(file, image.NewGray(image.Rect(0, 0, 1, 1))), file.Close()); err != nil {
					t.Fatal(err)
				}
				resource.Screenshots = append(resource.Screenshots, api.ScreenshotImage{Path: artifacts[i]})
				snapshot.Artifacts = append(snapshot.Artifacts, api.MediaArtifact{
					ID:   api.PublicResourceID(strconv.Itoa(i + 1)),
					Kind: api.MediaArtifactScreenshot,
				})
			}

			for _, ids := range tc.commands {
				retained, err := resource.DeleteArtifacts(ctx, snapshot, ids)
				if err != nil {
					t.Fatal(err)
				}
				updated, ok := retained.(workflowMediaPrivateArtifacts)
				if !ok {
					t.Fatalf("unexpected retained media resource: %T", retained)
				}
				if err := updated.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				resource = updated
				snapshot.Artifacts = slices.DeleteFunc(snapshot.Artifacts, func(artifact api.MediaArtifact) bool { return slices.Contains(ids, artifact.ID) })
			}

			for _, path := range artifacts[:2] {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("deleted screenshot still present or unreadable: %v", err)
				}
			}
			stored, err := repo.ListTrackerMetadataByPath(ctx, sourcePath)
			if err != nil || len(stored) != 1 || !slices.Equal(stored[0].ImageURLs, urls[2:]) {
				t.Fatalf("remaining tracker URL: records=%#v err=%v", stored, err)
			}
			if len(resource.Screenshots) != 1 || resource.Screenshots[0].Path != artifacts[2] {
				t.Fatalf("remaining screenshot selection: %#v", resource.Screenshots)
			}
			reusable, err := service.ReusableTrackerImageLinks(ctx, sourcePath, "", api.ReleaseInfo{})
			if err != nil || len(reusable) != 1 || reusable[0].Path != artifacts[2] || reusable[0].URL != urls[2] {
				t.Fatalf("remaining reusable link: links=%#v err=%v", reusable, err)
			}
		})
	}
}
