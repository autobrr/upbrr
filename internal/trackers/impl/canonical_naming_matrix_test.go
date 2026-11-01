// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

// This matrix exercises finalized components at the registry boundary. Detection,
// corrections and persistence belong to their producer tests.
func TestRegistryCanonicalNamingComponents(t *testing.T) {
	registry := MustNewRegistry()
	cases := []struct {
		role    api.ReleaseNameRole
		value   string
		setFact func(*api.UploadSubject, string)
	}{
		{api.NameRoleCut, "Unrated", func(s *api.UploadSubject, value string) { s.Cut = value }},
		{api.NameRoleEdition, "Collector's", func(s *api.UploadSubject, value string) { s.Edition = value }},
		{api.NameRolePresentation, "Open Matte", func(s *api.UploadSubject, value string) { s.Presentation = value }},
		{api.NameRoleEditionSet, "2in1", func(s *api.UploadSubject, value string) { s.EditionSet = value }},
		{api.NameRoleEditionDisplay, "Remastered Collector's", func(s *api.UploadSubject, _ string) {
			s.Edition = "Collector's"
			s.EditionDisplay = []api.EditionDisplayPart{{Text: "Remastered", Feature: api.ReleaseFeatureRemaster}, {Text: "Collector's"}}
			s.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureRemaster}
		}},
		{api.NameRoleHybrid, "Hybrid", func(s *api.UploadSubject, _ string) {
			s.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid}
		}},
		{api.NameRoleRepack, "REPACK2", func(s *api.UploadSubject, value string) { s.Repack = value }},
		{api.NameRoleThreeD, "3D", func(s *api.UploadSubject, value string) {
			s.Is3D = value
			s.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeature3DHalfSBS}
		}},
	}
	for _, tracker := range registry.Names() {
		for _, tc := range cases {
			t.Run(tracker+"/"+string(tc.role), func(t *testing.T) {
				subject := providerNameSubject(api.CanonicalCategoryMovie)
				subject.ProviderMetadata.IMDB.AKA = "IMDb Signal"
				position := 2
				if tc.role == api.NameRoleThreeD {
					position = 5 // After resolution, immediately before the WEB-DL source/format.
				}
				subject.GeneratedName.Components = slices.Insert(subject.GeneratedName.Components, position, api.ReleaseNameComponent{
					Role:           tc.role,
					Value:          tc.value,
					AvailableValue: tc.value,
					Present:        true,
					Join:           " ",
				})
				tc.setFact(&subject, tc.value)
				if tc.role == api.NameRoleEditionDisplay {
					subject.GeneratedName.Components = slices.Insert(subject.GeneratedName.Components, 2, api.ReleaseNameComponent{
						Role:           api.NameRoleEdition,
						Value:          subject.Edition,
						AvailableValue: subject.Edition,
						Join:           " ",
					})
				}
				rendered := subject.GeneratedName.Render()
				subject.ReleaseName, subject.ReleaseNameNoTag = rendered.Name, rendered.NameNoTag
				subject.GeneratedReleaseNames.IncludeEpisodeTitle = rendered
				subject.GeneratedReleaseNames.OmitEpisodeTitle = rendered
				before := providerSubjectSnapshot(t, subject)
				projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: tracker, Meta: subject}, "input", "catalog", "config")
				if failure != nil {
					t.Fatal(failure)
				}
				normalized := strings.ToLower(strings.ReplaceAll(projection.UploadReleaseName, ".", " "))
				expected := strings.ToLower(tc.value)
				// These policies remove punctuation from automatic components.
				if tracker == "BTN" || tracker == "DC" || tracker == "RMC" {
					expected = strings.ReplaceAll(expected, "'", "")
				}
				retained := strings.Contains(normalized, expected)
				want := true
				switch tracker {
				case "AR", "SP", "ASC", "TVC":
					// Source-preserving and restricted display-title contracts do not append components.
					want = false
				case "HDB":
					want = tc.role == api.NameRoleRepack || tc.role == api.NameRoleThreeD
				case "AITHER", "HHD", "YUS", "OTW":
					// Generic automatic editions remain omitted; manual components are independent.
					want = tc.role != api.NameRoleEdition && tc.role != api.NameRoleEditionDisplay
				case "LUME", "ULCX":
					want = tc.role != api.NameRoleEdition && tc.role != api.NameRoleEditionDisplay && tc.role != api.NameRoleHybrid
				}
				if retained != want {
					t.Errorf("component %s retention=%t, want %t in %q", tc.role, retained, want, projection.UploadReleaseName)
				}
				if tc.role == api.NameRoleEditionDisplay {
					wantCount := 0
					if want {
						wantCount = 1
					}
					for part := range strings.FieldsSeq(expected) {
						if count := strings.Count(normalized, part); count != wantCount {
							t.Errorf("display fragment %q count=%d, want %d in %q", part, count, wantCount, projection.UploadReleaseName)
						}
					}
				}
				if !slices.Equal(before, providerSubjectSnapshot(t, subject)) {
					t.Fatal("projection mutated canonical facts")
				}
				repeated, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: tracker, Meta: subject}, "input", "catalog", "config")
				if failure != nil || repeated.UploadReleaseName != projection.UploadReleaseName || repeated.DuplicateCriteria.Name != projection.DuplicateCriteria.Name {
					t.Fatalf("repeated projection changed names: %+v, %v", repeated, failure)
				}
			})
		}
	}
}

