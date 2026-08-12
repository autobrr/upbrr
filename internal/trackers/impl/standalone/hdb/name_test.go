// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package hdb

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDBReleaseNamePolicyBuildsStructuredOriginalIMDbName(t *testing.T) {
	t.Parallel()
	const (
		sourcePath = "Example.Release.2026.2160p.WEB-DL.H.265-GRP"
		generated  = "Example.Release.2026.Limited.2160p.WEB-DL.Dual-Audio.DDP.5.1.H.265-GRP"
	)
	meta := api.UploadSubject{
		SourcePath:  sourcePath,
		ReleaseName: generated,
		GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
			IncludeEpisodeTitle: api.ReleaseNameVariant{Name: generated},
			OmitEpisodeTitle:    api.ReleaseNameVariant{Name: generated},
		},
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 4,
			Category:   api.CanonicalCategoryMovie,
			IMDBID:     1234567,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 4,
			IMDB: &api.IMDBMetadata{
				IMDBID: 1234567,
				Title:  "Example Release",
				AKA:    "Example Original",
				Year:   2026,
			},
		},
		Release:     api.ReleaseInfo{Resolution: "2160p"},
		Edition:     "Director's Cut",
		Repack:      "REPACK",
		Source:      "WEB-DL",
		VideoEncode: "H.265",
		Audio:       "Dual-Audio DDP",
		Channels:    "5.1",
		Tag:         "-GRP",
	}

	input, failure := trackers.PrepareInputWithReleaseNamePolicy(
		trackers.PreparationInput{Tracker: "HDB", Meta: meta},
		Profile().ReleaseNamePolicy,
	)
	if failure != nil {
		t.Fatalf("resolve HDB name: %v", failure)
	}
	got, err := input.ReviewedUploadName()
	if err != nil {
		t.Fatalf("reviewed HDB name: %v", err)
	}
	const want = "Example Original 2026 2160p WEB-DL DDP 5.1 H.265-GRP"
	if got != want {
		t.Fatalf("HDB name = %q, want %q", got, want)
	}
	if strings.Contains(got, "Director's Cut") || strings.Contains(got, "REPACK") || strings.Contains(got, "Dual-Audio") || strings.Contains(got, "Example.Original") {
		t.Fatalf("HDB name retained prohibited/separator elements: %q", got)
	}
}

func TestBuildGeneratedHDBNameUsesMediumSpecificTechnicalOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		meta api.UploadSubject
		want string
	}{
		{
			name: "Blu-ray disc uses region source video audio",
			meta: api.UploadSubject{
				DiscType:   "BDMV",
				Source:     "BluRay",
				Region:     "CEE",
				VideoCodec: "AVC",
				Audio:      "Dual Audio DTS-HD MA",
				Channels:   "5.1",
				Release:    api.ReleaseInfo{Year: 2026, Resolution: "1080p"},
				Tag:        "-GRP",
			},
			want: "Example Release 2026 1080p CEE Blu-ray AVC DTS-HD MA 5.1-GRP",
		},
		{
			name: "BluRay encode uses audio video",
			meta: api.UploadSubject{
				Type:        "ENCODE",
				Source:      "Blu-ray",
				VideoEncode: "x264",
				Audio:       "Dubbed Multi Lang DTS",
				Channels:    "5.1",
				Release:     api.ReleaseInfo{Year: 2026, Resolution: "1080p"},
				Tag:         "-GRP",
			},
			want: "Example Release 2026 1080p BluRay DTS x264-GRP",
		},
		{
			name: "remux substitutes the source medium",
			meta: api.UploadSubject{
				Type:       "REMUX",
				Source:     "BluRay",
				Region:     "USA",
				VideoCodec: "AVC",
				Audio:      "TrueHD",
				Channels:   "7.1",
				Release:    api.ReleaseInfo{Year: 2026, Resolution: "2160p"},
				Tag:        "-GRP",
			},
			want: "Example Release 2026 2160p USA Remux AVC TrueHD 7.1-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := buildGeneratedHDBName(test.meta, "Example Release"); got != test.want {
				t.Fatalf("HDB name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHDBReleaseNamePolicyPreservesExactP2PName(t *testing.T) {
	t.Parallel()
	const exact = "Example.Release.2026.1080p.WEB-DL.H.264-GRP"
	resolved, err := Profile().ReleaseNamePolicy.Resolver(trackers.ReleaseNameInput{
		Subject: api.UploadSubject{ReleaseName: exact},
	})
	if err != nil {
		t.Fatalf("resolve exact HDB name: %v", err)
	}
	if resolved.Upload != exact {
		t.Fatalf("exact HDB name = %q, want %q", resolved.Upload, exact)
	}
}

func TestHDBReleaseNamePolicyRejectsStaleIMDbOriginalTitle(t *testing.T) {
	t.Parallel()
	const generated = "Example.Release.2026.1080p.WEB-DL.H.264-GRP"
	_, err := Profile().ReleaseNamePolicy.Resolver(trackers.ReleaseNameInput{
		Subject: api.UploadSubject{
			SourcePath:  "current-source",
			ReleaseName: generated,
			GeneratedReleaseNames: api.GeneratedReleaseNameVariants{
				IncludeEpisodeTitle: api.ReleaseNameVariant{Name: generated},
			},
			Identity: api.ExternalIdentity{
				SourcePath: "current-source",
				Generation: 2,
				IMDBID:     1234567,
			},
			ProviderMetadata: api.SourceScopedMetadata{
				SourcePath: "stale-source",
				Generation: 2,
				IMDB:       &api.IMDBMetadata{IMDBID: 1234567, AKA: "Stale Original"},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "current matching IMDb original title") {
		t.Fatalf("stale IMDb error = %v", err)
	}
}
