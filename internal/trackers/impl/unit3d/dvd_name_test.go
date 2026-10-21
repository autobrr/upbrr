// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package unit3d_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/dp"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/hhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lume"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ulcx"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/yus"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDVDEncodeSiteNamesAndPayloads(t *testing.T) {
	for _, profile := range dvdProfiles() {
		for _, test := range []struct {
			source, resolution, system, category string
		}{
			{
				source:   "PAL DVD",
				system:   "PAL",
				category: "MOVIE",
			},
			{
				source:     "NTSC DVD",
				resolution: "OTHER",
				system:     "NTSC",
				category:   "TV",
			},
			{
				source:     "DVD",
				resolution: "576i",
				system:     "PAL",
				category:   "MOVIE",
			},
			{
				source:     "DVD",
				resolution: "480p",
				system:     "NTSC",
				category:   "TV",
			},
			{
				source:     "PAL DVD",
				resolution: "480i",
				system:     "PAL",
				category:   "TV",
			},
			{
				source:     "NTSC DVD",
				resolution: "576p",
				system:     "NTSC",
				category:   "MOVIE",
			},
		} {
			t.Run(profile.Name+"_"+test.source+"_"+test.resolution, func(t *testing.T) {
				subject := dvdSubject(t, "ENCODE", test.source, test.resolution, test.category)
				before := subject.GeneratedName.Clone()
				prepared, name := dvdReviewedName(t, profile, subject, nil)
				prefix := "Example Release 2026 "
				if test.category == "TV" {
					prefix += "S01E02 Example Episode "
				}
				want := prefix + test.system + " DVD AAC 2.0-GRP"
				if profile.Name == "DP" || profile.Name == "LUME" {
					resolution := test.resolution
					if resolution == "OTHER" {
						resolution = ""
					}
					source := test.system + " DVDRip"
					if profile.Name == "LUME" {
						source = "DVDRip"
					}
					want = prefix + strings.TrimSpace(resolution+" "+source) + " AAC 2.0 x264-GRP"
				}
				if name != want {
					t.Fatalf("name = %q, want %q", name, want)
				}
				if prepared.Meta.Type != "ENCODE" || prepared.Meta.Source != test.source || !reflect.DeepEqual(before, subject.GeneratedName) {
					t.Fatal("site naming mutated canonical type, source, or generated document")
				}
				data, err := unit3d.DVDTestPayload(prepared, name, profile.Site)
				if err != nil {
					t.Fatal(err)
				}
				if data["name"] != want || data["type_id"] != "3" {
					t.Fatalf("payload name/type_id = %q/%q, want %q/3", data["name"], data["type_id"], want)
				}
				if test.category == "TV" && (data["season_number"] != "1" || data["episode_number"] != "2") {
					t.Fatalf("TV payload coordinates = %q/%q", data["season_number"], data["episode_number"])
				}
			})
		}
	}
}

func TestDVDNameSystemUsesFinalizedReleaseFacts(t *testing.T) {
	for _, profile := range dvdProfiles() {
		if profile.Name == "LUME" {
			continue
		}
		for _, test := range []struct {
			resolution, system string
		}{
			{resolution: "576p", system: "PAL"},
			{resolution: "480i", system: "NTSC"},
			{},
			{resolution: "OTHER"},
			{resolution: "1080p"},
		} {
			t.Run(profile.Name+"_"+test.resolution, func(t *testing.T) {
				subject := dvdSubject(t, "ENCODE", "DVD", test.resolution, "MOVIE")
				subject.Release.Source = "NTSC DVD"
				subject.Filename = "Example.Release.PAL.DVD.576p.mkv"
				// A conflicting inspected file cannot replace the selected release resolution.
				fileResolution := "576p"
				if test.system == "PAL" {
					fileResolution = "480p"
				}
				subject.MediaFileFacts = api.MediaFileFacts{
					TechnicalStatus: api.MetadataEvidenceStatusComplete,
					Files: []api.MediaFileFact{{
						Primary:    true,
						Source:     "NTSC DVD",
						Resolution: fileResolution,
					}},
				}
				prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: profile.Name, Meta: subject},
					unit3d.NewWithProfile(profile).ReleaseNamePolicy())
				if test.system == "" {
					var rule *trackers.NameRuleError
					if failure == nil || !errors.As(failure, &rule) || rule.Role != api.NameRoleSource ||
						!strings.Contains(rule.Reason, "Source") || !strings.Contains(rule.Reason, "Resolution") {
						t.Fatalf("expected actionable Source naming error, got %v", failure)
					}
					return
				}
				if failure != nil {
					t.Fatal(failure.Unwrap())
				}
				name, err := prepared.ReviewedUploadName()
				if err != nil || !strings.Contains(name, test.system+" DVD") {
					t.Fatalf("release-level system not retained: name=%q error=%v", name, err)
				}
			})
		}
	}
	for _, resolution := range []string{"", "OTHER", "1080p"} {
		t.Run("LUME_"+resolution, func(t *testing.T) {
			_, name := dvdReviewedName(t, lume.Profile(), dvdSubject(t, "ENCODE", "DVD", resolution, "MOVIE"), nil)
			if !strings.Contains(name, " DVDRip ") {
				t.Fatalf("LUME did not retain system-independent DVDRip: %q", name)
			}
		})
	}
}

