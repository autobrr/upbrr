// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package azfamily

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAZFamilyDVDEncodePayload(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		source     string
		resolution string
		discType   string
		category   string
	}{
		{
			name:       "DVD movie",
			source:     "DVD",
			resolution: "480p",
			category:   "MOVIE",
		},
		{
			name:       "PAL movie with DVD disc hint",
			source:     "PAL DVD",
			resolution: "576p",
			discType:   "DVD",
			category:   "MOVIE",
		},
		{
			name:       "NTSC episode with DVD disc hint",
			source:     "NTSC DVD",
			resolution: "480p",
			discType:   "DVD",
			category:   "TV",
		},
		{
			name:     "DVD movie without resolution",
			source:   "DVD",
			discType: "DVD",
			category: "MOVIE",
		},
		{
			name:       "PAL episode with unknown resolution",
			source:     "PAL DVD",
			resolution: "OTHER",
			discType:   "DVD",
			category:   "TV",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, site := range []string{"AZ", "CZ", "PHD"} {
				t.Run(site, func(t *testing.T) {
					request := api.ReleaseNameRequest{
						Category:    test.category,
						Type:        "ENCODE",
						Title:       "Example Film",
						Year:        2026,
						Source:      test.source,
						Resolution:  test.resolution,
						DiscType:    test.discType,
						Audio:       "DD 2.0",
						VideoEncode: "x264",
						VideoCodec:  "AVC",
						Tag:         "-GRP",
					}
					prefix := "Example Film 2026"
					if test.category == "TV" {
						request.Season, request.Episode, request.EpisodeTitle = "S01", "E02", "Example Episode"
						request.SearchYear = "2026"
						prefix += " S01E02 Example Episode"
						if site != "CZ" {
							prefix = "Example Film S01E02 Example Episode"
						}
					}
					if test.resolution != "" && test.resolution != "OTHER" {
						prefix += " " + test.resolution
					}
					subject := azFamilyGeneratedSubject(t, request)
					values := azFamilyDVDPayload(t, site, subject, nil)
					want := prefix + " DVDRip DD 2.0 x264-GRP"
					if got := values.Get("file_name"); got != want {
						t.Fatalf("payload name = %q, want %q", got, want)
					}
					if got := values.Get("rip_type_id"); got != "5" {
						t.Fatalf("DVD encode rip_type_id = %q, want 5", got)
					}
					if got := values.Get("video_resolution"); got != test.resolution {
						t.Fatalf("payload resolution = %q, want %q", got, test.resolution)
					}
					if subject.Type != "ENCODE" || subject.Source != test.source {
						t.Fatal("tracker naming changed canonical type or source")
					}
				})
			}
		})
	}
}

func TestAZFamilyDVDRipPayloadBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, typeName, source, discType, want string
	}{
		{"explicit DVD rip", "DVDRIP", "DVD", "", "5"},
		{"explicit PAL rip", "DVDRIP", "PAL DVD", "DVD", "5"},
		{"explicit NTSC rip", "DVDRIP", "NTSC DVD", "DVD", "5"},
		{"full DVD", "DISC", "PAL DVD", "DVD", "4"},
		{"DVD remux", "REMUX", "NTSC DVD", "DVD", "17"},
		{"BluRay encode", "ENCODE", "BluRay", "", "2"},
		{"HD DVD encode", "ENCODE", "HDDVD", "HDDVD", "2"},
		{"disc hint without source", "ENCODE", "", "DVD", "2"},
		{"DVD substring is not a source", "ENCODE", "DVD-R", "", "2"},
		{"normalized DVD encode", " encode ", " ntsc dvd ", "DVD", "5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, site := range []string{"AZ", "CZ", "PHD"} {
				subject := azFamilyGeneratedSubject(t, api.ReleaseNameRequest{
					Category:    "MOVIE",
					Type:        test.typeName,
					Title:       "Example Film",
					Year:        2026,
					Source:      test.source,
					DiscType:    test.discType,
					DVDSize:     "DVD9",
					Resolution:  "480p",
					Audio:       "DD 2.0",
					VideoEncode: "x264",
					VideoCodec:  "MPEG-2",
					Tag:         "-GRP",
				})
				values := azFamilyDVDPayload(t, site, subject, nil)
				if got := values.Get("rip_type_id"); got != test.want {
					t.Fatalf("%s rip_type_id = %q, want %q", site, got, test.want)
				}
				if test.typeName == "DVDRIP" {
					want := "Example Film 2026 480p DVDRip DD 2.0 x264-GRP"
					if got := values.Get("file_name"); got != want {
						t.Fatalf("%s explicit DVDRip name = %q, want %q", site, got, want)
					}
				}
			}
		})
	}
}

