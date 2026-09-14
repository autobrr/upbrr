// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package dvl

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDVLStructuredReleaseNamePolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		request   api.ReleaseNameRequest
		languages []string
		want      string
	}{
		{
			name: "DVD rip moves resolution format audio and encode without touching title tokens",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "DVDRIP",
				Title:       "DD 2.0 x264 DVDRip Tales",
				Year:        2001,
				Resolution:  "480p",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
				Tag:         "-GRP",
			},
			want: "DD 2.0 x264 DVDRip Tales 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVD rip falls back to video codec",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DVDRIP",
				Title:      "Example Movie",
				Year:       2001,
				Resolution: "480p",
				Source:     "PAL DVD",
				VideoCodec: "XviD",
				Audio:      "DD 2.0",
				Tag:        "-GRP",
			},
			want: "Example Movie 2001 480p DVDRip DD 2.0 XviD-GRP",
		},
		{
			name: "DVD rip keeps a dubbed audio prefix with its audio",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "DVDRIP",
				Title:       "Example Movie",
				Year:        2001,
				Resolution:  "480p",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "Dubbed DD 2.0",
				Tag:         "-GRP",
			},
			want: "Example Movie 2001 480p DVDRip Dubbed DD 2.0 x264-GRP",
		},
		{
			name: "TV DVD rip keeps a dual audio suffix with its audio",
			request: api.ReleaseNameRequest{
				Category:    "TV",
				Type:        "DVDRIP",
				Title:       "Example Show",
				Season:      "S01",
				Resolution:  "576p",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0 Dual-Audio",
				Tag:         "-GRP",
			},
			want: "Example Show S01 576p DVDRip DD 2.0 Dual-Audio x264-GRP",
		},
		{
			name: "DVD rip keeps a marker only audio cluster before the codec",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "DVDRIP",
				Title:       "Example Movie",
				Year:        2001,
				Resolution:  "480p",
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "Dubbed",
				Tag:         "-GRP",
			},
			want: "Example Movie 2001 480p DVDRip Dubbed x264-GRP",
		},
		{
			name: "DVD rip without resolution evidence uses its format anchor",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "DVDRIP",
				Title:       "Example Movie",
				Year:        2001,
				Source:      "PAL DVD",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
				Tag:         "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "Example Movie 2001 JAPANESE DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVD encode becomes DVD rip and omits edition and repack",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Uncut Shadows",
				Year:        2001,
				Resolution:  "480p",
				Source:      "PAL DVD",
				Edition:     "Uncut",
				Repack:      "REPACK",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
				Tag:         "-GRP",
			},
			want: "Uncut Shadows 2001 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVD encode without resolution keeps canonical source and edition",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example Movie",
				Year:        2001,
				Source:      "DVD",
				Edition:     "Uncut",
				VideoEncode: "x264",
				Audio:       "DD 2.0",
				Tag:         "-GRP",
			},
			want: "Example Movie 2001 Uncut DVD DD 2.0 x264-GRP",
		},
		{
			name: "DVD disc derives PAL using the DVD system role",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "DVD",
				Title:      "Example Movie",
				Year:       1997,
				Resolution: "576p",
				Source:     "DVD",
				DVDSize:    "DVD5",
				Audio:      "DD 5.1",
				Tag:        "-GRP",
			},
			want: "Example Movie 1997 PAL DVD5 DD 5.1-GRP",
		},
		{
			name: "DVD remux derives NTSC and puts foreign language after year",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "REMUX",
				Title:      "Example Movie",
				Year:       2001,
				Resolution: "480i",
				Source:     "DVD",
				VideoCodec: "MPEG-2",
				Audio:      "DD 5.1",
				Tag:        "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "Example Movie 2001 JAPANESE NTSC DVD REMUX DD 5.1-GRP",
		},
		{
			name: "DVD remux puts foreign language before edition after year",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "REMUX",
				Title:      "Example Movie",
				Year:       2001,
				Resolution: "480i",
				Source:     "DVD",
				Edition:    "Director Cut",
				Audio:      "DD 5.1",
				Tag:        "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "Example Movie 2001 JAPANESE Director Cut NTSC DVD REMUX DD 5.1-GRP",
		},
		{
			name: "TV DVD remux puts foreign language before season",
			request: api.ReleaseNameRequest{
				Category:   "TV",
				Type:       "REMUX",
				Title:      "Example Show",
				Year:       2024,
				AltTitle:   "AKA Alt Show",
				Season:     "S01",
				Resolution: "576p",
				Source:     "PAL DVD",
				VideoCodec: "MPEG-2",
				Audio:      "DD 2.0",
				SearchYear: "2024",
				Tag:        "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "Example Show AKA Alt Show 2024 JAPANESE S01 PAL DVD REMUX DD 2.0-GRP",
		},
		{
			name: "foreign language is a separate marker even when title has matching text",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Uncut JAPANESE 480p DVD Tales",
				Year:        2001,
				Resolution:  "480p",
				Source:      "BluRay",
				VideoEncode: "x264",
				Audio:       "DD 5.1",
				Tag:         "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "Uncut JAPANESE 480p DVD Tales 2001 JAPANESE 480p BluRay DD 5.1 x264-GRP",
		},
		{
			name: "English audio leaves a non disc name unchanged",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example Movie",
				Year:        2001,
				Resolution:  "1080p",
				Source:      "BluRay",
				VideoEncode: "x264",
				Audio:       "DD 5.1",
				Tag:         "-GRP",
			},
			languages: []string{"Japanese", "English"},
			want:      "Example Movie 2001 1080p BluRay DD 5.1 x264-GRP",
		},
		{
			name: "BDMV omits foreign language marker",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "BDMV",
				Title:      "Example Movie",
				Year:       2001,
				Resolution: "1080p",
				Source:     "BluRay",
				VideoCodec: "AVC",
				Audio:      "DD 5.1",
				Tag:        "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "Example Movie 2001 1080p BluRay AVC DD 5.1-GRP",
		},
		{
			name: "first linguistic language marker skips non linguistic values",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example Movie",
				Year:        2001,
				Resolution:  "1080p",
				Source:      "BluRay",
				VideoEncode: "x264",
				Audio:       "DD 5.1",
				Tag:         "-GRP",
			},
			languages: []string{"zxx", "no linguistic content", "und", "undetermined", "Norwegian Nynorsk"},
			want:      "Example Movie 2001 NORWEGIAN NYNORSK 1080p BluRay DD 5.1 x264-GRP",
		},
		{
			name: "all non linguistic audio values omit the marker",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example Movie",
				Year:        2001,
				Resolution:  "1080p",
				Source:      "BluRay",
				VideoEncode: "x264",
				Audio:       "DD 5.1",
				Tag:         "-GRP",
			},
			languages: []string{"zxx", "no linguistic content", "und", "undetermined"},
			want:      "Example Movie 2001 1080p BluRay DD 5.1 x264-GRP",
		},
		{
			name: "yearless DVD remux places foreign language before source",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "REMUX",
				Title:      "Example Movie",
				Resolution: "576p",
				Source:     "PAL DVD",
				Audio:      "DD 2.0",
				Tag:        "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "Example Movie JAPANESE PAL DVD REMUX DD 2.0-GRP",
		},
		{
			name: "DVD disc places foreign language before region and system",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "DVD",
				Title:      "Example Movie",
				Year:       1977,
				Resolution: "480p",
				Source:     "DVD",
				Region:     "USA",
				DVDSize:    "DVD9",
				Audio:      "LPCM 2.0",
			},
			languages: []string{"Japanese"},
			want:      "Example Movie 1977 JAPANESE USA NTSC DVD9 LPCM 2.0",
		},
		{
			name: "DVD disc places foreign language before visible PAL system and source",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "DVD",
				Title:      "Example Movie",
				Year:       1977,
				Resolution: "576p",
				Source:     "PAL DVD",
				Audio:      "LPCM 2.0",
				Tag:        "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "Example Movie 1977 JAPANESE PAL DVD LPCM 2.0-GRP",
		},
		{
			name: "DVD disc places foreign language before visible NTSC system and source",
			request: api.ReleaseNameRequest{
				Category:   "MOVIE",
				Type:       "DISC",
				DiscType:   "DVD",
				Title:      "Example Movie",
				Year:       1977,
				Resolution: "480p",
				Source:     "NTSC DVD",
				Audio:      "LPCM 2.0",
				Tag:        "-GRP",
			},
			languages: []string{"Japanese"},
			want:      "Example Movie 1977 JAPANESE NTSC DVD LPCM 2.0-GRP",
		},
		{
			name: "TV DVD encode retains episode attachment while becoming a rip",
			request: api.ReleaseNameRequest{
				Category:     "TV",
				Type:         "ENCODE",
				Title:        "Example Show",
				Season:       "S04",
				Episode:      "E03",
				EpisodeTitle: "Title",
				Resolution:   "480p",
				Source:       "NTSC DVD",
				VideoEncode:  "x264",
				Audio:        "DD 2.0",
				Tag:          "-GRP",
			},
			languages: []string{"German"},
			want:      "Example Show S04E03 Title GERMAN 480p DVDRip DD 2.0 x264-GRP",
		},
		{
			name: "DVD encode preserves repack text in the title while omitting repack component",
			request: api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "REPACK3 Shadows",
				Year:        1994,
				Resolution:  "480p",
				Source:      "DVD",
				Repack:      "REPACK3",
				VideoEncode: "x264",
				Audio:       "DD 5.1",
				Tag:         "-GRP",
			},
			want: "REPACK3 Shadows 1994 480p DVDRip DD 5.1 x264-GRP",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			subject := dvlGeneratedSubject(t, test.request, test.languages)
			if got := dvlReviewedName(t, subject, nil); got != test.want {
				t.Fatalf("reviewed name = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDVLStructuredReleaseNamePolicyPreservesOpaqueAndManualNames(t *testing.T) {
	t.Parallel()

	request := api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "DVDRIP",
		Title:       "Example Movie",
		Year:        2001,
		Resolution:  "480p",
		Source:      "PAL DVD",
		VideoEncode: "x264",
		Audio:       "DD 2.0",
		Tag:         "-GRP",
	}
	subject := dvlGeneratedSubject(t, request, []string{"Japanese"})
	override := "Manual DVL Name-GRP"
	if got := dvlReviewedName(t, subject, &override); got != override {
		t.Fatalf("opaque override = %q, want %q", got, override)
	}
	missingDocument := subject
	missingDocument.GeneratedName = nil
	missingDocument.ReleaseName = "Opaque Name Without Components-GRP"
	if got := dvlReviewedName(t, missingDocument, nil); got != missingDocument.ReleaseName {
		t.Fatalf("missing document name = %q, want %q", got, missingDocument.ReleaseName)
	}

	manual := subject
	manual.GeneratedName = manual.GeneratedName.Clone()
	for index := range manual.GeneratedName.Components {
		if manual.GeneratedName.Components[index].Role == api.NameRoleSource {
			manual.GeneratedName.Components[index].Manual = true
		}
	}
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got := dvlReviewedName(t, manual, nil); !strings.Contains(got, "PAL DVD") {
		t.Fatalf("manual source was changed: %q", got)
	}

	manualDisc := dvlGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "DISC",
		DiscType:   "DVD",
		Title:      "Example Movie",
		Year:       1997,
		Resolution: "576p",
		Source:     "DVD",
		DVDSize:    "DVD9",
		Audio:      "DD 5.1",
		Tag:        "-GRP",
	}, []string{"Japanese"})
	markDVLGeneratedComponentManual(t, manualDisc.GeneratedName, api.NameRoleSource, true)
	markDVLGeneratedComponentManual(t, manualDisc.GeneratedName, api.NameRoleDVDSystem, false)
	manualDisc.ReleaseName = manualDisc.GeneratedName.Render().Name
	if got, want := dvlReviewedName(t, manualDisc, nil), "Example Movie 1997 JAPANESE DVD DVD9 DD 5.1-GRP"; got != want {
		t.Fatalf("manual DVD source name = %q, want %q", got, want)
	}

	manualRemux := dvlGeneratedSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "REMUX",
		Title:      "Example Movie",
		Resolution: "576p",
		Source:     "DVD",
		Audio:      "DD 2.0",
		Tag:        "-GRP",
	}, []string{"Japanese"})
	markDVLGeneratedComponentManual(t, manualRemux.GeneratedName, api.NameRoleSource, true)
	appendDVLManualComponent(manualRemux.GeneratedName, api.NameRoleDVDSystem)
	manualRemux.ReleaseName = manualRemux.GeneratedName.Render().Name
	if got, want := dvlReviewedName(t, manualRemux, nil), "Example Movie JAPANESE DVD REMUX DD 2.0-GRP"; got != want {
		t.Fatalf("manual DVD remux name = %q, want %q", got, want)
	}

	manualResolution := dvlGeneratedSubject(t, request, []string{"Japanese"})
	markDVLGeneratedComponentManual(t, manualResolution.GeneratedName, api.NameRoleResolution, false)
	manualResolution.ReleaseName = manualResolution.GeneratedName.Render().Name
	if got, want := dvlReviewedName(t, manualResolution, nil), "Example Movie 2001 JAPANESE DVDRip DD 2.0 x264-GRP"; got != want {
		t.Fatalf("manual omitted resolution name = %q, want %q", got, want)
	}
}

