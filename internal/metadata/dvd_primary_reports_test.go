// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	paths "github.com/autobrr/upbrr/internal/pathing/layout"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/pkg/api"
)

type dvdPrimaryWitnessAnalyzer struct{}

func (dvdPrimaryWitnessAnalyzer) Analyze(_ context.Context, path string) (string, []byte, error) {
	marker, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read synthetic analyzer input: %w", err)
	}
	text := "General\nTitle : " + string(marker) + "\n"
	data := fmt.Appendf(nil, `{"media":{"track":[{"@type":"General","Title":%q,"Duration":"120"},{"@type":"Video","Width":"720","Height":"576"}]}}`, marker)
	return text, data, nil
}

func TestDVDPreparationPublishesRootReportsForCanonicalPrimary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		usable  []bool
		primary int
	}{
		{"partial first disc", []bool{false, true}, 1},
		{"ordinary single disc", []bool{true}, 0},
		{"multiple usable discs", []bool{true, true}, 0},
		{"no usable disc", []bool{false, false}, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "Example.Movie.2026.PAL.DVD")
			for i, usable := range tc.usable {
				discRoot := root
				if len(tc.usable) > 1 {
					discRoot = filepath.Join(root, fmt.Sprintf("Disc %d", i+1))
				}
				videoTS := filepath.Join(discRoot, "VIDEO_TS")
				if err := os.MkdirAll(videoTS, 0o700); err != nil {
					t.Fatal(err)
				}
				ifo := "VIDEO_TS.IFO"
				if !usable && i > 0 {
					// A title-set IFO without a VOB is also only fallback evidence.
					ifo = "VTS_01_0.IFO"
				}
				marker := fmt.Sprintf("synthetic-disc-%d", i+1)
				if usable {
					ifo = "VTS_01_0.IFO"
					if err := os.WriteFile(filepath.Join(videoTS, "VTS_01_1.VOB"), []byte(marker), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(filepath.Join(videoTS, ifo), []byte(marker), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(t.TempDir(), "metadata.sqlite")}}
			repo, err := db.OpenContext(t.Context(), cfg.MainSettings.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = repo.Close() })
			if err := repo.MigrateContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			service := newTestService(t, repo,
				WithConfig(cfg),
				WithMediaInfoExporter(mediainfo.NewService(nil, dvdPrimaryWitnessAnalyzer{})),
				WithSceneDetector(stubSceneDetector{}),
				WithIMDBClient(&stubIMDB{}),
				WithTVDBClient(&stubTVDB{}),
				WithTVmazeClient(&stubTVmaze{}),
			)
			layout, err := sourcelayout.Resolve(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			input := api.PrepareInput{
				SourcePath:   root,
				Search:       api.ClientSearchPolicy{Skip: true},
				Instructions: api.ReleaseFactInstructions{Category: new(api.CanonicalCategoryMovie)},
			}
			req := preparationstate.Request{
				Input:             input,
				Layout:            layout,
				Manifest:          api.SourceManifest{SourcePath: root},
				SourceFingerprint: "synthetic-dvd",
			}
			collector, err := preparedrelease.NewEvidenceCollector(service)
			if err != nil {
				t.Fatal(err)
			}
			result, err := collector.Collect(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
			if err != nil {
				t.Fatal(err)
			}
			releaseRoot, _, err := paths.ReleaseTempDirFor(tmpRoot, root, ParseReleaseInfo(root))
			if err != nil {
				t.Fatal(err)
			}

			assertPair := func(text, json string, i int) {
				t.Helper()
				marker := fmt.Sprintf("synthetic-disc-%d", i+1)
				for _, report := range []string{text, json} {
					data, err := os.ReadFile(report)
					if err != nil || !strings.Contains(string(data), marker) {
						t.Errorf("report %s does not identify %s: %s (%v)", filepath.Base(report), marker, data, err)
					}
				}
			}
			if len(result.Resources.Discs) != len(tc.usable) {
				t.Fatalf("disc resources = %d, want %d", len(result.Resources.Discs), len(tc.usable))
			}
			for i, disc := range result.Resources.Discs {
				assertPair(disc.MediaInfoTextPath, disc.MediaInfoJSONPath, i)
			}
			jsonPath, textPath := filepath.Join(releaseRoot, "MediaInfo.json"), filepath.Join(releaseRoot, "mediainfo.txt")
			assertNoPrimary := func(facts preparedrelease.CollectedFacts) {
				t.Helper()
				if facts.Disc.PrimaryDiscID != "" || facts.Resources.MediaInfoJSONPath != "" || facts.Resources.MediaInfoTextPath != "" {
					t.Error("fallback-only collection must not expose a primary report")
				}
				for _, report := range []string{jsonPath, textPath, filepath.Join(releaseRoot, "mediainfo.identity.json")} {
					if _, err := os.Stat(report); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("fallback-only collection left a root report: %v", err)
					}
				}
			}
			if tc.primary < 0 {
				assertNoPrimary(result)
				return
			}
			if result.Disc.PrimaryDiscID != result.Resources.Discs[tc.primary].ID {
				t.Fatalf("canonical primary ID = %q, want %q", result.Disc.PrimaryDiscID, result.Resources.Discs[tc.primary].ID)
			}
			if result.Resources.MediaInfoJSONPath != jsonPath || result.Resources.MediaInfoTextPath != textPath {
				t.Errorf("primary compatibility paths do not point to root report pair")
			}
			assertPair(textPath, jsonPath, tc.primary)

			if tc.primary == 1 {
				// Reprepare the same collection after its sole usable title disappears.
				path := filepath.Join(root, "Disc 2", "VIDEO_TS", "VTS_01_1.VOB")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				req.SourceFingerprint = "changed-synthetic-dvd"
				reloaded, err := collector.Collect(t.Context(), req)
				if err != nil {
					t.Fatal(err)
				}
				assertNoPrimary(reloaded)
				for i, disc := range reloaded.Resources.Discs {
					assertPair(disc.MediaInfoTextPath, disc.MediaInfoJSONPath, i)
				}
			}
		})
	}
}