func TestDVDRemuxSiteNamesAndPayloads(t *testing.T) {
	for _, profile := range dvdProfiles() {
		for _, source := range []string{"DVD", "PAL DVD", "NTSC DVD"} {
			t.Run(profile.Name+"_"+source, func(t *testing.T) {
				subject := dvdSubject(t, "REMUX", source, "576p", "TV")
				prepared, name := dvdReviewedName(t, profile, subject, nil)
				label := "PAL DVD"
				if source == "NTSC DVD" {
					label = source
				}
				if profile.Name == "LUME" {
					label = source
				}
				if profile.Name == "DP" || profile.Name == "LUME" {
					label += " REMUX"
				}
				want := "Example Release 2026 S01E02 Example Episode " + label + " AAC 2.0-GRP"
				if name != want {
					t.Fatalf("name = %q, want %q", name, want)
				}
				data, err := unit3d.DVDTestPayload(prepared, name, profile.Site)
				if err != nil || data["type_id"] != "2" || data["name"] != want {
					t.Fatalf("remux payload name/type_id = %q/%q, error=%v", data["name"], data["type_id"], err)
				}
			})
		}
	}
}

func TestDVDNamePreservesManualComponents(t *testing.T) {
	roles := []api.ReleaseNameRole{
		api.NameRoleSource, api.NameRoleResolution, api.NameRoleVideoFormat, api.NameRoleVideoCodec, api.NameRoleVideoEncode,
	}
	for _, profile := range dvdProfiles() {
		if profile.Name == "LUME" {
			continue
		}
		for _, releaseType := range []string{"ENCODE", "REMUX"} {
			for _, present := range []bool{true, false} {
				t.Run(profile.Name+"_"+releaseType+"_"+map[bool]string{true: "manual values", false: "manual omissions"}[present], func(t *testing.T) {
					subject := dvdSubject(t, releaseType, "DVD", "576p", "MOVIE")
					subject.Release.Resolution = ""
					if releaseType == "ENCODE" {
						// ENCODE has no generated format token; supply one explicitly
						// to exercise the same manual-component authority boundary.
						subject.GeneratedName.Components = append(subject.GeneratedName.Components, api.ReleaseNameComponent{
							Role:   api.NameRoleVideoFormat,
							Join:   " ",
							Manual: true,
						})
					}
					for _, role := range roles {
						dvdSetManualComponent(t, &subject, role, "Manual-"+string(role), present)
					}
					_, name := dvdReviewedName(t, profile, subject, nil)
					if name != subject.ReleaseName {
						t.Fatalf("manual components changed: %q, want %q", name, subject.ReleaseName)
					}
				})
			}
		}
		t.Run(profile.Name+"_manual generic remux source", func(t *testing.T) {
			subject := dvdSubject(t, "REMUX", "DVD", "", "MOVIE")
			dvdSetManualComponent(t, &subject, api.NameRoleSource, "DVD", true)
			_, name := dvdReviewedName(t, profile, subject, nil)
			want := "Example Release 2026 DVD AAC 2.0-GRP"
			if profile.Name == "DP" {
				want = "Example Release 2026 DVD REMUX AAC 2.0-GRP"
			}
			if name != want {
				t.Fatalf("manual source/default layout = %q, want %q", name, want)
			}
		})
	}
}

func TestDVDNamePreservesExactNames(t *testing.T) {
	for _, profile := range dvdProfiles() {
		for _, mode := range []string{"requested", "opaque", "full manual", "scene"} {
			t.Run(profile.Name+"_"+mode, func(t *testing.T) {
				subject := dvdSubject(t, "ENCODE", "DVD", "", "TV")
				want := "Exact.DVD.S01E02.480p.x264-GRP"
				var requested *string
				switch mode {
				case "requested":
					// Requested upload names retain independently generated duplicate names.
					subject = dvdSubject(t, "ENCODE", "DVD", "480p", "TV")
					requested = &want
				case "opaque":
					subject.GeneratedName = nil
					subject.ReleaseName = want
				case "full manual":
					subject.ReleaseName = want
				case "scene":
					subject.Scene, subject.SceneName, subject.ReleaseName = true, want, want
				}
				prepared, name := dvdReviewedName(t, profile, subject, requested)
				if name != want {
					t.Fatalf("exact name changed to %q", name)
				}
				data, err := unit3d.DVDTestPayload(prepared, name, profile.Site)
				if err != nil || data["name"] != want || data["type_id"] != "3" {
					t.Fatalf("exact-name payload name/type_id = %q/%q, error=%v", data["name"], data["type_id"], err)
				}
			})
		}
	}
}

