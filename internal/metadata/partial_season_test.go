// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/metadata/tmdb"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPartialSeasonPreparationRetainsCoverageAcrossRefreshAndRestart(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dbPath := filepath.Join(base, "db.sqlite")
	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	analyzer := &packAnalyzer{}
	service := newTestService(t, repo, WithMediaInfoExporter(mediainfo.NewService(nil, analyzer)), WithSceneDetector(stubSceneDetector{}),
		WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		WithTMDBClient(&stubTMDB{metadata: tmdb.MetadataResult{Title: "Example Show", Year: 2026}}),
		WithIMDBClient(&stubIMDB{}), WithTVDBClient(&stubTVDB{}), WithTVmazeClient(&stubTVmaze{}))
	collector, err := preparedrelease.NewEvidenceCollector(service)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := externalidentity.NewWithCandidateSource(repo, collector)
	if err != nil {
		t.Fatal(err)
	}
	open := func() *preparedrelease.Module {
		t.Helper()
		m, err := preparedrelease.New(repo, identity, collector)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	module := open()
	for _, part := range []int{1, 2} {
		source := filepath.Join(base, fmt.Sprintf("Example.Show.S01.Part.%d.1080p.WEB-DL-GRP", part))
		if err := os.Mkdir(source, 0o700); err != nil {
			t.Fatal(err)
		}
		first := 1 + (part-1)*8
		write := func(episode int) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(source, fmt.Sprintf("Example.Show.S01E%02d.mkv", episode)), []byte("synthetic video"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		write(first)
		write(first + 1)
		input := api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{Identity: api.ExternalIDOverrides{TMDBID: new(1234567)}}}
		check := func(want []int) api.PrepareResult {
			t.Helper()
			resolved, err := module.ResolveInput(t.Context(), input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateReplace, Values: api.ReleaseCorrectionValues{
				Identity:    input.Instructions.Identity,
				Metadata:    input.Instructions.Metadata,
				ReleaseName: input.Instructions.ReleaseName,
			}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := module.PrepareResolved(t.Context(), resolved)
			if err != nil {
				t.Fatal(err)
			}
			coverage := result.Release.Episode.Coverage
			if !result.Release.Episode.Pack || coverage.Part != fmt.Sprintf("Part.%d", part) || !slices.Equal(coverage.Episodes, want) || coverage.Status != api.MetadataEvidenceStatusComplete {
				t.Fatalf("prepared episode=%+v", result.Release.Episode)
			}
			ref := api.ReleaseRef{SourcePath: source, Generation: result.Release.Generation}
			display, err := module.ResolveDisplay(t.Context(), ref)
			if err != nil || !strings.Contains(display.ReleaseName, coverage.Part) {
				t.Fatalf("display=%+v err=%v", display, err)
			}
			upload, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
			if err != nil || !slices.Equal(upload.EpisodeCoverage.Episodes, want) || upload.EpisodeCoverage.Part != coverage.Part {
				t.Fatalf("upload coverage=%+v err=%v", upload.EpisodeCoverage, err)
			}
			duplicate, err := module.ResolveDuplicateSubject(t.Context(), api.DuplicateCheckInput{Release: ref})
			if err != nil || !slices.Equal(duplicate.EpisodeCoverage.Episodes, want) || duplicate.EpisodeCoverage.Part != coverage.Part {
				t.Fatalf("duplicate coverage=%+v err=%v", duplicate.EpisodeCoverage, err)
			}
			return result
		}
		original := check([]int{first, first + 1})
		module = open()
		reused := check([]int{first, first + 1})
		if reused.Release.Generation != original.Release.Generation {
			t.Fatal("restart did not reuse the exact generation")
		}
		input.ExternalFreshness = api.ExternalFreshnessRefresh
		check([]int{first, first + 1})
		input.ExternalFreshness = api.ExternalFreshnessReuse
		tag := "OTHER"
		input.Instructions.ReleaseName.Tag = &tag
		refreshed := check([]int{first, first + 1})
		if refreshed.Release.Generation == original.Release.Generation || !strings.HasSuffix(refreshed.Release.Naming.ReleaseName, "-OTHER") {
			t.Fatalf("metadata correction did not refresh the name: original=%d refreshed=%d name=%q", original.Release.Generation, refreshed.Release.Generation, refreshed.Release.Naming.ReleaseName)
		}
		write(first + 2)
		changed := check([]int{first, first + 1, first + 2})
		if changed.Release.Generation == refreshed.Release.Generation {
			t.Fatal("changed inventory reused old coverage")
		}
		input.Instructions.ReleaseName.Season = new("S02")
		resolved, err := module.ResolveInput(t.Context(), input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateReplace, Values: api.ReleaseCorrectionValues{
			Identity:    input.Instructions.Identity,
			Metadata:    input.Instructions.Metadata,
			ReleaseName: input.Instructions.ReleaseName,
		}})
		if err != nil {
			t.Fatal(err)
		}
		conflicting, err := module.PrepareResolved(t.Context(), resolved)
		if err != nil {
			t.Fatal(err)
		}
		if conflicting.Release.Episode.Coverage.Part != fmt.Sprintf("Part.%d", part) || conflicting.Release.Episode.Coverage.Status != api.MetadataEvidenceStatusContradictory {
			t.Fatalf("season correction invented membership: %+v", conflicting.Release.Episode)
		}
		input.Instructions.ReleaseName.Season = nil
		check([]int{first, first + 1, first + 2})
	}
	for _, target := range analyzer.targets {
		first := "Example.Show.S01E01.mkv"
		if strings.Contains(filepath.Base(filepath.Dir(target)), "Part.2") {
			first = "Example.Show.S01E09.mkv"
		}
		if filepath.Base(target) != first {
			t.Fatalf("inspected a non-primary pack member: %v", analyzer.targets)
		}
	}
}

func TestPartialSeasonNamesRetainIdentityAcrossMediaFormats(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ kind, disc, source string }{
		{"DISC", "BDMV", "BluRay"},
		{"DISC", "DVD", "NTSC DVD"},
		{"DISC", "HDDVD", "HDDVD"},
		{"DVDRIP", "", "DVD"},
		{"REMUX", "", "BluRay"},
		{"WEBDL", "", "Web"},
	} {
		t.Run(tc.kind+tc.disc, func(t *testing.T) {
			name := BuildReleaseName(api.ReleaseNameRequest{
				Category:   "TV",
				Type:       tc.kind,
				DiscType:   tc.disc,
				Source:     tc.source,
				Title:      "Example Show",
				Season:     "S01",
				Part:       "Part.2",
				Resolution: "1080p",
				Audio:      "DD2.0",
				VideoCodec: "H.264",
				DVDSize:    "DVD9",
			}, api.NopLogger{})
			if !strings.Contains(name.Name, "Part.2") {
				t.Fatalf("part lost from generated name: %+v", name)
			}
		})
	}
}
