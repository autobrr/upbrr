// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"fmt"
	"strings"
	"testing"

	preparationstate "github.com/autobrr/upbrr/internal/preparedrelease/state"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestBuildReleaseNameUsesCodecRoleWhenMediaEncodeIsUnavailable(t *testing.T) {
	media := mediaInfoDoc{}
	media.Media.Track = []map[string]any{{
		"@type":                "Video",
		"Format":               "MPEG-4 Visual",
		"Encoded_Library_Name": "ffmpeg",
	}}
	videoEncode, videoCodec, _, _ := videoEncodeFromMedia(media, "ENCODE")
	if videoEncode != "" || videoCodec != "MPEG-4 Visual" {
		t.Fatalf("derived video facts = encode %q, codec %q", videoEncode, videoCodec)
	}

	for _, test := range []struct {
		name     string
		category string
		typeName string
	}{
		{
			name:     "movie encode",
			category: "MOVIE",
			typeName: "ENCODE",
		},
		{
			name:     "movie web dl",
			category: "MOVIE",
			typeName: "WEBDL",
		},
		{
			name:     "movie web rip",
			category: "MOVIE",
			typeName: "WEBRIP",
		},
		{
			name:     "movie hdtv",
			category: "MOVIE",
			typeName: "HDTV",
		},
		{
			name:     "movie dvd rip",
			category: "MOVIE",
			typeName: "DVDRIP",
		},
		{
			name:     "tv encode",
			category: "TV",
			typeName: "ENCODE",
		},
		{
			name:     "tv web dl",
			category: "TV",
			typeName: "WEBDL",
		},
		{
			name:     "tv web rip",
			category: "TV",
			typeName: "WEBRIP",
		},
		{
			name:     "tv hdtv",
			category: "TV",
			typeName: "HDTV",
		},
		{
			name:     "tv dvd rip",
			category: "TV",
			typeName: "DVDRIP",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := api.ReleaseNameRequest{
				Category:    test.category,
				Type:        test.typeName,
				Title:       "Example Release",
				Year:        2026,
				SearchYear:  "2026",
				Season:      "S01",
				Episode:     "E02",
				Resolution:  "576p",
				Source:      "DVD",
				Service:     "NF",
				Audio:       "AC3 2.0",
				VideoCodec:  videoCodec,
				VideoEncode: videoEncode,
			}
			result := BuildReleaseName(request, api.NopLogger{})
			codec, codecOK := result.GeneratedName.Component(api.NameRoleVideoCodec)
			if !codecOK || !codec.Present || codec.Value != videoCodec || codec.AvailableValue != videoCodec {
				t.Fatalf("codec component = %#v, found=%t", codec, codecOK)
			}
			if encode, exists := result.GeneratedName.Component(api.NameRoleVideoEncode); exists {
				t.Fatalf("unexpected encode component = %#v", encode)
			}
			if strings.Count(result.NameNoTag, videoCodec) != 1 {
				t.Fatalf("codec duplicated in %q", result.NameNoTag)
			}

			subject := trackerLookupSubject(preparationstate.State{
				Type:        request.Type,
				Source:      request.Source,
				VideoCodec:  videoCodec,
				VideoEncode: videoEncode,
				Release: api.ReleaseInfo{
					Category: request.Category,
					Title:    request.Title,
					Year:     request.Year,
					Type:     request.Type,
					Source:   request.Source,
				},
				ReleaseName:      result.Name,
				ReleaseNameNoTag: result.NameNoTag,
				GeneratedName:    result.GeneratedName,
			})
			included := prepareStructuredName(t, subject, "metadata/codec-include/v1", func(editor *trackers.NameEditor) error {
				return editor.Include(api.NameRoleVideoCodec)
			})
			if included != result.Name || strings.Count(included, videoCodec) != 1 {
				t.Fatalf("codec inclusion rendered %q; want unchanged %q", included, result.Name)
			}
			omittedEncode := prepareStructuredName(t, subject, "metadata/encode-omit/v1", func(editor *trackers.NameEditor) error {
				return editor.Omit(api.NameRoleVideoEncode)
			})
			if omittedEncode != result.Name {
				t.Fatalf("absent encode omission rendered %q; want unchanged %q", omittedEncode, result.Name)
			}
			omitted := prepareStructuredName(t, subject, "metadata/codec-omit/v1", func(editor *trackers.NameEditor) error {
				return editor.Omit(api.NameRoleVideoCodec)
			})
			if strings.Contains(omitted, videoCodec) {
				t.Fatalf("codec omission rendered %q", omitted)
			}
			moved := prepareStructuredName(t, subject, "metadata/codec-move/v1", func(editor *trackers.NameEditor) error {
				return editor.MoveBefore(api.NameRoleVideoCodec, api.NameRoleAudio)
			})
			if strings.Count(moved, videoCodec) != 1 || strings.Index(moved, videoCodec) > strings.Index(moved, request.Audio) {
				t.Fatalf("codec move rendered %q", moved)
			}
		})
	}
}

