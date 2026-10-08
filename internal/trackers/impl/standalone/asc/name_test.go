// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReleaseNamePolicyVersion(t *testing.T) {
	t.Parallel()
	if got := Profile().ReleaseNamePolicy.ID; got != "standalone/asc/v2" {
		t.Fatalf("ASC release-name policy = %q", got)
	}
}

func TestResolveDisplayTitleHonorsOmitAlternateTitle(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release: api.ReleaseInfo{Title: "Example Release"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title:         "Example Release",
			OriginalTitle: "Example Original",
		}},
		NamePresentation: api.ReleaseNamePresentation{
			Version:            api.ReleaseNamePresentationVersionV1,
			OmitAlternateTitle: true,
		},
	}
	if got := resolveDisplayTitle(meta); got != "Example Release" {
		t.Fatalf("display title = %q", got)
	}
}

func TestResolveUploadTitleOmitsEmptySeasonEpisodeDelimiter(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Release:  api.ReleaseInfo{Title: "Example Show"},
		NamePresentation: api.ReleaseNamePresentation{
			Version:           api.ReleaseNamePresentationVersionV1,
			OmitSeasonEpisode: true,
		},
	}
	if got := resolveUploadTitle(meta); got != "Example Show" {
		t.Fatalf("upload title = %q", got)
	}
}

func TestResolveDisplayTitlePrefersManualFacts(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release:     api.ReleaseInfo{Title: "Parsed Title"},
		ReleaseName: "Fallback Name",
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
			Title:         "Provider Title",
			OriginalTitle: "Provider Original",
			Localized:     map[string]api.TMDBLocalizedData{"pt-BR": {Title: "Localized Title"}},
		}},
		EffectiveMetadata: api.EffectiveMetadata{
			Title:                   "Manual Title",
			TitleProvenance:         api.FactProvenanceManual,
			OriginalTitle:           "Manual Original",
			OriginalTitleProvenance: api.FactProvenanceManual,
		},
	}
	if got := resolveDisplayTitle(meta); got != "Manual Title (Manual Original)" {
		t.Fatalf("display title = %q", got)
	}

	meta.EffectiveMetadata = api.EffectiveMetadata{
		TitleProvenance: api.FactProvenanceManualEmpty, OriginalTitleProvenance: api.FactProvenanceManualEmpty,
	}
	if got := resolveDisplayTitle(meta); got != "" {
		t.Fatalf("manual-empty display title = %q", got)
	}
}

func TestReleaseNamePolicyPreservesDailyEpisodeIdentity(t *testing.T) {
	t.Parallel()

	input, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker: "ASC",
		Meta: api.UploadSubject{
			Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
			Release: api.ReleaseInfo{
				Title:   "Example Show",
				Season:  2,
				Episode: 3,
			},
			SeasonInt:        2,
			EpisodeInt:       3,
			SeasonStr:        "S02",
			EpisodeStr:       "E03",
			DailyEpisodeDate: "2026-02-03",
			NamePresentation: api.ReleaseNamePresentation{
				Version:      api.ReleaseNamePresentationVersionV1,
				UseDailyDate: true,
			},
		},
	}, Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatalf("resolve ASC daily name: %v", failure)
	}
	got, err := input.ReviewedUploadName()
	if err != nil {
		t.Fatalf("review ASC daily name: %v", err)
	}
	if got != "Example Show - 2026-02-03" {
		t.Fatalf("daily upload title = %q", got)
	}
}

func TestResolveDisplayTitlePreservesAutomaticAlternatesAndManualOriginalTitle(t *testing.T) {
	t.Parallel()

	movie := api.UploadSubject{Release: api.ReleaseInfo{Title: "Release"}, ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Title: "TMDB", OriginalTitle: ""}}}
	if got := resolveDisplayTitle(movie); got != "TMDB" {
		t.Fatalf("blank movie alternate display = %q", got)
	}
	tv := movie
	tv.Identity = api.ExternalIdentity{Category: api.CanonicalCategoryTV}
	tv.ProviderMetadata.TMDB.Title = ""
	if got := resolveDisplayTitle(tv); got != "Release" {
		t.Fatalf("TV alternate release fallback display = %q", got)
	}
	manual := api.UploadSubject{Release: api.ReleaseInfo{Title: "Release"}, EffectiveMetadata: api.EffectiveMetadata{OriginalTitle: "Manual Original", OriginalTitleProvenance: api.FactProvenanceManual}}
	if got := resolveDisplayTitle(manual); got != "Release (Manual Original)" {
		t.Fatalf("manual original without TMDB display = %q", got)
	}
	manual.EffectiveMetadata.OriginalTitle = ""
	manual.EffectiveMetadata.OriginalTitleProvenance = api.FactProvenanceManualEmpty
	if got := resolveDisplayTitle(manual); got != "Release" {
		t.Fatalf("manual-empty original display = %q", got)
	}
}

