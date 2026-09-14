// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package btn

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBTNStructuredNamePolicyProjectsGeneratedFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		request api.ReleaseNameRequest
		adjust  func(*api.UploadSubject)
		want    string
	}{
		{
			name: "daily date and mapped codec source",
			request: api.ReleaseNameRequest{
				Category:    "TV",
				Type:        "WEBDL",
				Title:       "Example Daily",
				Year:        2026,
				Season:      "S01",
				Episode:     "E02",
				DailyDate:   "2026-08-08",
				ManualDate:  true,
				Resolution:  "1080p",
				Source:      "Web",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			adjust: func(subject *api.UploadSubject) {
				subject.DailyEpisodeDate, subject.Type, subject.Source, subject.VideoEncode = "2026-08-08", "WEBDL", "Web", "x264"
			},
			want: "Example.Daily.2026.08.08.1080p.WEB-DL.H.264-GRP",
		},
		{
			name: "broadcast year and SD omitted",
			request: api.ReleaseNameRequest{
				Category:    "TV",
				Type:        "WEBDL",
				Title:       "Example Show",
				Year:        2026,
				Season:      "S01",
				Episode:     "E01",
				Resolution:  "SD",
				Source:      "Web",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			adjust: func(subject *api.UploadSubject) {
				subject.Type, subject.Source, subject.VideoEncode = "WEBDL", "Web", "x264"
			},
			want: "Example.Show.S01E01.WEB-DL.H.264-GRP",
		},
		{
			name: "series title year and 480p retained",
			request: api.ReleaseNameRequest{
				Category:    "TV",
				Type:        "WEBDL",
				Title:       "Example Show 2026",
				Year:        2026,
				Season:      "S01",
				Episode:     "E01",
				Resolution:  "480p",
				Source:      "Web",
				VideoEncode: "x264",
				Tag:         "-GRP",
			},
			adjust: func(subject *api.UploadSubject) {
				subject.Type, subject.Source, subject.VideoEncode = "WEBDL", "Web", "x264"
			},
			want: "Example.Show.2026.S01E01.480p.WEB-DL.H.264-GRP",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			subject := btnGeneratedSubject(t, test.request)
			test.adjust(&subject)
			if got := btnReviewedName(t, subject, nil); got != test.want {
				t.Fatalf("BTN name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBTNStructuredNamePolicyKeepsExactAuthority(t *testing.T) {
	t.Parallel()
	scene := api.UploadSubject{Scene: true, SceneName: "Example.Show.S01E01.1080p.WEB-DL.x264-GRp"}
	if got := btnReviewedName(t, scene, nil); got != scene.SceneName {
		t.Fatalf("scene exact name = %q", got)
	}
	anime := api.UploadSubject{Anime: true, Filename: "[GRP] Example Show - 01 [ABC123].mkv"}
	if got, want := btnReviewedName(t, anime, nil), "[GRP] Example Show - 01 [ABC123]"; got != want {
		t.Fatalf("anime exact name = %q, want %q", got, want)
	}
	subject := btnGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Example Show",
		Season:     "S01",
		Episode:    "E01",
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	})
	override := "Requested.Name-GRP"
	if got := btnReviewedName(t, subject, &override); got != override {
		t.Fatalf("requested name = %q", got)
	}
}

func TestBTNStructuredNamePolicyNormalizesGeneratedComponents(t *testing.T) {
	t.Parallel()
	subject := btnGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Éxample's & Show",
		Season:     "S01",
		Episode:    "E01",
		Resolution: "1080p",
		Source:     "Web",
		Audio:      "DDP 5.1 Atmos",
		Tag:        "-GRP",
	})
	subject.Type, subject.Source = "WEBDL", "Web"
	if got, want := btnReviewedName(t, subject, nil), "Examples.and.Show.S01E01.1080p.WEB-DL.DDPA5.1-GRP"; got != want {
		t.Fatalf("component normalization = %q, want %q", got, want)
	}
}

func TestBTNStructuredNamePolicyUsesSemanticGroupAndSearch(t *testing.T) {
	t.Parallel()
	subject := btnGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Example Show",
		Season:     "S01",
		Resolution: "1080p",
		Source:     "Web",
		Tag:        "-GRP",
	})
	subject.TVPack = true
	subject.FileList = []string{"Example.Show.S01E01.1080p.WEB-DL.H.264-GRP.mkv", "Example.Show.S01E02.1080p.WEB-DL.H.264-OTHER.mkv"}
	if got, want := btnReviewedName(t, subject, nil), "Example.Show.S01.1080p.WEB-DL-BTN"; got != want {
		t.Fatalf("mixed group = %q, want %q", got, want)
	}
	subject.TrackerIDs = map[string]string{"BTN": "123"}
	if got := resolveSearchName(subject); got != "" {
		t.Fatalf("canonical BTN search = %q, want empty", got)
	}
	subject.TrackerIDs = nil
	subject.Release.Title = "Fact title"
	if got, want := resolveSearchName(subject), "Fact title"; got != want {
		t.Fatalf("fact search = %q, want %q", got, want)
	}
	subject.Release.Title = ""
	subject.SourcePath = "current.mkv"
	subject.ProviderMetadata = api.SourceScopedMetadata{SourcePath: "stale.mkv", TVDB: &api.TVDBMetadata{NameEnglish: "Stale Title"}}
	if got := resolveSearchName(subject); got != "" {
		t.Fatalf("stale provider search = %q, want empty", got)
	}
	subject.ProviderMetadata = api.SourceScopedMetadata{SourcePath: "current.mkv", TVDB: &api.TVDBMetadata{NameEnglish: "Current Title"}}
	if got, want := resolveSearchName(subject), "Current Title"; got != want {
		t.Fatalf("current provider search = %q, want %q", got, want)
	}
	subject.EffectiveMetadata = api.EffectiveMetadata{Title: "Manual Title", TitleProvenance: api.FactProvenanceManual}
	if got, want := resolveSearchName(subject), "Manual Title"; got != want {
		t.Fatalf("manual search = %q, want %q", got, want)
	}
}