func TestAZFamilyDVDEncodePreservesNameAuthority(t *testing.T) {
	t.Parallel()
	for _, site := range []string{"AZ", "CZ", "PHD"} {
		t.Run(site, func(t *testing.T) {
			t.Parallel()
			base := azFamilyGeneratedSubject(t, api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example Film",
				Year:        2026,
				Source:      "DVD",
				DiscType:    "DVD",
				Resolution:  "480p",
				Audio:       "DD 2.0",
				VideoEncode: "x264",
				Tag:         "-GRP",
			})
			const custom = "Chosen DVD Wording-GRP"
			for _, mode := range []string{"requested", "opaque", "scene", "manual source", "omitted source", "cleared source"} {
				t.Run(mode, func(t *testing.T) {
					subject := base
					subject.GeneratedName = base.GeneratedName.Clone()
					want := custom
					var requested *string
					switch mode {
					case "requested":
						name := custom
						requested = &name
					case "opaque":
						subject.GeneratedName = nil
						subject.ReleaseName = custom
					case "scene":
						subject.Scene, subject.SceneName = true, custom
						if site == "PHD" {
							want = "Example Film 2026 480p DVDRip DD 2.0 x264-GRP"
						}
					default:
						for index := range subject.GeneratedName.Components {
							component := &subject.GeneratedName.Components[index]
							if component.Role != api.NameRoleSource {
								continue
							}
							component.Manual = true
							component.Present = mode == "manual source"
							if component.Present {
								component.Value = "Chosen Source"
							} else if mode == "cleared source" {
								component.Value, component.AvailableValue = "", ""
							}
						}
						want = "Example Film 2026 480p DD 2.0 x264-GRP"
						if mode == "manual source" {
							want = "Example Film 2026 480p Chosen Source DD 2.0 x264-GRP"
						}
						subject.ReleaseName = subject.GeneratedName.Render().Name
					}
					values := azFamilyDVDPayload(t, site, subject, requested)
					if got := values.Get("file_name"); got != want {
						t.Fatalf("name = %q, want %q", got, want)
					}
					if got := values.Get("rip_type_id"); got != "5" {
						t.Fatalf("name authority changed DVD classification to %q", got)
					}
				})
			}
		})
	}
}

func TestAZFamilyDVDEncodeProviderPrecedence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		site, want string
	}{
		{"AZ", "English Film 2025"},
		{"CZ", "Cinema Film 2024"},
		{"PHD", "Example Film 2025"},
	} {
		t.Run(test.site, func(t *testing.T) {
			t.Parallel()
			subject := azFamilyGeneratedSubject(t, api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "ENCODE",
				Title:       "Example Film",
				Year:        2026,
				Source:      "DVD",
				DiscType:    "DVD",
				Resolution:  "480p",
				Audio:       "DD 2.0",
				VideoEncode: "x264",
				Tag:         "-GRP",
			})
			subject.SourcePath = "prepared/source"
			subject.Identity.SourcePath = subject.SourcePath
			subject.Identity.TMDBID, subject.Identity.IMDBID = 123, 456
			subject.ProviderMetadata = api.SourceScopedMetadata{
				SourcePath: subject.SourcePath,
				TMDB: &api.TMDBMetadata{
					TMDBID: 123,
					Title:  "English Film",
					Year:   2025,
				},
				IMDB: &api.IMDBMetadata{
					IMDBID: 456,
					AKA:    "Original Film",
					Year:   2024,
					Akas: []api.IMDBAKA{{
						Title:    "Cinema Film",
						Country:  "Otherland",
						Language: "English",
					}},
				},
			}
			const suffix = "480p DVDRip DD 2.0 x264-GRP"
			if got, want := azFamilyDVDPayload(t, test.site, subject, nil).Get("file_name"), test.want+" "+suffix; got != want {
				t.Fatalf("provider name = %q, want %q", got, want)
			}
			for _, present := range []bool{true, false} {
				manual := subject
				manual.GeneratedName = subject.GeneratedName.Clone()
				markAZFamilyComponentManual(t, manual.GeneratedName, api.NameRoleTitle, present)
				markAZFamilyComponentManual(t, manual.GeneratedName, api.NameRoleYear, present)
				manual.ReleaseName = manual.GeneratedName.Render().Name
				want := suffix
				if present {
					want = "Example Film 2026 " + suffix
				}
				if got := azFamilyDVDPayload(t, test.site, manual, nil).Get("file_name"); got != want {
					t.Fatalf("manual title/year present=%t: name = %q, want %q", present, got, want)
				}
			}
			if test.site == "CZ" {
				subject.EffectiveMetadata.OriginalTitleProvenance = api.FactProvenanceManualEmpty
				_, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
					Tracker: test.site,
					Meta:    subject,
				}, New(test.site).ReleaseNamePolicy())
				if failure == nil {
					t.Fatal("DVD encode restored the provider original title after an explicit clear")
				}
			}
		})
	}
}

func azFamilyDVDPayload(t *testing.T, site string, subject api.UploadSubject, requested *string) url.Values {
	t.Helper()
	before := subject.GeneratedName.Clone()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             site,
		Meta:                subject,
		RequestedUploadName: requested,
	}, New(site).ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure)
	}
	values, err := buildFinalPayload(t.Context(), siteFor(site), sessionState{}, prepared, "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("file_name") != prepared.Projection.UploadReleaseName {
		t.Fatal("payload did not consume the reviewed name")
	}
	if !reflect.DeepEqual(before, subject.GeneratedName) || prepared.Meta.Type != subject.Type || prepared.Meta.Source != subject.Source {
		t.Fatal("name projection or payload changed canonical naming facts")
	}
	if prepared.Projection.DuplicateCriteria.Name != strings.TrimSpace(subject.Release.Title) {
		t.Fatal("upload wording replaced the canonical search title")
	}
	return values
}