func TestDVLProfileUsesStructuredPolicy(t *testing.T) {
	t.Parallel()

	policy := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if policy.ID != "unit3d/dvl/v2" || policy.Structured == nil || policy.Resolver != nil {
		t.Fatalf("DVL policy = %#v, want structured unit3d/dvl/v2", policy)
	}
}

func TestDVLRipKeepsManualAudioMarkersWithAudio(t *testing.T) {
	for _, category := range []string{"MOVIE", "TV"} {
		for _, audio := range []string{"Dubbed DD 2.0", "DD 2.0 Dual-Audio", "Dubbed", "Dual-Audio", ""} {
			t.Run(category+"/"+audio, func(t *testing.T) {
				subject := dvlGeneratedSubject(t, api.ReleaseNameRequest{
					Category:    category,
					Type:        "DVDRIP",
					Title:       "Example Release",
					Year:        2001,
					Source:      "PAL DVD",
					Resolution:  "480p",
					Audio:       audio,
					VideoEncode: "x264",
					Tag:         "-GRP",
				}, nil)
				for index := range subject.GeneratedName.Components {
					component := &subject.GeneratedName.Components[index]
					if component.Role == api.NameRoleDubbed || component.Role == api.NameRoleDualAudio || component.Role == api.NameRoleVideoFormat {
						component.Manual = true
					}
				}
				want := "Example Release 2001 480p DVDRip " + audio + " x264-GRP"
				if category == "TV" {
					want = "Example Release 480p DVDRip " + audio + " x264-GRP"
				}
				want = strings.Join(strings.Fields(want), " ")
				if got := dvlReviewedName(t, subject, nil); got != want {
					t.Fatalf("manual audio marker name = %q, want %q", got, want)
				}
			})
		}
	}
}