func TestDVDNamePreservesOtherFormats(t *testing.T) {
	for _, profile := range dvdProfiles() {
		for _, test := range []struct {
			releaseType, source, resolution string
		}{
			{releaseType: "DISC", source: "PAL DVD"},
			{
				releaseType: "ENCODE",
				source:      "BluRay",
				resolution:  "1080p",
			},
			{
				releaseType: "ENCODE",
				source:      "HDDVD",
				resolution:  "1080p",
			},
			{
				releaseType: "REMUX",
				source:      "BluRay",
				resolution:  "1080p",
			},
			{
				releaseType: "WEBDL",
				source:      "Web",
				resolution:  "1080p",
			},
			{
				releaseType: "DVDRIP",
				source:      "PAL DVD",
				resolution:  "576p",
			},
		} {
			t.Run(profile.Name+"_"+test.releaseType+"_"+test.source, func(t *testing.T) {
				subject := dvdSubject(t, test.releaseType, test.source, test.resolution, "MOVIE")
				prepared, name := dvdReviewedName(t, profile, subject, nil)
				if name != subject.ReleaseName {
					t.Fatalf("unrelated format changed: %q, want %q", name, subject.ReleaseName)
				}
				if test.releaseType == "DISC" {
					wantType := "1"
					if profile.Name == "YUS" {
						wantType = "17"
					}
					data, err := unit3d.DVDTestPayload(prepared, name, profile.Site)
					if err != nil || data["type_id"] != wantType {
						t.Fatalf("disc type_id = %q, want %q; error=%v", data["type_id"], wantType, err)
					}
				}
			})
		}
	}
}

func dvdProfiles() []unit3d.Profile {
	return []unit3d.Profile{dp.Profile(), hhd.Profile(), ulcx.Profile(), yus.Profile(), lume.Profile()}
}

func dvdRequest(releaseType, source, resolution, category string) api.ReleaseNameRequest {
	request := api.ReleaseNameRequest{
		Category:    category,
		Type:        releaseType,
		Title:       "Example Release",
		Year:        2026,
		SearchYear:  "2026",
		Source:      source,
		Resolution:  resolution,
		VideoCodec:  "AVC",
		VideoEncode: "x264",
		Audio:       "AAC 2.0",
		Tag:         "-GRP",
	}
	if releaseType == "DISC" {
		request.DiscType, request.DVDSize = "DVD", "DVD9"
	}
	if category == "TV" {
		request.Season, request.Episode, request.EpisodeTitle = "S01", "E02", "Example Episode"
	}
	return request
}

func dvdSubject(t *testing.T, releaseType, source, resolution, category string) api.UploadSubject {
	t.Helper()
	request := dvdRequest(releaseType, source, resolution, category)
	result := metadata.BuildReleaseName(request, api.NopLogger{})
	if result.GeneratedName == nil {
		t.Fatal("missing generated DVD document")
	}
	canonicalCategory := api.CanonicalCategoryMovie
	if category == "TV" {
		canonicalCategory = api.CanonicalCategoryTV
	}
	return api.UploadSubject{
		SourcePath:       "dvd-source",
		Type:             releaseType,
		Source:           source,
		DiscType:         request.DiscType,
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
		VideoCodec:       request.VideoCodec,
		VideoEncode:      request.VideoEncode,
		SeasonInt:        1,
		EpisodeInt:       2,
		Release: api.ReleaseInfo{
			Category:   category,
			Type:       releaseType,
			Source:     source,
			Title:      request.Title,
			Year:       request.Year,
			Resolution: resolution,
		},
		Identity: api.ExternalIdentity{
			SourcePath: "dvd-source",
			Generation: 1,
			Category:   canonicalCategory,
			IMDBID:     1,
			TMDBID:     1,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: "dvd-source",
			Generation: 1,
			IMDB: &api.IMDBMetadata{
				IMDBID: 1,
				Title:  request.Title,
				Year:   request.Year,
			},
			TMDB: &api.TMDBMetadata{
				TMDBID:   1,
				Title:    request.Title,
				Year:     request.Year,
				Category: category,
			},
		},
		LanguageFacts: api.LanguageFacts{Tracks: []api.MediaTrackFacts{{
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Default:      true,
			DefaultKnown: true,
			Codec:        "AAC",
			AudioLabel:   request.Audio,
		}}},
	}
}

func dvdReviewedName(t *testing.T, profile unit3d.Profile, subject api.UploadSubject, requested *string) (trackers.PreparationInput, string) {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             profile.Name,
		Meta:                subject,
		RequestedUploadName: requested,
	}, unit3d.NewWithProfile(profile).ReleaseNamePolicy())
	if failure != nil {
		t.Fatal(failure.Unwrap())
	}
	name, err := prepared.ReviewedUploadName()
	if err != nil {
		t.Fatal(err)
	}
	return prepared, name
}

func dvdSetManualComponent(t *testing.T, subject *api.UploadSubject, role api.ReleaseNameRole, value string, present bool) {
	t.Helper()
	for index := range subject.GeneratedName.Components {
		component := &subject.GeneratedName.Components[index]
		if component.Role == role {
			component.Value, component.Present, component.Manual = value, present, true
			subject.ReleaseName = subject.GeneratedName.Render().Name
			return
		}
	}
	t.Fatalf("manual role %s missing", role)
}