func TestBuildReleaseNameRetainsDVDDiscFactsForIndependentPolicyOperations(t *testing.T) {
	for _, test := range []struct {
		name          string
		category      string
		season        string
		size          string
		sourceVisible bool
		wantName      string
	}{
		{
			name:     "movie known size",
			category: "MOVIE",
			size:     "DVD9",
			wantName: "Example Release 2026 PAL DVD9",
		},
		{
			name:          "movie missing size",
			category:      "MOVIE",
			sourceVisible: true,
			wantName:      "Example Release 2026 PAL DVD",
		},
		{
			name:          "tv unknown size",
			category:      "TV",
			season:        "S01",
			size:          "DVD10",
			sourceVisible: true,
			wantName:      "Example Release S01 PAL DVD DVD10",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta, err := NewService(&fakeRepo{}, WithConfig(config.Config{})).deriveMediaFacts(t.Context(), preparationstate.State{
				SourcePath: "Example.Release.2026.PAL.DVD",
				DiscType:   "DVD",
				Type:       "DISC",
				Source:     "PAL DVD",
				SeasonStr:  test.season,
				Release: api.ReleaseInfo{
					Category: test.category,
					Title:    "Example Release",
					Year:     2026,
					Type:     "DISC",
					Source:   "PAL DVD",
					Size:     test.size,
				},
			})
			if err != nil {
				t.Fatalf("derive media facts: %v", err)
			}
			if meta.Source != "PAL DVD" || meta.Type != "DISC" || meta.Release.Size != test.size {
				t.Fatalf("derived DVD facts = source %q type %q size %q", meta.Source, meta.Type, meta.Release.Size)
			}
			if meta.ReleaseNameNoTag != test.wantName {
				t.Fatalf("name = %q, want %q", meta.ReleaseNameNoTag, test.wantName)
			}
			assertReleaseNameComponent(t, meta.GeneratedName, api.NameRoleSource, "DVD", test.sourceVisible)
			assertReleaseNameComponent(t, meta.GeneratedName, api.NameRoleVideoFormat, "DISC", false)
			assertReleaseNameComponent(t, meta.GeneratedName, api.NameRoleDVDSystem, "PAL", true)
			assertReleaseNameComponent(t, meta.GeneratedName, api.NameRoleDVDSize, test.size, test.size != "")
			if meta.AvailableGeneratedName == nil {
				t.Fatal("media producer did not retain available DVD naming facts")
			}
			assertReleaseNameComponent(t, meta.AvailableGeneratedName, api.NameRoleSource, "DVD", test.sourceVisible)
			assertReleaseNameComponent(t, meta.AvailableGeneratedName, api.NameRoleVideoFormat, "DISC", false)

			subject := trackerLookupSubject(meta)
			withSource := prepareStructuredName(t, subject, "metadata/dvd-source/v1", func(editor *trackers.NameEditor) error {
				return editor.Include(api.NameRoleSource)
			})
			if test.sourceVisible {
				if withSource != meta.ReleaseName {
					t.Fatalf("source inclusion rendered %q; want idempotent %q", withSource, meta.ReleaseName)
				}
			} else if strings.Count(withSource, "DVD") != 2 || !strings.Contains(withSource, "PAL DVD DVD9") {
				t.Fatalf("source inclusion rendered %q", withSource)
			}
			withType := prepareStructuredName(t, subject, "metadata/dvd-type/v1", func(editor *trackers.NameEditor) error {
				if err := editor.Include(api.NameRoleVideoFormat); err != nil {
					return fmt.Errorf("include DVD format: %w", err)
				}
				return editor.Set(api.NameRoleVideoFormat, "DVD-VIDEO")
			})
			if !strings.HasSuffix(withType, "DVD-VIDEO") || strings.Count(withType, "DVD-VIDEO") != 1 ||
				(test.sourceVisible && !strings.Contains(withType, "PAL DVD")) {
				t.Fatalf("type inclusion rendered %q", withType)
			}
			if test.size != "" {
				movedSize := prepareStructuredName(t, subject, "metadata/dvd-size-order/v1", func(editor *trackers.NameEditor) error {
					return editor.MoveBefore(api.NameRoleDVDSize, api.NameRoleDVDSystem)
				})
				if !strings.Contains(movedSize, test.size+" PAL") {
					t.Fatalf("size move rendered %q", movedSize)
				}
			}
			if test.sourceVisible {
				withoutSystem := prepareStructuredName(t, subject, "metadata/dvd-system-omit/v1", func(editor *trackers.NameEditor) error {
					return editor.Omit(api.NameRoleDVDSystem)
				})
				if strings.Contains(withoutSystem, "PAL") || !strings.Contains(withoutSystem, "DVD") {
					t.Fatalf("system omission rendered %q", withoutSystem)
				}
			}
		})
	}
}