func TestComplianceFileName(t *testing.T) {
	t.Parallel()

	ddp := api.UploadSubject{Audio: "DD+ 5.1", Channels: "5.1"}
	tests := []struct {
		name string
		meta api.UploadSubject
		in   string
		want string
	}{
		{
			name: "inserts missing audio before the video codec",
			meta: ddp,
			in:   "Example.Show.S01E05.NORDiC.1080p.DSNP.WEB-DL.H.264-GRP.mkv",
			want: "Example.Show.S01E05.NORDiC.1080p.DSNP.WEB-DL.DDP5.1.H.264-GRP.mkv",
		},
		{
			name: "single token codec",
			meta: api.UploadSubject{Audio: "AAC 2.0", Channels: "2.0"},
			in:   "Example.Show.S01E05.1080p.WEB-DL.x264-GRP.mkv",
			want: "Example.Show.S01E05.1080p.WEB-DL.AAC2.0.x264-GRP.mkv",
		},
		{
			name: "dts-hd ma",
			meta: api.UploadSubject{Audio: "DTS-HD MA 5.1", Channels: "5.1"},
			in:   "Example.Movie.2020.1080p.BluRay.x264-GRP.mkv",
			want: "Example.Movie.2020.1080p.BluRay.DTS-HD.MA.5.1.x264-GRP.mkv",
		},
		{
			name: "atmos carriers",
			meta: api.UploadSubject{Audio: "TrueHD 7.1 Atmos", Channels: "7.1"},
			in:   "Example.Movie.2020.2160p.BluRay.HEVC-GRP.mkv",
			want: "Example.Movie.2020.2160p.BluRay.TrueHD.Atmos.7.1.HEVC-GRP.mkv",
		},
		{
			name: "folder name without extension",
			meta: ddp,
			in:   "Example.Show.S01.1080p.WEB-DL.H.264-GRP",
			want: "Example.Show.S01.1080p.WEB-DL.DDP5.1.H.264-GRP",
		},
		{
			name: "already has audio",
			meta: ddp,
			in:   "Example.Show.S01E05.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv",
			want: "Example.Show.S01E05.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv",
		},
		{
			name: "remux with audio after the codec",
			meta: api.UploadSubject{Audio: "DTS-HD MA 5.1", Channels: "5.1"},
			in:   "Example.Movie.2020.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP.mkv",
			want: "Example.Movie.2020.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP.mkv",
		},
		{
			name: "title containing an audio-like word is not mistaken for audio",
			meta: ddp,
			in:   "Flac.Attack.2020.1080p.WEB-DL.H.264-GRP.mkv",
			want: "Flac.Attack.2020.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv",
		},
		{
			name: "no video codec token",
			meta: ddp,
			in:   "Example.Movie.2020.1080p.WEB-DL-GRP.mkv",
			want: "Example.Movie.2020.1080p.WEB-DL-GRP.mkv",
		},
		{
			name: "no resolution token",
			meta: ddp,
			in:   "Example.Movie.2020.WEB-DL.H.264-GRP.mkv",
			want: "Example.Movie.2020.WEB-DL.H.264-GRP.mkv",
		},
		{
			name: "unknown audio facts",
			meta: api.UploadSubject{},
			in:   "Example.Movie.2020.1080p.WEB-DL.H.264-GRP.mkv",
			want: "Example.Movie.2020.1080p.WEB-DL.H.264-GRP.mkv",
		},
		{
			name: "channels taken from the audio string when absent",
			meta: api.UploadSubject{Audio: "Dual-Audio DD 5.1"},
			in:   "Example.Movie.2020.1080p.WEB-DL.H.264-GRP.mkv",
			want: "Example.Movie.2020.1080p.WEB-DL.DD5.1.H.264-GRP.mkv",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := complianceFileName(tt.meta, tt.in); got != tt.want {
				t.Fatalf("complianceFileName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAudioFileNameTokenIsStableForDDPAtmos(t *testing.T) {
	t.Parallel()
	if got := audioFileNameToken(api.UploadSubject{Audio: "DD+ 5.1 Atmos", Channels: "5.1"}); got != "DDP5.1.Atmos" {
		t.Fatalf("token = %q", got)
	}
	if got := audioFileNameToken(api.UploadSubject{Audio: "DD+ Unknown", Channels: "Unknown"}); got != "" {
		t.Fatalf("token with unknown channels = %q", got)
	}
}

func TestComplianceFileNameRecognisesExistingAudioSpellings(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Audio: "DD+ 5.1", Channels: "5.1"}
	for _, name := range []string{
		"M.2020.1080p.WEB-DL.DDP51.H.264-GRP.mkv",
		"M.2020.1080p.WEB-DL.DDPA5.1.H.264-GRP.mkv",
		"M.2020.1080p.WEB-DL.DD5.1.H.264-GRP.mkv",
		"M.2020.1080p.WEB-DL.AC-3.H.264-GRP.mkv",
		"M.2020.1080p.WEB-DL.E-AC-3.H.264-GRP.mkv",
		"M.2020.1080p.BluRay.TrueHD.Atmos.7.1.H.264-GRP.mkv",
		"M.2020.1080p.BluRay.DTS-HD.MA.5.1.H.264-GRP.mkv",
		"M.2020.1080p.WEB-DL.AAC2.0.H.264-GRP.mkv",
	} {
		if got := complianceFileName(meta, name); got != name {
			t.Errorf("complianceFileName(%q) added a second audio token: %q", name, got)
		}
	}
}

func TestComplianceFileNameWithReason(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Audio: "DD+ 5.1", Channels: "5.1"}
	tests := []struct {
		name string
		meta api.UploadSubject
		in   string
		want complianceSkip
		fix  bool
	}{
		{
name: "applied",
 meta: meta,
 in: "M.2020.1080p.WEB-DL.H.264-GRP.mkv",
 want: complianceApplied,
},
		{
name: "already present",
 meta: meta,
 in: "M.2020.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv",
 want: complianceHasAudio,
},
		{
name: "no audio facts",
 meta: api.UploadSubject{},
 in: "M.2020.1080p.WEB-DL.H.264-GRP.mkv",
 want: complianceNoAudioFacts,
 fix: true,
},
		{
name: "no resolution",
 meta: meta,
 in: "M.2020.WEB-DL.H.264-GRP.mkv",
 want: complianceNoResolution,
 fix: true,
},
		{
name: "no codec",
 meta: meta,
 in: "M.2020.1080p.WEB-DL-GRP.mkv",
 want: complianceNoVideoCodec,
 fix: true,
},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, got := complianceFileNameWithReason(tt.meta, tt.in)
			if got != tt.want || got.unresolved() != (tt.fix) {
				t.Fatalf("reason = %q unresolved=%t, want %q unresolved=%t", got, got.unresolved(), tt.want, tt.fix)
			}
		})
	}
}

func TestAudioFileNameTokenVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		audio    string
		channels string
		want     string
	}{
		{"DTS-HD MA 7.1", "7.1", "DTS-HD.MA.7.1"},
		{"DTS-HD 5.1", "5.1", "DTS-HD.5.1"},
		{"DTS:X 7.1", "7.1", "DTS-X.7.1"},
		{"DTS 5.1", "5.1", "DTS5.1"},
		{"TrueHD 7.1", "7.1", "TrueHD.7.1"},
		{"TrueHD 7.1 Atmos", "7.1", "TrueHD.Atmos.7.1"},
		{"DD 5.1", "5.1", "DD5.1"},
		{"FLAC 2.0", "2.0", "FLAC2.0"},
		{"LPCM 2.0", "2.0", "LPCM.2.0"},
		{"PCM 2.0", "2.0", "PCM.2.0"},
		{"Opus 2.0", "2.0", "OPUS2.0"},
		{"MP3 2.0", "2.0", "", },
		{"Vorbis 2.0", "2.0", ""},
		{"AAC 2.0", "6 channels", "AAC2.0"},
		{"AAC", "", ""},
	}
	for _, tt := range tests {
		if got := audioFileNameToken(api.UploadSubject{Audio: tt.audio, Channels: tt.channels}); got != tt.want {
			t.Errorf("audioFileNameToken(%q, %q) = %q, want %q", tt.audio, tt.channels, got, tt.want)
		}
	}
}

