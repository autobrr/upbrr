// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestMapPreparationRequestPreservesSingleSourceIntent(t *testing.T) {
	t.Parallel()

	tmdbID := 123456
	category := "TV"
	releaseType := "episode"
	tag := "GRP"
	distributor := "Example Distributor"
	personalRelease := false
	client := "qbit"
	forceRecheck := false
	request := Request{
		SourcePath: "  Example.Release.2026.mkv  ",
		Options: UploadOptions{
			KeepFolder:      true,
			KeepImages:      true,
			OnlyID:          true,
			SkipAutoTorrent: true,
			InteractionMode: InteractionModeInteractive,
		},
		ExternalIDOverrides: ExternalIDOverrides{TMDBID: &tmdbID},
		ReleaseNameOverrides: ReleaseNameOverrides{
			Category: &category,
			Type:     &releaseType,
			Tag:      &tag,
		},
		MetadataOverrides: MetadataOverrides{
			Distributor:     &distributor,
			PersonalRelease: &personalRelease,
		},
		SourceLookupURL:    "  https://example.invalid/source  ",
		TrackerIDOverrides: map[string]string{"btn": "123"},
		PlaylistInstruction: PlaylistInstruction{
			Set:      true,
			Selected: []string{},
			UseAll:   false,
		},
		ClientOverrides: ClientOverrides{
			Client:       &client,
			ForceRecheck: &forceRecheck,
		},
		ConfirmBDMVRescan: true,
	}

	input, err := MapPreparationRequest(request, PreparationIntentUpload)
	if err != nil {
		t.Fatalf("map preparation request: %v", err)
	}
	canonicalCategory := CanonicalCategoryTV
	expected := PrepareInput{
		SourcePath: "Example.Release.2026.mkv",
		Intent:     PreparationIntentUpload,
		Instructions: ReleaseFactInstructions{
			Identity: ExternalIDOverrides{TMDBID: new(123456)},
			Category: &canonicalCategory,
			ReleaseName: ReleaseNameOverrides{
				Category: new("TV"),
				Type:     new("episode"),
				Tag:      new("GRP"),
			},
			Metadata:     MetadataOverrides{Distributor: new("Example Distributor"), PersonalRelease: new(false)},
			SourceLookup: "https://example.invalid/source",
			Playlist: PlaylistInstruction{
				Set:      true,
				Selected: nil,
				UseAll:   false,
			},
			TrackerIDs: map[string]string{"btn": "123"},
		},
		Policy: PreparationPolicy{
			KeepFolder: true,
			KeepImages: true,
			OnlyID:     true,
		},
		Search: ClientSearchPolicy{Skip: true, Client: new("qbit")},
		Controls: PreparationControls{
			Interaction:       InteractionModeInteractive,
			ConfirmBDMVRescan: true,
			ForceRecheck:      new(false),
		},
	}
	if !reflect.DeepEqual(input, expected) {
		t.Fatalf("mapped input = %#v, want %#v", input, expected)
	}

	*request.ExternalIDOverrides.TMDBID = 999999
	*request.ReleaseNameOverrides.Tag = "CHANGED"
	*request.MetadataOverrides.Distributor = "CHANGED"
	*request.ClientOverrides.Client = "changed"
	request.TrackerIDOverrides["btn"] = "changed"
	request.PlaylistInstruction.Selected = append(request.PlaylistInstruction.Selected, "00001.mpls")
	if !reflect.DeepEqual(input, expected) {
		t.Fatalf("mapped input aliases request state: %#v", input)
	}
}

func TestPrepareInputMappingFieldDispositionIsExplicit(t *testing.T) {
	t.Parallel()

	assertFieldNames(t, reflect.TypeFor[PrepareInput](), []string{"SourcePath", "Intent", "Instructions", "Policy", "Search", "Controls", "Force"})
	assertFieldNames(t, reflect.TypeFor[ReleaseFactInstructions](), []string{
		"Identity", "Category", "ReleaseName", "Metadata", "SourceLookup", "BlurayReleaseID", "Playlist", "TrackerIDs",
	})
	assertFieldNames(t, reflect.TypeFor[PreparationPolicy](), []string{"KeepFolder", "KeepImages", "OnlyID"})
	assertFieldNames(t, reflect.TypeFor[ClientSearchPolicy](), []string{"Skip", "Client"})
	assertFieldNames(t, reflect.TypeFor[PreparationControls](), []string{"Interaction", "ConfirmBDMVRescan", "ForceRecheck"})
}

func TestMapPreparationRequestRejectsBlankSource(t *testing.T) {
	t.Parallel()

	_, err := MapPreparationRequest(Request{SourcePath: "  "}, PreparationIntentPreview)
	if !errors.Is(err, ErrPreparationSourceRequired) {
		t.Fatalf("error = %v, want ErrPreparationSourceRequired", err)
	}
}

func assertFieldNames(t *testing.T, value reflect.Type, want []string) {
	t.Helper()
	got := make([]string, 0, value.NumField())
	for field := range value.Fields() {
		got = append(got, field.Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("%s fields = %v, want %v", value.Name(), got, want)
	}
}