// Exact upload/search pairs cover shared, passthrough and reconstructed naming
// with an independent release version and retained stereoscopic subtype facts.
func TestRegistryCanonicalThreeDNames(t *testing.T) {
	registry := MustNewRegistry()
	for _, tc := range []struct {
		tracker string
		want    string
	}{
		{"BLU", "TMDB Signal 2001 REPACK2 1080p 3D WEB-DL AAC 2.0 H.264-GRP"},
		{"AITHER", "TMDB Signal 2001 REPACK2 1080p 3D WEB-DL AAC 2.0 H.264-GRP"},
		{"OTW", "TMDB Signal 2001 REPACK2 1080p 3D WEB-DL AAC 2.0 H.264-GRP"},
		{"HDB", "IMDb Signal 2002 REPACK2 1080p 3D WEB-DL AAC 2.0 H.264-GRP"},
		{"RHD", "TMDB Signal 2001 ENGLISH REPACK2 1080p 3D WEB-DL AAC 2.0 H.264-GRP"},
		{"ANT", "TVDB Signal 2001 REPACK2 1080p 3D WEB-DL AAC 2.0 H.264-GRP"},
	} {
		for _, authority := range []string{"automatic", "manual include", "manual omission"} {
			t.Run(tc.tracker+"/"+authority, func(t *testing.T) {
				subject := providerNameSubject(api.CanonicalCategoryMovie)
				subject.AudioLanguages = []string{"English"}
				subject.ProviderMetadata.IMDB.AKA = "IMDb Signal"
				subject.Repack, subject.Is3D = "REPACK2", "3D"
				subject.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeature3DHalfSBS}
				subject.GeneratedName.Components = slices.Insert(subject.GeneratedName.Components, 2, api.ReleaseNameComponent{
					Role:           api.NameRoleRepack,
					Value:          "REPACK2",
					AvailableValue: "REPACK2",
					Present:        true,
					Join:           " ",
				})
				subject.GeneratedName.Components = slices.Insert(subject.GeneratedName.Components, 6, api.ReleaseNameComponent{
					Role:           api.NameRoleThreeD,
					Value:          "3D",
					AvailableValue: "3D",
					Present:        authority != "manual omission",
					Manual:         authority != "automatic",
					Join:           " ",
				})
				rendered := subject.GeneratedName.Render()
				subject.ReleaseName, subject.ReleaseNameNoTag = rendered.Name, rendered.NameNoTag
				subject.GeneratedReleaseNames.IncludeEpisodeTitle, subject.GeneratedReleaseNames.OmitEpisodeTitle = rendered, rendered
				before := providerSubjectSnapshot(t, subject)
				want := tc.want
				if authority == "manual omission" {
					want = strings.Replace(want, " 3D", "", 1)
				}
				prepared := prepareProviderName(t, registry, tc.tracker, subject, nil)
				data, err := json.Marshal(prepared.Projection)
				if err != nil {
					t.Fatal(err)
				}
				var retained api.TrackerReleaseProjection
				if err := json.Unmarshal(data, &retained); err != nil {
					t.Fatal(err)
				}
				prepared.Projection = &retained
				descriptor, _ := registry.LookupDescriptor(tc.tracker)
				reloaded, failure := trackers.PrepareInputWithReleaseNamePolicy(prepared, descriptor.ReleaseNamePolicy)
				if failure != nil {
					t.Fatal(failure)
				}
				name, err := reloaded.ReviewedUploadName()
				if err != nil || name != want || reloaded.Projection.DuplicateCriteria.Name != want {
					t.Fatalf("retained upload/search=%q/%q, want %q: %v", name, reloaded.Projection.DuplicateCriteria.Name, want, err)
				}
				// RHD's legacy callback has no exact complete-name override contract.
				if tc.tracker != "RHD" {
					requested := "Exact Manual 3D Half SBS Name"
					reviewed := prepareProviderName(t, registry, tc.tracker, subject, &requested)
					if reviewed.Projection.UploadReleaseName != requested || reviewed.Projection.DuplicateCriteria.Name != want {
						t.Fatalf("requested upload/search=%q/%q, want %q/%q", reviewed.Projection.UploadReleaseName, reviewed.Projection.DuplicateCriteria.Name, requested, want)
					}
				}
				if !slices.Equal(before, providerSubjectSnapshot(t, subject)) {
					t.Fatal("3D naming or retained review changed canonical facts")
				}
			})
		}
	}
}