func TestBTNNamingPolicyVersion(t *testing.T) {
	t.Parallel()
	if got := New().ReleaseNamePolicy().ID; got != "standalone/btn/v4" {
		t.Fatalf("BTN naming policy ID = %q", got)
	}
}

func TestBTNAudioRulesDoNotRewriteOtherRoles(t *testing.T) {
	for _, tc := range []struct{ input, plain, audio string }{
		{"DD+ 5.1 Atmos", "DD.5.1.Atmos", "DDPA5.1"},
		{"DDP 5.1 Atmos", "DDP.5.1.Atmos", "DDPA5.1"},
		{"TrueHD 5.1 Atmos", "TrueHD.5.1.Atmos", "TrueHDA5.1"},
		{"DDP 5.1", "DDP.5.1", "DDP5.1"},
		{"DD 2.0", "DD.2.0", "DD2.0"},
		{"AC3 2.0", "AC3.2.0", "AC32.0"},
		{"DTS 5.1", "DTS.5.1", "DTS5.1"},
		{"AAC 2.0", "AAC.2.0", "AAC2.0"},
		{"FLAC 2.0", "FLAC.2.0", "FLAC2.0"},
		{"TrueHD 5.1", "TrueHD.5.1", "TrueHD5.1"},
		{"PCM 2.0", "PCM.2.0", "PCM2.0"},
		{"LPCM 2.0", "LPCM.2.0", "LPCM2.0"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			subject := btnGeneratedSubject(t, api.ReleaseNameRequest{
				Category:     "TV",
				Type:         "WEBDL",
				Title:        tc.input + " Tales",
				Season:       "S01",
				Episode:      "E01",
				EpisodeTitle: tc.input + " Episode",
				Resolution:   "1080p",
				Source:       "Web",
				Audio:        tc.input,
				Tag:          "-" + tc.input + " Group",
			})
			subject.Type, subject.Source = "WEBDL", "Web"
			want := tc.plain + ".Tales.S01E01." + tc.plain + ".Episode.1080p.WEB-DL." + tc.audio + "-" + tc.plain + ".Group"
			if got := btnReviewedName(t, subject, nil); got != want {
				t.Fatalf("name=%q want=%q", got, want)
			}
		})
	}
}

func TestBTNNormalizesGroupAndOmitsEmptyAutomaticEdition(t *testing.T) {
	subject := btnGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "TV",
		Type:       "WEBDL",
		Title:      "Example Show",
		Season:     "S01",
		Episode:    "E01",
		Resolution: "1080p",
		Source:     "Web",
		Edition:    "!!!",
		Tag:        "-Gröup Name",
	})
	subject.Type, subject.Source = "WEBDL", "Web"
	if got, want := btnReviewedName(t, subject, nil), "Example.Show.S01E01.1080p.WEB-DL-Group.Name"; got != want {
		t.Fatalf("name=%q want=%q", got, want)
	}
	for i := range subject.GeneratedName.Components {
		if subject.GeneratedName.Components[i].Role == api.NameRoleEdition || subject.GeneratedName.Components[i].Role == api.NameRoleGroup {
			subject.GeneratedName.Components[i].Manual = true
		}
	}
	if got, want := btnReviewedName(t, subject, nil), "Example.Show.S01E01.!!!.1080p.WEB-DL-Gröup.Name"; got != want {
		t.Fatalf("manual name=%q want=%q", got, want)
	}
}

func btnGeneratedSubject(t *testing.T, request api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	generated := metadata.BuildReleaseName(request, api.NopLogger{})
	if generated.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
		ReleaseName:      generated.Name,
		ReleaseNameNoTag: generated.NameNoTag,
		GeneratedName:    generated.GeneratedName,
		Tag:              request.Tag,
		Release: api.ReleaseInfo{
			Title:      request.Title,
			Year:       request.Year,
			Resolution: request.Resolution,
		},
	}
}

func btnReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "BTN",
		Meta:                subject,
		RequestedUploadName: requested,
	}, New().ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}