func TestDerivedDVDManualSourceSpellingIsPreserved(t *testing.T) {
	for _, test := range []struct {
		source string
		size   string
		suffix string
	}{
		{source: "dvd", suffix: "dvd"},
		{source: "pal dvd", suffix: "dvd"},
		{
			source: "Pal Dvd",
			size:   "DVD10",
			suffix: "Dvd",
		},
		{
			source: "nTsC dVd",
			size:   "Other",
			suffix: "dVd",
		},
	} {
		t.Run(test.source, func(t *testing.T) {
			meta, err := NewService(&fakeRepo{}, WithConfig(config.Config{})).deriveMediaFacts(t.Context(), preparationstate.State{
				SourcePath: "Example.Release.2026.DVD",
				DiscType:   "DVD",
				Type:       "DISC",
				Source:     "DVD",
				Release: api.ReleaseInfo{
					Category: "MOVIE",
					Title:    "Example Release",
					Year:     2026,
					Type:     "DISC",
					Source:   "DVD",
					Size:     test.size,
				},
				ReleaseNameOverrides: api.ReleaseNameOverrides{Source: new(test.source)},
			})
			if err != nil {
				t.Fatal(err)
			}
			want := strings.TrimSpace("Example Release 2026 " + test.source + " " + test.size)
			if meta.ReleaseName != want {
				t.Fatalf("manual source name=%q, want %q", meta.ReleaseName, want)
			}
			assertReleaseNameComponent(t, meta.GeneratedName, api.NameRoleSource, test.suffix, true)
			prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "TEST", Meta: trackerLookupSubject(meta)},
				trackers.StructuredReleaseNamePolicy("metadata/dvd-spelling/v1", trackers.StructuredNamePolicy{
					Authority: []trackers.NameAuthority{{Role: api.NameRoleSource, Aspect: trackers.NamePresence}, {Role: api.NameRoleDVDSystem, Aspect: trackers.NamePresence}},
					Mandatory: func(editor *trackers.NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
						if err := editor.Include(api.NameRoleSource); err != nil {
							return fmt.Errorf("include source: %w", err)
						}
						return editor.Omit(api.NameRoleDVDSystem)
					},
				}),
			)
			want = strings.TrimSpace("Example Release 2026 " + test.suffix + " " + test.size)
			if failure != nil || prepared.Projection.UploadReleaseName != want {
				t.Fatalf("independent system omission failure=%v name=%q want=%q", failure, prepared.Projection.UploadReleaseName, want)
			}
		})
	}
}