func dvlGeneratedSubject(t *testing.T, request api.ReleaseNameRequest, languages []string) api.UploadSubject {
	t.Helper()

	result := metadata.BuildReleaseName(request, api.NopLogger{})
	if result.GeneratedName == nil {
		t.Fatal("BuildReleaseName did not produce a structured document")
	}
	return api.UploadSubject{
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		Release: api.ReleaseInfo{
			Category:   request.Category,
			Title:      request.Title,
			Year:       request.Year,
			Resolution: request.Resolution,
			Size:       request.DVDSize,
		},
		AlternateTitle: request.AltTitle,
		Type:           request.Type,
		DiscType:       request.DiscType,
		Source:         request.Source,
		Audio:          request.Audio,
		VideoCodec:     request.VideoCodec,
		VideoEncode:    request.VideoEncode,
		AudioLanguages: languages,
	}
}

func dvlReviewedName(t *testing.T, subject api.UploadSubject, requested *string) string {
	t.Helper()

	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "DVL",
		Meta:                subject,
		RequestedUploadName: requested,
	}, unit3d.NewWithProfile(Profile()).ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

func markDVLGeneratedComponentManual(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole, present bool) {
	t.Helper()

	for index := range document.Components {
		component := &document.Components[index]
		if component.Role != role {
			continue
		}
		component.Manual = true
		component.Present = present
		return
	}
	t.Fatalf("generated name is missing %s", role)
}

func appendDVLManualComponent(document *api.ReleaseNameDocument, role api.ReleaseNameRole) {
	document.Components = append(document.Components, api.ReleaseNameComponent{
		Role:   role,
		Join:   " ",
		Manual: true,
	})
}
