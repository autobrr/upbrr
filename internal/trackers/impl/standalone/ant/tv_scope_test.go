// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/dupe"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestANTTVBatchMembershipDoesNotProveWholeSeason(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                    string
		episode                 int
		files                   []any
		fileCount               int
		wantCoexists, wantBlock bool
		special, unsorted       bool
	}{
		{
name: "batch excludes later episode",
 episode: 8,
 fileCount: 2,
 wantCoexists: true,
 files: []any{map[string]any{"name": "Example.Series.S01E05.1080p.WEB.H264-OTHER.mkv"}, map[string]any{"name": "Example.Series.S01E06.1080p.WEB.H264-OTHER.mkv"}},
},
		{
name: "batch overlaps episode",
 episode: 5,
 fileCount: 2,
 files: []any{map[string]any{"name": "Example.Series.S01E05.1080p.WEB.H264-OTHER.mkv"}, map[string]any{"name": "Example.Series.S01E06.1080p.WEB.H264-OTHER.mkv"}},
},
		{
name: "gapped batch excludes interior episode",
 episode: 6,
 fileCount: 2,
 wantCoexists: true,
 files: []any{map[string]any{"name": "Example.Series.S01E05.1080p.WEB.H264-OTHER.mkv"}, map[string]any{"name": "Example.Series.S01E07.1080p.WEB.H264-OTHER.mkv"}},
},
		{
name: "incomplete membership stays uncertain",
 episode: 8,
 fileCount: 3,
 files: []any{map[string]any{"name": "Example.Series.S01E05.1080p.WEB.H264-OTHER.mkv"}, map[string]any{"name": "Example.Series.S01E06.1080p.WEB.H264-OTHER.mkv"}},
},
		{name: "unknown batch stays uncertain", episode: 8},
		{
name: "unsorted batch stays uncertain",
 episode: 8,
 unsorted: true,
},
		{
name: "special batch excludes another special",
 episode: 3,
 special: true,
 fileCount: 2,
 wantCoexists: true,
 files: []any{map[string]any{"name": "Example.Series.S00E01.1080p.WEB.H264-OTHER.mkv"}, map[string]any{"name": "Example.Series.S00E02.1080p.WEB.H264-OTHER.mkv"}},
},
		{
name: "full season still blocks exact file",
 episode: 2,
 fileCount: 2,
 wantBlock: true,
 files: []any{map[string]any{"name": "Example.Series.S01E01.1080p.WEB.H264-GRP.mkv"}, map[string]any{"name": "Example.Series.S01E02.1080p.WEB.H264-GRP.mkv"}},
},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := map[string]any{
"category": []any{5000, 100003},
 "season": 1,
 "fileName": "Example.Series.2026.S01.1080p.WEB.H264.torrent",
 "files": test.files,
 "fileCount": test.fileCount,
 "media": "WEB",
 "codec": "H264",
 "resolution": "1080p",
}
			season := 1
			if test.special {
				season = 0
				item["season"] = 0
				item["fileName"] = "Example.Series.2026.S00.1080p.WEB.H264.torrent"
			}
			if test.unsorted {
				delete(item, "season")
			}
			entry := antDupeEntries(map[string]any{"item": []any{item}})[0]
			target := api.TrackerDuplicateTarget{
Category: "tv",
 Season: season,
 Episode: test.episode,
 Type: "WEBDL",
 Source: "WEB",
 Resolution: "1080p",
 VideoCodec: "H264",
}
			if test.wantBlock {
				target.FileNames = []string{"Example.Series.S01E02.1080p.WEB.H264-GRP.mkv"}
			}
			result := dupe.Evaluate(target, []dupe.TrackerCandidate{dupe.NormalizeCandidate(entry, "ANT")}, *Profile().CategoryPolicies[api.CanonicalCategoryTV].DupePolicy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
			coexists := result.Candidates[0].Relation == api.DupeRelationCoexists
			if coexists != test.wantCoexists || result.Blocks != test.wantBlock || !test.wantCoexists && !test.wantBlock && !result.RequiresAction {
				t.Fatalf("wrong membership result: relation=%s blocks=%v action=%v", result.Candidates[0].Relation, result.Blocks, result.RequiresAction)
			}
		})
	}
}

func TestANTExplicitSpecialSeasonSurvivesFilenameFallback(t *testing.T) {
	t.Parallel()
	item := map[string]any{
"category": []any{5000, 100004},
 "season": 0,
 "episode": 1,
 "fileName": "Example.Series.2026.S00E01.1080p.WEB.H264.torrent",
 "files": []any{map[string]any{"name": "Example.Series.S01E01.1080p.WEB.H264-OTHER.mkv"}},
}
	entry := antDupeEntries(map[string]any{"item": []any{item}})[0]
	candidate := dupe.NormalizeCandidate(entry, "ANT")
	result := dupe.Evaluate(api.TrackerDuplicateTarget{
Category: "tv",
 Season: 0,
 Episode: 1,
 Names: []string{"Example.Series.S00E01.1080p.WEB.H264-GRP"},
}, []dupe.TrackerCandidate{candidate}, *Profile().CategoryPolicies[api.CanonicalCategoryTV].DupePolicy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
	if candidate.Season != 0 || result.Candidates[0].Relation == api.DupeRelationCoexists {
		t.Fatalf("explicit special numbering lost: candidate=%+v relation=%s", candidate, result.Candidates[0].Relation)
	}
	delete(item, "season")
	delete(item, "episode")
	entry = antDupeEntries(map[string]any{"item": []any{item}})[0]
	if candidate = dupe.NormalizeCandidate(entry, "ANT"); candidate.Season != 1 || candidate.Episode != 1 {
		t.Fatalf("absent coordinates lost filename fallback: %+v", candidate)
	}
}

func TestANTProposedBatchDoesNotUseWholeSeasonPreference(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	if err := registry.Register(New()); err != nil {
		t.Fatal(err)
	}
	meta := api.UploadSubject{
		Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryTV, TVDBID: 100001},
		ProviderMetadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{
TVDBID: 100001,
 Name: "Example Series",
 Genres: "Drama",
}},
		ReleaseName:      "Example.Series.S01.1080p.WEB.H264-GRP",
 SeasonInt: 1,
 TVPack: true,
 MultipleEpisodes: true,
		FileList:   []string{"Example.Series.S01E05.1080p.WEB.H264-GRP.mkv", "Example.Series.S01E06.1080p.WEB.H264-GRP.mkv"},
		SourcePath: filepath.Join(t.TempDir(), "Example.Series.S01"),
 MediaInfoTextPath: "prepared-media",
		LanguageFacts: api.LanguageFacts{ProgrammeLanguages: []string{"English"}, ProgrammeStatus: api.MetadataEvidenceStatusComplete},
	}
	projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "ANT", Meta: meta}, "input", "catalog", "config")
	if failure != nil {
		t.Fatal(failure)
	}
	policy, _ := registry.LookupDupePolicyForCategory("ANT", api.CanonicalCategoryTV)
	result := dupe.Evaluate(projection.DuplicateTarget, []dupe.TrackerCandidate{{
Category: "tv",
 Season: 1,
 Episode: 5,
}}, policy, dupe.SearchEvidence{Complete: true, WorkScope: dupe.WorkScopeProviderID})
	if result.Candidates[0].Relation == api.DupeRelationCoexists || !result.RequiresAction {
		t.Fatalf("batch incorrectly superseded an overlapping episode: %+v", result)
	}
}