func TestBuildReleaseNameDVLStylePolicyKeepsCodecAndLanguageSemantic(t *testing.T) {
	media := mediaInfoDoc{}
	media.Media.Track = []map[string]any{{
		"@type":                "Video",
		"Format":               "MPEG-4 Visual",
		"Encoded_Library_Name": "ffmpeg",
	}}
	videoEncode, videoCodec, _, _ := videoEncodeFromMedia(media, "ENCODE")
	result := BuildReleaseName(api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "ENCODE",
		Title:       "FRENCH Signal",
		Year:        2026,
		Edition:     "FRENCH Cut",
		Repack:      "FRENCH REPACK",
		Resolution:  "576p",
		Source:      "DVD",
		Audio:       "FRENCH AC3 2.0",
		VideoCodec:  videoCodec,
		VideoEncode: videoEncode,
	}, api.NopLogger{})
	subject := trackerLookupSubject(preparationstate.State{
		Type:       "ENCODE",
		Source:     "DVD",
		VideoCodec: videoCodec,
		Release: api.ReleaseInfo{
			Category: "MOVIE",
			Title:    "FRENCH Signal",
			Year:     2026,
			Type:     "ENCODE",
			Source:   "DVD",
		},
		ReleaseName:      result.Name,
		ReleaseNameNoTag: result.NameNoTag,
		GeneratedName:    result.GeneratedName,
	})
	name := prepareStructuredName(t, subject, "dvl/semantic-collision/v1", func(editor *trackers.NameEditor) error {
		if err := editor.InsertBefore(api.NameRoleLanguageMarker, "FRENCH", api.NameRoleResolution); err != nil {
			return fmt.Errorf("insert language marker: %w", err)
		}
		if err := editor.MoveBefore(api.NameRoleVideoCodec, api.NameRoleAudio); err != nil {
			return fmt.Errorf("move codec: %w", err)
		}
		return editor.Omit(api.NameRoleEdition)
	})
	if strings.Count(name, "FRENCH") != 4 || !strings.Contains(name, "FRENCH 576p") || !strings.Contains(name, "FRENCH REPACK") ||
		!strings.Contains(name, "FRENCH AC3 2.0") ||
		strings.Count(name, videoCodec) != 1 || strings.Index(name, videoCodec) > strings.Index(name, "FRENCH AC3 2.0") {
		t.Fatalf("DVL-style semantic policy rendered %q", name)
	}
}

func prepareStructuredName(
	t *testing.T,
	subject api.UploadSubject,
	id string,
	operation func(*trackers.NameEditor) error,
) string {
	t.Helper()
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "TEST", Meta: subject},
		trackers.StructuredReleaseNamePolicy(id, trackers.StructuredNamePolicy{
			Defaults: func(editor *trackers.NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
				return operation(editor)
			},
		}),
	)
	if failure != nil {
		t.Fatalf("prepare structured name: %v", failure)
	}
	return prepared.Projection.UploadReleaseName
}

func assertReleaseNameComponent(t *testing.T, document *api.ReleaseNameDocument, role api.ReleaseNameRole, want string, present bool) {
	t.Helper()
	component, exists := document.Component(role)
	if !exists || component.Value != want || component.AvailableValue != want || component.Present != present {
		t.Fatalf("component %q = %#v, exists=%t; want value=%q present=%t", role, component, exists, want, present)
	}
}