func TestComplianceFileNameIgnoresAudioLookingReleaseGroup(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{Audio: "DD+ 5.1", Channels: "5.1"}
	for group, want := range map[string]string{
		"DTS":  "M.2020.1080p.WEB-DL.DDP5.1.H.264-DTS.mkv",
		"AAC":  "M.2020.1080p.WEB-DL.DDP5.1.H.264-AAC.mkv",
		"FLAC": "M.2020.1080p.WEB-DL.DDP5.1.H.264-FLAC.mkv",
	} {
		in := "M.2020.1080p.WEB-DL.H.264-" + group + ".mkv"
		if got := complianceFileName(meta, in); got != want {
			t.Errorf("complianceFileName(%q) = %q, want %q", in, got, want)
		}
	}
	// A real audio token before the group is still recognised.
	has := "M.2020.1080p.BluRay.REMUX.AVC.DTS-HD.MA.5.1-GRP.mkv"
	if got := complianceFileName(api.UploadSubject{Audio: "DTS-HD MA 5.1", Channels: "5.1"}, has); got != has {
		t.Errorf("complianceFileName(%q) = %q", has, got)
	}
}

func TestResolveSearchTitlePrefersFinalizedTitle(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		ReleaseName:       "Example Show AKA Ekusanpuru S03 1080p Dual-Audio AAC 2.0 AVC",
		SourcePath:        "/data/Example Show/Season 03",
		EffectiveMetadata: api.EffectiveMetadata{Title: "Example Show"},
	}
	if got := resolveSearchTitle(meta); got != "Example Show" {
		t.Fatalf("search title = %q", got)
	}
	meta.EffectiveMetadata = api.EffectiveMetadata{}
	if got := resolveSearchTitle(meta); got != meta.ReleaseName {
		t.Fatalf("search title without a finalized title = %q", got)
	}
	meta.ReleaseName = ""
	if got := resolveSearchTitle(meta); got != "" {
		t.Fatalf("source path must never become a search title, got %q", got)
	}
}

func TestRenameContentNamesGenericPackFolderAfterTheRelease(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Audio:       "Dual-Audio AAC 2.0",
		Channels:    "2.0",
		ReleaseName: "Example Show AKA Ekusanpuru S03 1080p Dual-Audio AAC 2.0 AVC",
	}
	tests := []struct {
		name string
		in   string
		kind trackers.ContentNameKind
		want string
	}{
		{
name: "generic pack folder",
 in: "Season 03",
 kind: trackers.ContentRootFolderName,
 want: "Example.Show.AKA.Ekusanpuru.S03.1080p.Dual-Audio.AAC.2.0.AVC",
},
		{
name: "show-name folder without release info",
 in: "Example Show",
 kind: trackers.ContentRootFolderName,
 want: "Example.Show.AKA.Ekusanpuru.S03.1080p.Dual-Audio.AAC.2.0.AVC",
},
		{
name: "release-like folder is kept",
 in: "Example.Show.S03.1080p.WEB-DL.AAC2.0.H.264-GRP",
 kind: trackers.ContentRootFolderName,
 want: "Example.Show.S03.1080p.WEB-DL.AAC2.0.H.264-GRP",
},
		{
name: "files keep the file rule",
 in: "Season 03",
 kind: trackers.ContentFileName,
 want: "Season 03",
},
		{
name: "subfolders keep the file rule",
 in: "Season 03",
 kind: trackers.ContentSubfolderName,
 want: "Season 03",
},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := renameContent(meta, tt.in, tt.kind)
			if got != tt.want {
				t.Fatalf("renameContent(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if again := renameContent(meta, got, tt.kind); again != got {
				t.Fatalf("renameContent is not idempotent: %q then %q", got, again)
			}
		})
	}

	// Without a release name a generic folder keeps its name.
	if got := renameContent(api.UploadSubject{}, "Season 03", trackers.ContentRootFolderName); got != "Season 03" {
		t.Fatalf("without a release name = %q", got)
	}
}

func TestReleaseFolderNameDropsUnsafeCharacters(t *testing.T) {
	t.Parallel()

	if got := releaseFolderName(` Example: Show / Part 2? "Cut" <1080p> | AAC 2.0 `); got != "Example.Show.Part.2.Cut.1080p.AAC.2.0" {
		t.Fatalf("releaseFolderName = %q", got)
	}
	if got := releaseFolderName("   "); got != "" {
		t.Fatalf("blank release name = %q", got)
	}
}
