// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/mediafacts"
	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/azfamily"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/dp"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/hhd"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/lume"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/ulcx"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d/sites/yus"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestDVDEncodeGeneratedNameKeepsEncodeLayout(t *testing.T) {
	for _, category := range []string{"MOVIE", "TV"} {
		for _, source := range []string{"DVD", "PAL DVD", "NTSC DVD"} {
			for _, resolution := range []string{"576p", ""} {
				t.Run(category+"_"+source+"_"+resolution, func(t *testing.T) {
					request := api.ReleaseNameRequest{
						Category:           category,
						Type:               "ENCODE",
						Title:              "Example Release",
						Year:               2026,
						SearchYear:         "2026",
						Source:             source,
						Resolution:         resolution,
						Season:             "S00",
						Episode:            "E02",
						EpisodeTitle:       "Arrival",
						ManualEpisodeTitle: true,
						Edition:            "Uncut",
						VideoEncode:        "x264",
						Audio:              "DD 2.0",
						Tag:                "-GRP",
					}
					baseline := request
					baseline.Source = "BluRay"
					want := strings.Replace(BuildReleaseName(baseline, api.NopLogger{}).Name, " BluRay ", " DVDRip ", 1)
					result := BuildReleaseName(request, api.NopLogger{})
					if result.Name != want || strings.Count(result.Name, "DVDRip") != 1 {
						t.Fatalf("DVD encode name = %q, want ENCODE layout %q", result.Name, want)
					}
					component, found := result.GeneratedName.Component(api.NameRoleSource)
					if !found || !component.Present || component.Value != "DVDRip" {
						t.Fatalf("DVD encode source component = %+v, found=%t", component, found)
					}
					if category == "TV" && !strings.Contains(result.Name, "S00E02 Arrival") {
						t.Fatalf("DVD encode lost TV episode components: %q", result.Name)
					}
				})
			}
		}
	}
}

func TestDVDEncodeRefreshKeepsFactCorrectionsFormatable(t *testing.T) {
	for _, source := range []string{"DVD", "PAL DVD", "NTSC DVD"} {
		for _, resolution := range []string{"576p", "", "OTHER"} {
			t.Run(source+"_"+resolution, func(t *testing.T) {
				state := dvdEncodeRefreshState(t, source, resolution)
				state.ReleaseNameOverrides.Edition = new("Uncut")
				state.ReleaseNameOverrides.NoEdition = new(true)
				svc := newTestService(t, &fakeRepo{}, WithConfig(config.Config{}))
				for range 2 {
					var err error
					state, err = svc.deriveMediaFacts(t.Context(), state)
					if err != nil {
						t.Fatal(err)
					}
					if state.Type != "ENCODE" || state.Source != source || state.Release.Resolution != resolution {
						t.Fatalf("corrected facts changed: type=%q source=%q resolution=%q", state.Type, state.Source, state.Release.Resolution)
					}
					for _, role := range []api.ReleaseNameRole{api.NameRoleSource, api.NameRoleVideoFormat, api.NameRoleResolution, api.NameRoleEdition} {
						component, found := state.GeneratedName.Component(role)
						wantManual := role == api.NameRoleEdition || role == api.NameRoleResolution && resolution != "576p"
						if wantManual && !found || found && component.Manual != wantManual {
							t.Fatalf("role %s = %+v, found=%t, want manual=%t", role, component, found, wantManual)
						}
					}
					name := prepareStructuredName(t, trackerLookupSubject(state), "metadata/dvd-encode-refresh/v1", func(editor *trackers.NameEditor) error {
						if err := editor.Set(api.NameRoleSource, "PAL DVD"); err != nil {
							return fmt.Errorf("format DVD source: %w", err)
						}
						if err := editor.Omit(api.NameRoleResolution); err != nil {
							return fmt.Errorf("omit DVD resolution: %w", err)
						}
						return nil
					})
					if !strings.Contains(name, " PAL DVD ") || strings.Contains(name, "576p") || strings.Contains(name, "Uncut") {
						t.Fatalf("fact correction prevented formatting or restored omitted edition: %q", name)
					}
				}
			})
		}
	}
}

