// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dupe

import (
	"slices"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestNormalizeTitleProviderSeparatesPresentation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		provider string
		edition  string
	}{
		{
			name:     "Example.Movie.2026.IMAX.2160p.DSNP.WEB-DL.H.265-GRP",
			provider: "dsnp",
			edition:  "imax",
		},
		{
			name:     "Example Movie 2026 IMAX Hybrid REPACK 2160p DSNP WEB-DL DD+ 5.1 Atmos DV HDR H.265-GRP",
			provider: "dsnp",
			edition:  "imax",
		},
		{
			name:     "Example.Movie.2026.IMAX.2160p.DSN+.WEB-DL.H.265-GRP",
			provider: "dsnp",
			edition:  "imax",
		},
		{
			name:     "Example.Movie.2026.IMAX.2160p.HTSR.WEB-DL.H.265-GRP",
			provider: "htsr",
			edition:  "imax",
		},
		{name: "Example.Movie.2026.Hybrid.2160p.DSNP.WEB-DL.H.265-GRP", provider: "dsnp"},
		{
			name:     "Example.Movie.2026.DSNP.IMAX.2160p.WEB-DL.H.265-GRP",
			provider: "dsnp",
			edition:  "imax",
		},
		{name: "Example.Movie.2026.IMAX.2160p.WEB-DL.H.265-GRP", edition: "imax"},
		{name: "Example.Movie.2026.Hybrid.2160p.WEB-DL.H.265-GRP"},
		{name: "Example.Movie.2026.IMAX.Hybrid.2160p.BluRay.REMUX.HEVC-GRP", edition: "imax"},
		{name: "Example.Movie.2026.2160p.WEB-DL.DSNP.H.265-GRP", provider: "dsnp"},
		{name: "The.Amazon.2026.2160p.WEB-DL.H.265-GRP"},
		{name: "Example.Movie.2026.1080p.iP.WEB-DL.H.264-GRP", provider: "iplayer"},
		{name: "Example.Movie.2026.1080p.iPlayer.WEB-DL.H.264-GRP", provider: "iplayer"},
		{name: "Example.Movie.2026.1080p.BRAV.WEB-DL.H.264-GRP", provider: "bravo"},
		{name: "Example.Movie.2026.1080p.BRAVO.WEB-DL.H.264-GRP", provider: "bravo"},
		{name: "Example.Movie.2026.1080p.YT.WEB-DL.H.264-GRP", provider: "youtube"},
		{name: "Example.Movie.2026.1080p.YOUTUBE.WEB-DL.H.264-GRP", provider: "youtube"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{Name: test.name}, "LST"))
			target := normalizeTargetFacts(api.TrackerDuplicateTarget{Names: []string{test.name}})
			for _, facts := range []normalizedFacts{candidate, target} {
				if facts.Provider.Value != test.provider || facts.Edition.Value != test.edition {
					t.Fatalf("provider=%#v edition=%#v; want provider=%q edition=%q", facts.Provider, facts.Edition, test.provider, test.edition)
				}
				if test.provider == "" {
					if facts.Provider.Status != FactMissing {
						t.Fatalf("absent provider = %#v", facts.Provider)
					}
				} else if facts.Provider.Status != FactPartial || !slices.Equal(facts.Provider.SourceFields, []string{"title"}) {
					t.Fatalf("title provider provenance = %#v", facts.Provider)
				}
			}
			if test.provider != "" && (candidate.Provider.Origin != FactOriginTrackerTitle || target.Provider.Origin != FactOriginContentName) {
				t.Fatalf("candidate origin=%s target origin=%s", candidate.Provider.Origin, target.Provider.Origin)
			}
		})
	}
}

func TestNormalizeStructuredProviderWithPresentationTitle(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		provider string
		want     string
		status   FactStatus
	}{
		{
			name:     "Example.Movie.2026.IMAX.Hybrid.2160p.DSNP.WEB-DL.HEVC-GRP",
			provider: "DSNP",
			want:     "dsnp",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.Hybrid.2160p.WEB-DL.HEVC-GRP",
			provider: "PROVIDER_A",
			want:     "providera",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.IMAX.Hybrid.2160p.DSNP.WEB-DL.HEVC-GRP",
			provider: "AMZN",
			want:     "amzn",
			status:   FactContradictory,
		},
		{
			name:     "Example.Movie.2026.1080p.iTunes.WEB-DL.H.264-GRP",
			provider: "iTunes",
			want:     "itunes",
			status:   FactComplete,
		},
		{
			name:     "Example.Movie.2026.1080p.ABC.WEB-DL.H.264-GRP",
			provider: "ABC",
			want:     "abc",
			status:   FactComplete,
		},
	} {
		t.Run(test.provider+"/"+test.name, func(t *testing.T) {
			t.Parallel()
			candidate := normalizeCandidateFacts(NormalizeCandidate(api.DupeEntry{Name: test.name, Provider: test.provider}, "LST"))
			target := normalizeTargetFacts(api.TrackerDuplicateTarget{Names: []string{test.name}, Provider: test.provider})
			for _, facts := range []normalizedFacts{candidate, target} {
				if facts.Provider.Value != test.want || facts.Provider.Status != test.status {
					t.Fatalf("structured provider = %#v; want %s/%s", facts.Provider, test.want, test.status)
				}
				if test.status == FactContradictory && !slices.Equal(facts.Provider.Contradictions, []string{"dsnp"}) {
					t.Fatalf("real provider contradiction = %#v", facts.Provider)
				}
			}
			if candidate.Provider.Origin != FactOriginTrackerAPI || target.Provider.Origin != FactOriginTargetMedia {
				t.Fatalf("candidate origin=%s target origin=%s", candidate.Provider.Origin, target.Provider.Origin)
			}
		})
	}
}