// Explicit structured edits must win over automatic omissions without changing
// the canonical document. Complete-name overrides retain the automatic search projection.
func TestRegistryCanonicalNamingManualAuthority(t *testing.T) {
	registry := MustNewRegistry()
	components := []struct {
		role  api.ReleaseNameRole
		value string
	}{
		{api.NameRoleCut, "Unrated"},
		{api.NameRoleEdition, "Collector's"},
		{api.NameRolePresentation, "Open Matte"},
		{api.NameRoleHybrid, "Hybrid"},
		{api.NameRoleRepack, "REPACK2"},
		{api.NameRoleThreeD, "3D"},
	}
	for _, tracker := range registry.Names() {
		descriptor, _ := registry.LookupDescriptor(tracker)
		if descriptor.ReleaseNamePolicy.Structured == nil {
			continue
		}
		for _, omitted := range []bool{false, true} {
			t.Run(tracker+"/omitted="+strconv.FormatBool(omitted), func(t *testing.T) {
				subject := providerNameSubject(api.CanonicalCategoryMovie)
				const title = "Unrated Remastered Open Matte Hybrid REPACK2 3D Tales"
				subject.GeneratedName.Components[0].Value = title
				subject.Release.Title, subject.EffectiveMetadata.Title = title, title
				subject.ProviderMetadata.IMDB.Title, subject.ProviderMetadata.IMDB.AKA = title, title
				subject.ProviderMetadata.TMDB.Title = title
				for index, component := range components {
					position := 2 + index
					if component.role == api.NameRoleThreeD {
						position = slices.IndexFunc(subject.GeneratedName.Components, func(part api.ReleaseNameComponent) bool {
							return part.Role == api.NameRoleVideoFormat
						})
					}
					subject.GeneratedName.Components = slices.Insert(subject.GeneratedName.Components, position, api.ReleaseNameComponent{
						Role:           component.role,
						Value:          component.value,
						AvailableValue: component.value,
						Present:        !omitted,
						Manual:         true,
						Join:           " ",
					})
				}
				subject.Cut, subject.Edition, subject.Presentation, subject.Repack = "Unrated", "Collector's", "Open Matte", "REPACK2"
				subject.Is3D = "3D"
				subject.ReleaseFeatures = []api.ReleaseFeature{api.ReleaseFeatureHybrid, api.ReleaseFeature3DHalfSBS}
				rendered := subject.GeneratedName.Render()
				subject.ReleaseName, subject.ReleaseNameNoTag = rendered.Name, rendered.NameNoTag
				subject.GeneratedReleaseNames.IncludeEpisodeTitle, subject.GeneratedReleaseNames.OmitEpisodeTitle = rendered, rendered
				before := providerSubjectSnapshot(t, subject)
				prepared := prepareProviderName(t, registry, tracker, subject, nil)
				normalized := strings.ToLower(strings.ReplaceAll(prepared.Projection.UploadReleaseName, ".", " "))
				if !strings.HasPrefix(normalized, strings.ToLower(title)) {
					t.Fatalf("component edits changed title words in %q", prepared.Projection.UploadReleaseName)
				}
				normalized = strings.TrimPrefix(normalized, strings.ToLower(title))
				previous := -1
				for _, component := range components {
					index := strings.Index(normalized, strings.ToLower(component.value))
					if (index >= 0) == omitted {
						t.Errorf("manual component %q omission=%t in %q", component.value, omitted, normalized)
					}
					if !omitted && index <= previous {
						t.Errorf("manual component order changed in %q", normalized)
					}
					previous = index
				}
				requested := "Manual Hybrid REPACK2 3D Complete Name"
				reviewed := prepareProviderName(t, registry, tracker, subject, &requested)
				if reviewed.Projection.UploadReleaseName != requested {
					t.Errorf("complete name changed to %q", reviewed.Projection.UploadReleaseName)
				}
				if reviewed.Projection.DuplicateCriteria.Name != prepared.Projection.DuplicateCriteria.Name {
					t.Error("complete-name edit changed automatic search contract")
				}
				if !slices.Equal(before, providerSubjectSnapshot(t, subject)) {
					t.Fatal("manual projection mutated canonical facts")
				}
			})
		}
	}
}