func TestDVDEncodeRefreshReachesRegisteredSitePolicies(t *testing.T) {
	profiles := []unit3d.Profile{hhd.Profile(), ulcx.Profile(), yus.Profile(), dp.Profile(), lume.Profile()}
	registry := trackers.NewRegistry()
	for _, profile := range profiles {
		if err := registry.Register(unit3d.NewWithProfile(profile)); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(azfamily.New("CZ")); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"DVD", "PAL DVD", "NTSC DVD"} {
		t.Run(source, func(t *testing.T) {
			state := dvdEncodeRefreshState(t, source, "576p")
			svc := newTestService(t, &fakeRepo{}, WithConfig(config.Config{}))
			for range 2 {
				var err error
				state, err = svc.deriveMediaFacts(t.Context(), state)
				if err != nil {
					t.Fatal(err)
				}
			}
			subject := trackerLookupSubject(state)
			subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage:      "English",
				Tracks:                state.MediaTracks,
				PrimaryAudioTrackID:   state.PrimaryAudioTrackID,
				TrackCoverageComplete: state.TrackCoverageComplete,
			})
			standard := "PAL"
			if source == "NTSC DVD" {
				standard = "NTSC"
			}
			for _, site := range []string{"HHD", "ULCX", "YUS", "DP", "LUME", "CZ"} {
				descriptor, found := registry.LookupDescriptor(site)
				if !found {
					t.Fatalf("missing registered site %s", site)
				}
				want := "Example Film 2026 576p DVDRip DD 2.0 x264-GRP"
				switch site {
				case "HHD", "ULCX", "YUS":
					want = "Example Film 2026 " + standard + " DVD DD 2.0-GRP"
				case "DP":
					want = "Example Film 2026 576p " + standard + " DVDRip DD 2.0 x264-GRP"
				}
				for _, requested := range []*string{nil, new("Complete Manual Name-GRP")} {
					prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
						Tracker:             site,
						Meta:                subject,
						RequestedUploadName: requested,
					}, descriptor.ReleaseNamePolicy)
					if failure != nil {
						t.Fatalf("%s projection: %v", site, failure)
					}
					got, err := prepared.ReviewedUploadName()
					if requested != nil {
						want = *requested
					}
					if err != nil || got != want {
						t.Fatalf("%s refreshed name=%q want=%q err=%v", site, got, want, err)
					}
				}
			}
			if subject.Type != "ENCODE" || subject.Source != source || !strings.Contains(subject.ReleaseName, " DVDRip ") {
				t.Fatalf("site presentation changed canonical facts/name: %q %q %q", subject.Type, subject.Source, subject.ReleaseName)
			}
		})
	}
}

func TestDVDEncodeAuthorityExceptionDoesNotAffectOtherFacts(t *testing.T) {
	for _, test := range []struct{ releaseType, source string }{
		{"ENCODE", "BluRay"}, {"ENCODE", "HDDVD"}, {"ENCODE", ""}, {"REMUX", "DVD"}, {"DISC", "DVD"}, {"DVDRIP", "DVD"},
	} {
		t.Run(test.releaseType+"_"+test.source, func(t *testing.T) {
			state := dvdEncodeRefreshState(t, test.source, "576p")
			state.ReleaseNameOverrides.Type = new(test.releaseType)
			if test.releaseType == "DISC" {
				state.DiscType = "DVD"
			}
			state, err := newTestService(t, &fakeRepo{}, WithConfig(config.Config{})).deriveMediaFacts(t.Context(), state)
			if err != nil {
				t.Fatal(err)
			}
			for _, role := range []api.ReleaseNameRole{api.NameRoleSource, api.NameRoleVideoFormat, api.NameRoleResolution} {
				component, found := state.GeneratedName.Component(role)
				if !found || !component.Manual {
					t.Fatalf("unrelated corrected role lost authority: %s %+v found=%t", role, component, found)
				}
			}
		})
	}
}

func dvdEncodeRefreshState(t *testing.T, source, resolution string) preparationstate.State {
	t.Helper()
	root := t.TempDir()
	mediaPath := filepath.Join(root, "mediainfo.json")
	payload := `{"media":{"track":[{"@type":"General","Format":"Matroska"},{"@type":"Video","Format":"AVC","Width":"720","Height":"480","ScanType":"Progressive","Encoded_Library_Name":"x264"},{"@type":"Audio","ID":"1","Format":"AC-3","Channels":"2","Language":"en","Default":"Yes"}]}}`
	if err := os.WriteFile(mediaPath, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(root, "Example.Film.2026.BluRay.mkv")
	return preparationstate.State{
		SourcePath:        sourcePath,
		VideoPath:         sourcePath,
		MediaInfoJSONPath: mediaPath,
		Type:              "REMUX",
		Source:            "BluRay",
		Tag:               "-GRP",
		Release: api.ReleaseInfo{
			Category:   "MOVIE",
			Title:      "Example Film",
			Year:       2026,
			Resolution: "1080p",
			Type:       "REMUX",
			Source:     "BluRay",
			Group:      "GRP",
		},
		Identity: api.ExternalIdentity{
			SourcePath: sourcePath,
			Generation: 1,
			Category:   api.CanonicalCategoryMovie,
			TMDBID:     1,
			IMDBID:     1,
		},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: sourcePath,
			Generation: 1,
			TMDB: &api.TMDBMetadata{
				TMDBID:           1,
				Category:         "MOVIE",
				Title:            "Example Film",
				Year:             2026,
				OriginalLanguage: "en",
			},
			IMDB: &api.IMDBMetadata{
				IMDBID: 1,
				Title:  "Example Film",
				Year:   2026,
			},
		},
		MetadataOverrides: api.MetadataOverrides{OriginalLanguage: new("English")},
		ReleaseNameOverrides: api.ReleaseNameOverrides{
			Type:       new("ENCODE"),
			Source:     new(source),
			Resolution: new(resolution),
		},
	}
}
