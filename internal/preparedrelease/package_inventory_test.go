// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/upbrr/internal/filesystem"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/sourcelayout"
	"github.com/autobrr/upbrr/internal/torrent"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/aither"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/sp"
	"github.com/autobrr/upbrr/pkg/api"
)

// Native media inspection is synthetic; inventory uses the production torrent owner.
type packageInventoryPipeline struct{ privateResourcePipelineFake }

func (p packageInventoryPipeline) CollectPreparationEvidence(_ context.Context, request preparationstate.Request) (preparationstate.State, error) {
	state := p.state
	inventory, err := torrent.ResolvePackageInventory(api.TorrentSubject{
		SourcePath: state.SourcePath,
		DiscType:   state.DiscType,
		FileList:   state.FileList,
		KeepFolder: request.Input.Policy.KeepFolder && !state.TVPack,
	})
	state.PackageInventory = inventory
	if err != nil {
		return state, fmt.Errorf("collect synthetic package inventory: %w", err)
	}
	return state, nil
}

func TestPreparedPackageInventoryReachesAither(t *testing.T) {
	for _, tc := range []struct {
		name         string
		files        []string
		direct       bool
		blocked      bool
		badStructure bool
		archive      bool
	}{
		{
			name:   "direct ISO",
			files:  []string{"Example.3D.iso"},
			direct: true,
		},
		{
			name:    "extracted disc",
			files:   []string{filepath.Join("BDMV", "index.bdmv"), filepath.Join("BDMV", "STREAM", "00001.m2ts")},
			blocked: true,
		},
		{
			name:         "incomplete disc structure",
			files:        []string{filepath.Join("BDMV", "STREAM", "00001.m2ts")},
			blocked:      true,
			badStructure: true,
		},
		{
			name:    "included prohibited archive",
			files:   []string{filepath.Join("BDMV", "index.bdmv"), filepath.Join("BDMV", "STREAM", "00001.m2ts"), "included.rar"},
			blocked: true,
			archive: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range tc.files {
				file := filepath.Join(root, name)
				if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("synthetic disc"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			source := root
			if tc.direct {
				source = filepath.Join(root, tc.files[0])
			}
			state := preparationstate.State{
				SourcePath: source,
				Paths:      []string{source},
				DiscType:   "BDMV",
				Type:       "DISC",
				Is3D:       "3D",
			}
			layout, err := sourcelayout.Resolve(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			for _, disc := range layout.Discs {
				state.Discs = append(state.Discs, preparationstate.DiscResource{
					ID:        disc.ID,
					Name:      disc.Name,
					Type:      disc.Type,
					Root:      disc.Root,
					BluRayISO: disc.Image,
				})
			}
			collector, err := NewEvidenceCollector(packageInventoryPipeline{privateResourcePipelineFake{state: state}})
			if err != nil {
				t.Fatal(err)
			}
			store := newMemoryStore()
			for range 2 {
				module := newTestModule(t, store, collector)
				result, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: source})
				if err != nil {
					t.Fatal(err)
				}
				upload, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{SourcePath: source, Generation: result.Release.Generation}})
				if err != nil {
					t.Fatal(err)
				}
				projected := api.NewTrackerValidationSubject(upload, "AITHER")
				if projected.PackageFacts.Status != api.MetadataEvidenceStatusComplete || projected.PackageFacts.KnownFileCount != len(tc.files) {
					t.Fatalf("inventory = %+v", projected.PackageFacts)
				}
				failures, err := aither.ValidationPolicy().Check(t.Context(), projected, api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				blocked := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "aither_3d_iso" })
				if blocked != tc.blocked {
					t.Fatalf("ISO blocked = %t, want %t: %+v", blocked, tc.blocked, failures)
				}
				if found := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "aither_archive" }); found != tc.archive {
					t.Fatalf("archive outcome = %t, want %t", found, tc.archive)
				}
				failures, err = sp.ValidationPolicy().Check(t.Context(), projected, api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				if found := slices.ContainsFunc(failures, func(f api.RuleFailure) bool { return f.Rule == "sp_full_disc_structure" }); found != tc.badStructure {
					t.Fatalf("disc structure outcome = %t, want %t: %+v", found, tc.badStructure, failures)
				}
			}
		})
	}
}

