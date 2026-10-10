// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/externalidentity"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/aither"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPreparedDatedSpecialDuplicateScope(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		source    string
		overrides api.ReleaseNameOverrides
	}{
		{name: "dated special source", source: "Example.Series.S00E01.2026-01-01.1080p.WEB-DL-GRP.mkv"},
		{
			name:      "dated source corrected to special",
			source:    "Example.Series.2026-01-01.1080p.WEB-DL-GRP.mkv",
			overrides: api.ReleaseNameOverrides{Season: new("S00"), Episode: new("E01")},
		},
		{
			name:      "special with manual date",
			source:    "Example.Series.S00E01.1080p.WEB-DL-GRP.mkv",
			overrides: api.ReleaseNameOverrides{ManualDate: new("2026-01-01")},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := filepath.Join(t.TempDir(), test.source)
			if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
				t.Fatal(err)
			}
			dbPath := filepath.Join(t.TempDir(), "metadata.sqlite")
			module, repo := openSpecialPreparation(t, dbPath)
			input := api.PrepareInput{
				SourcePath: source,
				Instructions: api.ReleaseFactInstructions{
					Category:    new(api.CanonicalCategoryTV),
					ReleaseName: test.overrides,
					Identity: api.ExternalIDOverrides{
						TMDBID:   new(0),
						IMDBID:   new(0),
						TVDBID:   new(0),
						TVmazeID: new(0),
						MALID:    new(0),
					},
				},
			}
			prepared, err := module.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, _ = openSpecialPreparation(t, dbPath)
			reused, err := module.Prepare(t.Context(), input)
			if err != nil || reused.Release.Generation != prepared.Release.Generation {
				t.Fatalf("restart preparation: generation=%d error=%v", reused.Release.Generation, err)
			}
			facts := reused.Release.Episode
			if facts.SeasonLabel != "S00" || facts.Episode != 1 || facts.DailyDate != "" {
				t.Fatalf("special facts retain date scope: %+v", facts)
			}
			upload, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{
				Release: api.ReleaseRef{SourcePath: source, Generation: reused.Release.Generation},
			})
			if err != nil {
				t.Fatal(err)
			}
			// The offline collector clears provider IDs. Supply only the matching
			// title snapshot required by AITHER; episode facts remain its real output.
			upload.Identity.TMDBID = 555001
			upload.ProviderMetadata = api.SourceScopedMetadata{
				SourcePath: upload.Identity.SourcePath,
				Generation: upload.Identity.Generation,
				TMDB: &api.TMDBMetadata{
					TMDBID:   555001,
					Category: "TV",
					Title:    "Example Series",
				},
			}
			projection, failure := impl.MustNewRegistry().ProjectRelease(t.Context(), trackers.PreparationInput{
				Tracker: "AITHER",
				Meta:    upload,
				Logger:  api.NopLogger{},
			}, "", "", "")
			if failure != nil {
				t.Fatal(failure.Message())
			}
			if projection.DuplicateTarget.Date != "" || !strings.Contains(projection.UploadReleaseName, "S00E01") {
				t.Fatalf("special projection diverges: %+v", projection)
			}
			search := dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID}
			withoutCandidates := dupe.Evaluate(projection.DuplicateTarget, nil, *aither.Profile().DupePolicy, search)
			for _, candidateName := range []string{
				"Example.Series.S00E02.1080p.WEB-DL-GRP",
				"Example.Series.S00E02.2026-01-01.1080p.WEB-DL-GRP",
				"Example.Series.S00E02-E03.1080p.WEB-DL-GRP",
				"Example.Series.S00E02-E03.2026-01-01.1080p.WEB-DL-GRP",
				"Example.Series.S01E01.1080p.WEB-DL-GRP",
				"Example.Series.S00E01.2026-01-01.1080p.WEB-DL-GRP",
				"Example.Series.S00E01-E02.1080p.WEB-DL-GRP",
				"Example.Series.S00E01-E02.2026-01-01.1080p.WEB-DL-GRP",
			} {
				candidate := dupe.NormalizeCandidate(api.DupeEntry{Name: candidateName}, "AITHER")
				result := dupe.Evaluate(projection.DuplicateTarget, []dupe.TrackerCandidate{candidate}, *aither.Profile().DupePolicy,
					search)
				if candidate.Episode == 1 && candidate.Season == 0 {
					if !result.RequiresAction && !result.Blocks || result.Candidates[0].Relation == api.DupeRelationCoexists {
						t.Fatalf("same special ignored: relation=%s action=%t review=%v", result.Candidates[0].Relation, result.RequiresAction, result.ReviewReasons)
					}
				} else if result.Candidates[0].Relation != api.DupeRelationCoexists || result.RequiresAction != withoutCandidates.RequiresAction ||
					!reflect.DeepEqual(result.ReviewReasons, withoutCandidates.ReviewReasons) {
					t.Fatalf("unrelated episode %q: relation=%s action=%t review=%v", candidateName, result.Candidates[0].Relation, result.RequiresAction, result.ReviewReasons)
				}
			}
		})
	}
}

