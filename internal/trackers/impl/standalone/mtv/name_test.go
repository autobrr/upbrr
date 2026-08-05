// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package mtv

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveUploadNameUsesCurrentTVDBTitleAndCollisionYear(t *testing.T) {
	t.Parallel()
	if got := Profile().ReleaseNamePolicy.ID; got != "standalone/mtv/v3" {
		t.Fatalf("MTV release-name policy = %q, want standalone/mtv/v3", got)
	}
	const (
		sourcePath = "Example.Show.S01E02.1080p.WEB-DL.H.264-GRP"
		generated  = "Parsed.Show.S01E02.Example.Episode.1080p.WEB-DL.H.264-GRP"
	)
	meta := api.UploadSubject{
		SourcePath:  sourcePath,
		ReleaseName: generated,
		GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: api.ReleaseNameVariant{Name: generated},
			OmitEpisodeTitle:    api.ReleaseNameVariant{Name: "Parsed.Show.S01E02.1080p.WEB-DL.H.264-GRP"},
		},
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 3,
			Category:   api.CanonicalCategoryTV,
			TVDBID:     7654321,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 3,
			TVDB: &api.TVDBMetadata{
				TVDBID:      7654321,
				NameEnglish: "Example Show",
				NameDisambiguation: api.TVDBNameDisambiguation{
					CanonicalName: "Example Show",
					SeriesYear:    2020,
					Locale:        "US",
					IncludeYear:   true,
					IncludeLocale: true,
					Status:        api.MetadataEvidenceStatusPartial,
					Source:        "tvdb-v4-search/v1",
				},
			},
		},
		Release:      api.ReleaseInfo{Title: "Parsed Show", Resolution: "1080p"},
		SeasonInt:    1,
		EpisodeInt:   2,
		SeasonStr:    "S01",
		EpisodeStr:   "E02",
		EpisodeTitle: "Example Episode",
		Service:      "AMZN",
		Type:         "WEBDL",
		Audio:        "DDP5.1",
		VideoCodec:   "H.264",
		Tag:          "-GRP",
	}

	const want = "Example.Show.2020.S01E02.Example.Episode.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP"
	if got := resolveUploadName(meta); got != want {
		t.Fatalf("MTV name = %q, want %q", got, want)
	} else if strings.Contains(got, ".US.") {
		t.Fatalf("MTV name included locale token: %q", got)
	}
}

func TestResolveUploadNameUsesOnlyProvedGroupComposition(t *testing.T) {
	t.Parallel()
	const generated = "Example.Show.S01.1080p.WEB-DL.H.264"
	base := api.UploadSubject{
		ReleaseName: generated,
		GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: api.ReleaseNameVariant{Name: generated},
		},
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Release: api.ReleaseInfo{
			Title:      "Example Show",
			Resolution: "1080p",
		},
		SeasonInt:  1,
		SeasonStr:  "S01",
		Type:       "WEBDL",
		VideoCodec: "H.264",
		TVPack:     true,
	}
	tests := []struct {
		name  string
		scene bool
		tag   string
		want  string
	}{
		{name: "no group", want: "NOGRP"},
		{
			name:  "proved scene",
			scene: true,
			want:  "SCENE",
		},
		{
			name: "explicit P2P composition",
			tag:  "-P2P",
			want: "P2P",
		},
		{
			name: "known release group",
			tag:  "-GRP",
			want: "GRP",
		},
		{
			name: "unproved scene label",
			tag:  "-SCENE",
			want: "NOGRP",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := base
			meta.Scene = tc.scene
			meta.Tag = tc.tag
			got := resolveUploadName(meta)
			if !strings.HasSuffix(got, "-"+tc.want) {
				t.Fatalf("MTV group name = %q, want suffix -%s", got, tc.want)
			}
		})
	}
}

func TestResolveUploadNamePreservesExactRenamedSceneName(t *testing.T) {
	t.Parallel()
	const exact = "Example Show [SCENE].S01E02-GRP"
	got := resolveUploadName(api.UploadSubject{
		Scene:        true,
		SceneName:    exact,
		SceneRenamed: true,
		ReleaseName:  "Generated.Show.S01E02.Example.Episode.1080p.WEB-DL.H.264-GRP",
	})
	if got != exact {
		t.Fatalf("exact MTV scene name = %q, want %q", got, exact)
	}
}

func TestResolveUploadNameRejectsStaleTVDBTitle(t *testing.T) {
	t.Parallel()
	const generated = "Parsed.Show.S01.1080p.WEB-DL.H.264"
	got := resolveUploadName(api.UploadSubject{
		SourcePath:  "current-source",
		ReleaseName: generated,
		GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: api.ReleaseNameVariant{Name: generated},
		},
		Identity: api.ExternalIdentity{
			SourcePath: "current-source",
			Generation: 5,
			Category:   api.CanonicalCategoryTV,
			TVDBID:     7654321,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "stale-source",
			Generation: 5,
			TVDB:       &api.TVDBMetadata{TVDBID: 7654321, NameEnglish: "Stale Show"},
		},
		Release:    api.ReleaseInfo{Title: "Parsed Show", Resolution: "1080p"},
		SeasonInt:  1,
		SeasonStr:  "S01",
		Type:       "WEBDL",
		VideoCodec: "H.264",
	})
	if strings.HasPrefix(got, "Stale.Show.") || !strings.HasPrefix(got, "Parsed.Show.") {
		t.Fatalf("stale MTV TVDB title changed name: %q", got)
	}
}