func TestPreparedInventoryUsesTorrentSelectionAcrossFamilies(t *testing.T) {
	registry := impl.MustNewRegistry()
	for _, tc := range []struct {
		name       string
		files      []string
		rules      map[string][]string
		keepFolder bool
		pack       bool
		violations bool
	}{
		{
			name:  "complete season",
			pack:  true,
			files: []string{"Example.S01E01.mkv", "Example.S01E02.mkv"},
			rules: map[string][]string{
				"AITHER": {"aither_archive", "aither_media_only", "aither_multi_season"},
				"AZ":     {"azfamily_archive", "azfamily_media_only", "azfamily_single_file_folder"},
				"BTN":    {"btn_package_safety", "btn_episode_range", "btn_multi_season_package"},
				"SP":     {"sp_package_safety", "sp_pack_completeness"},
				"OTW":    {"otw_season_structure"},
			},
		},
		{
			name:  "episode gap",
			pack:  true,
			files: []string{"Example.S01E01.mkv", "Example.S01E03.mkv"},
			rules: map[string][]string{
				"BTN": {"btn_episode_range"}, "SP": {"sp_pack_completeness"},
			},
			violations: true,
		},
		{
			name:       "multiple movie files",
			files:      []string{"Example.2026.mkv", "Second.2026.mkv"},
			rules:      map[string][]string{"RF": {"rf_movie_file_count"}},
			violations: true,
		},
		{
			name:  "collapsed single file",
			files: []string{"Example.2026.mkv"},
			rules: map[string][]string{
				"AZ": {"azfamily_single_file_folder"}, "BTN": {"btn_single_file_layout"},
			},
		},
		{
			name:       "single-episode pack ignores folder retention",
			pack:       true,
			keepFolder: true,
			files:      []string{"Example.S01E01.mkv"},
			rules: map[string][]string{
				"BTN": {"btn_single_file_layout", "btn_episode_range"},
			},
		},
		{
			name:       "retained single file folder",
			keepFolder: true,
			files:      []string{"Example.2026.mkv"},
			rules: map[string][]string{
				"AZ": {"azfamily_single_file_folder"}, "BTN": {"btn_single_file_layout"},
			},
			violations: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range append(slices.Clone(tc.files), "sample.mkv", "release.nfo", "excluded.rar") {
				if err := os.WriteFile(filepath.Join(root, name), []byte("synthetic file"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			video, selected, err := filesystem.CollectVideoFiles(t.Context(), root, false)
			if err != nil {
				t.Fatal(err)
			}
			state := preparationstate.State{
				SourcePath:     root,
				Paths:          []string{root},
				VideoPath:      video,
				FileList:       selected,
				TVPack:         tc.pack,
				SeasonInt:      1,
				Container:      "MKV",
				AudioLanguages: []string{"English"},
			}
			collector, err := NewEvidenceCollector(packageInventoryPipeline{privateResourcePipelineFake{state: state}})
			if err != nil {
				t.Fatal(err)
			}
			module := newTestModule(t, newMemoryStore(), collector)
			result, err := module.Prepare(t.Context(), api.PrepareInput{SourcePath: root, Policy: api.PreparationPolicy{KeepFolder: tc.keepFolder}})
			if err != nil {
				t.Fatal(err)
			}
			upload, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{SourcePath: root, Generation: result.Release.Generation}})
			if err != nil {
				t.Fatal(err)
			}
			upload.Identity.Category = api.CanonicalCategoryTV
			for tracker, rules := range tc.rules {
				subject := api.NewTrackerValidationSubject(upload, tracker)
				if subject.PackageFacts.Status != api.MetadataEvidenceStatusComplete || subject.PackageFacts.KnownFileCount != len(tc.files) {
					t.Fatalf("%s inventory = %+v", tracker, subject.PackageFacts)
				}
				roundtrip := api.NewTrackerValidationSubjectFromRuleSubject(trackers.RuleSubjectFromValidation(subject), tracker)
				if roundtrip.PackageFacts.Scope != subject.PackageFacts.Scope || roundtrip.PackageFacts.SingleFileFolder != subject.PackageFacts.SingleFileFolder || roundtrip.PackageFacts.Status != subject.PackageFacts.Status {
					t.Fatal("rule adapter lost package layout authority")
				}
				if subject.MediaFileFacts.LanguageStatus == api.MetadataEvidenceStatusComplete && len(tc.files) > 1 {
					t.Fatal("inventory completeness promoted single-file language evidence")
				}
				descriptor, ok := registry.LookupDescriptor(tracker)
				if !ok {
					t.Fatalf("missing tracker %s", tracker)
				}
				failures, err := descriptor.Validation.Check(t.Context(), subject, api.NopLogger{})
				if err != nil {
					t.Fatal(err)
				}
				for _, rule := range rules {
					var found bool
					for _, failure := range failures {
						if failure.Rule != rule {
							continue
						}
						found = true
						if failure.EvidenceStatus != api.MetadataEvidenceStatusComplete || failure.Disposition == api.RuleDispositionAdvisory {
							t.Fatalf("%s: inventory did not reach rule: %+v", tracker, failure)
						}
					}
					if found != tc.violations {
						t.Fatalf("%s %s present = %t, want %t: %+v", tracker, rule, found, tc.violations, failures)
					}
				}
			}
		})
	}
}