func TestPreparedSpecialSourceEvidenceSurvivesRestartAndProjection(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		source   string
		files    []string
		season   int
		label    string
		episode  int
		date     string
		multiple bool
		pack     bool
	}{
		{
			name:    "individual special",
			source:  "Example.Series.S00E01.1080p.WEB-DL-GRP.mkv",
			label:   "S00",
			episode: 1,
		},
		{
			name:   "daily release preserves date",
			source: "Example.Series.2026-01-01.1080p.WEB-DL-GRP.mkv",
			date:   "2026-01-01",
		},
		{
			name:    "title digits are not episode evidence",
			source:  "Example42.Series.S00E01.1080p.WEB-DL-GRP.mkv",
			label:   "S00",
			episode: 1,
		},
		{
			name:     "seasonless combined tokens survive correction",
			source:   "Example.Series.E01E02.1080p.WEB-DL-GRP.mkv",
			multiple: true,
		},
		{name: "unknown season", source: "Example.Series.1080p.WEB-DL-GRP.mkv"},
		{
			name:    "episode without season",
			source:  "Example.Series.E01.1080p.WEB-DL-GRP.mkv",
			episode: 1,
		},
		{
			name:   "special season pack",
			source: "Example.Series.S00.1080p.WEB-DL-GRP.mkv",
			label:  "S00",
			pack:   true,
		},
		{
			name:   "zero episode",
			source: "Example.Series.S00E00.1080p.WEB-DL-GRP.mkv",
			label:  "S00",
		},
		{
			name:   "zero episode with absolute evidence",
			source: "[GRP] Example.Series.S00E00.02 (1080p).mkv",
			label:  "S00",
		},
		{
			name:    "positive season",
			source:  "Example.Series.S01E01.1080p.WEB-DL-GRP.mkv",
			season:  1,
			label:   "S01",
			episode: 1,
		},
		{
			name:     "multiple tokens",
			source:   "Example.Series.S00E01E02.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "episode range",
			source:   "Example.Series.S00E01-E02.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "range without repeated E",
			source:   "Example.Series.S00E01-02.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "versioned episode range",
			source:   "Example.Series.S00E01-E02v2.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "versioned range without repeated E",
			source:   "Example.Series.S00E01-02v2.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "versioned separate episode member",
			source:   "Example.Series.S00E01,E02v2.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "versioned first range endpoint",
			source:   "Example.Series.S00E01v2-02.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "versioned adjoining episode members",
			source:   "Example.Series.S00E01v2E02v3.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "zero episode member",
			source:   "Example.Series.S00E01,E00.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "zero first comma members",
			source:   "Example.Series.S00E00,E01.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			multiple: true,
		},
		{
			name:     "zero first semicolon members",
			source:   "Example.Series.S00E00;E02.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			multiple: true,
		},
		{
			name:     "zero first out of width member",
			source:   "Example.Series.S00E00,E1000.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			multiple: true,
		},
		{
			name:     "zero first cross season range",
			source:   "Example.Series.S00E00-S01E01.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			multiple: true,
		},
		{
			name:     "zero first cross season zero members",
			source:   "Example.Series.S00E00,S01E00.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			multiple: true,
		},
		{
			name:     "versioned zero first members",
			source:   "Example.Series.S00E00v2,E01v3.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			multiple: true,
		},
		{
			name:     "zero first distinct file members",
			source:   "Example.Series.S00E00.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S00E00.mkv", "Example.Series.S00E01.mkv"},
			label:    "S00",
			multiple: true,
			pack:     true,
		},
		{
			name:     "provisional folder with combined selected file",
			source:   "Example.Series.S00E00.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S00E00,E01.mkv"},
			label:    "S00",
			multiple: true,
		},
		{
			name:     "zero file member",
			source:   "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S00E01.mkv", "Example.Series.S00E00.mkv"},
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "versioned zero file member",
			source:   "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S00E01.mkv", "Example.Series.S00E00v2.mkv"},
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "other season zero file member",
			source:   "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S00E01.mkv", "Example.Series.S01E00.mkv"},
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "out of width range member",
			source:   "Example.Series.S00E01-E1000.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "bare wide range endpoint",
			source:   "Example.Series.S00E01-1000.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "bare versioned additive endpoint",
			source:   "Example.Series.S00E01+1001v2.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "bare wider ampersand endpoint",
			source:   "Example.Series.S00E01&10000.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "zero first bare wide endpoint",
			source:   "Example.Series.S00E00-1000.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			multiple: true,
		},
		{
			name:     "wide first bare zero endpoint",
			source:   "Example.Series.S00E1000+00000.1080p.WEB-DL-GRP.mkv",
			episode:  1000,
			multiple: true,
		},
		{
			name:     "unsupported secondary season width",
			source:   "Example.Series.S00E01-S001E02.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "wide secondary season",
			source:   "Example.Series.S00E01-S10000E02.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "overflowing secondary season",
			source:   "Example.Series.S00E01-S999999999999999999999999E01.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "bare wide endpoint in selected file",
			source:   "Example.Series.S00E00.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S00E01-1000.mkv"},
			label:    "S00",
			multiple: true,
		},
		{
			name:     "range followed by date",
			source:   "Example.Series.S00E01-02-2026-01-01.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:    "hyphen date annotation",
			source:  "Example.Series.S00E01-2026-01-01.1080p.WEB-DL-GRP.mkv",
			label:   "S00",
			episode: 1,
		},
		{
			name:    "dot date annotation",
			source:  "Example.Series.S00E01-2026.01.01.1080p.WEB-DL-GRP.mkv",
			label:   "S00",
			episode: 1,
		},
		{
			name:     "out of width member with repeated season",
			source:   "Example.Series.S00E01-S00E1000.1080p.WEB-DL-GRP.mkv",
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "out of width conflicting file member",
			source:   "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S00E01.mkv", "Example.Series.S00E1000.mkv"},
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "versioned conflicting file episode",
			source:   "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S00E01.mkv", "Example.Series.S00E02v2.mkv"},
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:    "versioned individual episode",
			source:  "Example.Series.S00E01v2.1080p.WEB-DL-GRP.mkv",
			label:   "S00",
			episode: 1,
		},
		{
			name:    "same episode files",
			source:  "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:   []string{"Example.Series.S00E01.Part1.mkv", "Example.Series.S00E01.Part2.mkv"},
			label:   "S00",
			episode: 1,
		},
		{
			name:     "underscore conflicting file episodes",
			source:   "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:    []string{"Example_S00E01_Part1.mkv", "Example_S00E02_Part2.mkv"},
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:    "underscore same episode files",
			source:  "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:   []string{"Example_S00E01_Part1.mkv", "Example_S00E01_Part2.mkv"},
			label:   "S00",
			episode: 1,
		},
		{
			name:     "conflicting file episodes",
			source:   "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S00E01.mkv", "Example.Series.S00E02.mkv"},
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:     "conflicting episode-only files",
			source:   "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.E01.mkv", "Example.Series.E02.mkv"},
			label:    "S00",
			episode:  1,
			multiple: true,
		},
		{
			name:    "same episode-only files",
			source:  "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:   []string{"Example.Series.E01.Part1.mkv", "Example.Series.E01.Part2.mkv"},
			label:   "S00",
			episode: 1,
		},
		{
			name:     "conflicting file season",
			source:   "Example.Series.S00E01.1080p.WEB-DL-GRP",
			files:    []string{"Example.Series.S01E01.mkv"},
			label:    "S00",
			episode:  1,
			multiple: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := filepath.Join(t.TempDir(), test.source)
			if len(test.files) == 0 {
				if err := os.WriteFile(source, []byte("synthetic media"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(source, 0o700); err != nil {
					t.Fatal(err)
				}
				for _, name := range test.files {
					if err := os.WriteFile(filepath.Join(source, name), []byte("synthetic media"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}
			dbPath := filepath.Join(t.TempDir(), "metadata.sqlite")
			module, repo := openSpecialPreparation(t, dbPath)
			input := api.PrepareInput{
				SourcePath: source,
				Instructions: api.ReleaseFactInstructions{
					Category: new(api.CanonicalCategoryTV),
					Identity: api.ExternalIDOverrides{
						TMDBID:   new(0),
						IMDBID:   new(0),
						TVDBID:   new(0),
						TVmazeID: new(0),
						MALID:    new(0),
					},
				},
			}
			initial, err := module.Prepare(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			facts := initial.Release.Episode
			if facts.Season != test.season || facts.SeasonLabel != test.label || facts.Episode != test.episode ||
				facts.MultipleEpisodes != test.multiple || facts.Pack != test.pack || facts.DailyDate != test.date {
				t.Fatalf("source evidence=%+v", facts)
			}
			if test.multiple {
				input.Instructions.ReleaseName = api.ReleaseNameOverrides{Season: new("S00"), Episode: new("E01")}
				corrected, err := module.Prepare(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				if corrected.Release.Episode.SeasonLabel != "S00" || corrected.Release.Episode.Episode != 1 || !corrected.Release.Episode.MultipleEpisodes {
					t.Fatalf("coordinate correction erased source membership: %+v", corrected.Release.Episode)
				}
				initial = corrected
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, repo = openSpecialPreparation(t, dbPath)
			reused, err := module.Prepare(t.Context(), input)
			if err != nil || reused.Release.Generation != initial.Release.Generation || !reflect.DeepEqual(reused.Release.Episode, initial.Release.Episode) {
				t.Fatalf("restart lost episode facts: episode=%+v generation=%d error=%v", reused.Release.Episode, reused.Release.Generation, err)
			}
			assertSpecialEpisodeProjection(t, module, reused.Release)
			persisted, err := repo.LoadPreparedRelease(t.Context(), source)
			if err != nil || !reflect.DeepEqual(persisted.Episode, reused.Release.Episode) {
				t.Fatalf("persisted episode=%+v error=%v", persisted.Episode, err)
			}
			if test.name != "multiple tokens" {
				return
			}
			// v34 could not distinguish missing season zero or retain combined membership.
			stale := persisted
			stale.Compatibility.ContractVersion = "prepared-release-v34"
			stale.Episode.SeasonLabel = ""
			stale.Episode.MultipleEpisodes = false
			if err := repo.CommitPreparedRelease(t.Context(), stale); err != nil {
				t.Fatal(err)
			}
			if err := repo.Close(); err != nil {
				t.Fatal(err)
			}
			module, _ = openSpecialPreparation(t, dbPath)
			upgraded, err := module.Prepare(t.Context(), input)
			if err != nil || upgraded.Release.Generation != stale.Generation+1 || upgraded.Release.Compatibility.ContractVersion != ContractVersion ||
				upgraded.Release.Episode.SeasonLabel != "S00" || !upgraded.Release.Episode.MultipleEpisodes {
				t.Fatalf("v34 special facts reused: generation=%d episode=%+v error=%v", upgraded.Release.Generation, upgraded.Release.Episode, err)
			}
			assertSpecialEpisodeProjection(t, module, upgraded.Release)
		})
	}
}

func TestPreparedJoinedSourceMembershipSurvivesRestart(t *testing.T) {
	t.Parallel()
	for _, token := range []string{
		"S00E01S01E02", "S00E01S00E02", "S00E00S01E01", "S00E01v2S01E02v3",
		"S00E01-E02S01E03", "S00E01+02S01E03", "S00E01&02S01E03",
		"S00E01S001E1000", "S00E01S999999999999999999999999E10000v2",
		"_S00E01_E02_", "_S00E01S01E02_",
	} {
		for _, selectedFile := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_selected_file_%t", token, selectedFile), func(t *testing.T) {
				t.Parallel()
				source := filepath.Join(t.TempDir(), "Example.Series."+token+".1080p.WEB-DL-GRP.mkv")
				video := source
				if selectedFile {
					source = filepath.Join(t.TempDir(), "Example.Series.S00E01.1080p.WEB-DL-GRP")
					if err := os.Mkdir(source, 0o700); err != nil {
						t.Fatal(err)
					}
					video = filepath.Join(source, "Example.Series."+token+".mkv")
				}
				if err := os.WriteFile(video, []byte("synthetic media"), 0o600); err != nil {
					t.Fatal(err)
				}
				dbPath := filepath.Join(t.TempDir(), "prepared.sqlite")
				module, repo := openSpecialPreparation(t, dbPath)
				input := api.PrepareInput{SourcePath: source, Instructions: api.ReleaseFactInstructions{
					Category: new(api.CanonicalCategoryTV),
					Identity: api.ExternalIDOverrides{
						TMDBID:   new(0),
						IMDBID:   new(0),
						TVDBID:   new(0),
						TVmazeID: new(0),
						MALID:    new(0),
					},
				}}
				initial, err := module.Prepare(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				if !initial.Release.Episode.MultipleEpisodes {
					t.Fatalf("source membership lost: %+v", initial.Release.Episode)
				}
				assertSpecialEpisodeProjection(t, module, initial.Release)
				input.Instructions.ReleaseName = api.ReleaseNameOverrides{Season: new("S00"), Episode: new("E01")}
				corrected, err := module.Prepare(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, _ = openSpecialPreparation(t, dbPath)
				reused, err := module.Prepare(t.Context(), input)
				if err != nil || reused.Release.Generation != corrected.Release.Generation ||
					!reflect.DeepEqual(reused.Release.Episode, corrected.Release.Episode) || !reused.Release.Episode.MultipleEpisodes {
					t.Fatalf("correction/restart lost membership: facts=%+v err=%v", reused.Release.Episode, err)
				}
				assertSpecialEpisodeProjection(t, module, reused.Release)
			})
		}
	}
}

func TestPreparedZeroEpisodeCanBeCorrected(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"file", "repeated zero", "unsupported width", "unsupported season", "directory"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			directory := kind == "directory"
			token := "S00E00"
			switch kind {
			case "repeated zero":
				token = "S00E00,E00"
			case "unsupported width":
				token = "S00E1000"
			case "unsupported season":
				token = "S001E01"
			}
			source := filepath.Join(t.TempDir(), "Example.Series."+token+".1080p.WEB-DL-GRP.mkv")
			if directory {
				source = strings.TrimSuffix(source, ".mkv")
				if err := os.Mkdir(source, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			video := source
			if directory {
				video = filepath.Join(source, "Example.Series.S00E01.mkv")
			}
			if err := os.WriteFile(video, []byte("synthetic media"), 0o600); err != nil {
				t.Fatal(err)
			}
			module, _ := openSpecialPreparation(t, filepath.Join(t.TempDir(), "metadata.sqlite"))
			result, err := module.Prepare(t.Context(), api.PrepareInput{
				SourcePath: source,
				Instructions: api.ReleaseFactInstructions{
					Category:    new(api.CanonicalCategoryTV),
					ReleaseName: api.ReleaseNameOverrides{Season: new("S00"), Episode: new("E01")},
					Identity: api.ExternalIDOverrides{
						TMDBID:   new(0),
						IMDBID:   new(0),
						TVDBID:   new(0),
						TVmazeID: new(0),
						MALID:    new(0),
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Release.Episode.Episode != 1 || result.Release.Episode.MultipleEpisodes || result.Release.Episode.Pack {
				t.Fatalf("single zero-source correction changed membership: %+v", result.Release.Episode)
			}
			assertSpecialEpisodeProjection(t, module, result.Release)
		})
	}
}

func assertSpecialEpisodeProjection(t *testing.T, module *Module, release api.PreparedRelease) {
	t.Helper()
	encoded, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	var decoded api.PreparedRelease
	if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded.Episode, release.Episode) {
		t.Fatalf("JSON round trip lost episode evidence: %+v error=%v", decoded.Episode, err)
	}
	upload, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{
		SourcePath: release.Source.SourcePath, Generation: release.Generation,
	}})
	if err != nil {
		t.Fatal(err)
	}
	validation := api.NewTrackerValidationSubject(upload, "AITHER")
	facts := release.Episode
	if upload.SeasonInt != facts.Season || upload.SeasonStr != facts.SeasonLabel || upload.EpisodeInt != facts.Episode ||
		upload.EpisodeStr != facts.EpisodeLabel || upload.MultipleEpisodes != facts.MultipleEpisodes || upload.TVPack != facts.Pack {
		t.Fatalf("upload projection lost episode evidence: season=%d label=%q episode=%d multiple=%t pack=%t",
			upload.SeasonInt, upload.SeasonStr, upload.EpisodeInt, upload.MultipleEpisodes, upload.TVPack)
	}
	if validation.SeasonInt != facts.Season || validation.SeasonStr != facts.SeasonLabel || validation.EpisodeInt != facts.Episode ||
		validation.EpisodeStr != facts.EpisodeLabel || validation.MultipleEpisodes != facts.MultipleEpisodes || validation.TVPack != facts.Pack {
		t.Fatalf("validation projection lost episode evidence: season=%d label=%q episode=%d multiple=%t pack=%t",
			validation.SeasonInt, validation.SeasonStr, validation.EpisodeInt, validation.MultipleEpisodes, validation.TVPack)
	}
	failures, err := unit3d.NewWithProfile(aither.Profile()).ValidationPolicy().Check(t.Context(), validation, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	blocked := slices.ContainsFunc(failures, func(failure api.RuleFailure) bool { return failure.Rule == "canonical_tv_metadata" })
	wantAllowed := facts.Season > 0 || facts.SeasonLabel == "S00" && facts.Episode > 0 && !facts.Pack && !facts.MultipleEpisodes
	if blocked == wantAllowed {
		t.Fatalf("AITHER canonical TV gate disagrees with prepared evidence: episode=%+v failures=%+v", facts, failures)
	}
}

// openSpecialPreparation uses the production collector and a real database;
// only external media inspection and scene/IMDb services are stubbed.
func openSpecialPreparation(t *testing.T, dbPath string) (*Module, *db.SQLiteRepository) {
	t.Helper()
	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	if err := repo.MigrateContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	service := metadata.NewService(repo,
		metadata.WithConfig(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}}),
		metadata.WithMediaInfoExporter(sceneGroupMediaInfo{}),
		metadata.WithSceneDetector(&preparedSceneDetector{}),
		metadata.WithIMDBClient(sceneGroupIMDB{}),
	)
	collector, err := NewEvidenceCollector(service)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := externalidentity.NewWithCandidateSource(repo, collector)
	if err != nil {
		t.Fatal(err)
	}
	module, err := New(repo, identity, collector)
	if err != nil {
		t.Fatal(err)
	}
	return module, repo
}
