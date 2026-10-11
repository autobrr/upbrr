// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package impl

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUnit3DProviderNamesUseTrackerAuthority(t *testing.T) {
	registry := MustNewRegistry()
	for _, category := range []api.CanonicalCategory{api.CanonicalCategoryMovie, api.CanonicalCategoryTV} {
		for _, tracker := range []string{"LST", "BLU", "AITHER", "HHD", "ULCX", "YUS"} {
			t.Run(string(category)+"/"+tracker, func(t *testing.T) {
				subject := providerNameSubject(category)
				before := providerSubjectSnapshot(t, subject)
				prepared := prepareProviderName(t, registry, tracker, subject, nil)
				wantTitle, wantYear := "TMDB Signal", 2001
				if tracker == "HHD" || tracker == "ULCX" || tracker == "YUS" {
					wantTitle = "IMDb Signal"
				}
				if tracker == "HHD" || tracker == "ULCX" {
					wantYear = 2002
				}
				if category == api.CanonicalCategoryTV {
					wantYear = 1998
				}
				wantPrefix := wantTitle + " " + strconv.Itoa(wantYear) + " "
				if !strings.HasPrefix(prepared.Projection.UploadReleaseName, wantPrefix) {
					t.Fatalf("upload name = %q, want prefix %q", prepared.Projection.UploadReleaseName, wantPrefix)
				}
				if prepared.Projection.DuplicateCriteria.Name != prepared.Projection.UploadReleaseName {
					t.Fatalf("duplicate name = %q, upload = %q", prepared.Projection.DuplicateCriteria.Name, prepared.Projection.UploadReleaseName)
				}
				if !slices.Equal(before, providerSubjectSnapshot(t, subject)) {
					t.Fatal("tracker projection changed canonical naming facts")
				}
			})
		}
	}
}

func TestUnit3DProviderNamesKeepTVDBQualifiers(t *testing.T) {
	registry := MustNewRegistry()
	for _, tracker := range []string{"AITHER", "DP", "HHD", "ULCX", "YUS", "LUME"} {
		for _, includeYear := range []bool{false, true} {
			t.Run(tracker+"/year="+strconv.FormatBool(includeYear), func(t *testing.T) {
				subject := providerNameSubject(api.CanonicalCategoryTV)
				evidence := &subject.ProviderMetadata.TVDB.NameDisambiguation
				evidence.IncludeYear, evidence.IncludeLocale = includeYear, true
				evidence.SeriesYear, evidence.Locale = 2004, "US"
				wantTitle, wantLocale, wantYear := "TMDB Signal", " US", ""
				if tracker == "HHD" || tracker == "ULCX" || tracker == "YUS" {
					wantTitle = "IMDb Signal"
				}
				if tracker == "LUME" {
					wantLocale = ""
				}
				if includeYear && tracker != "ULCX" {
					wantYear = " 1998"
					if tracker == "AITHER" || tracker == "DP" {
						wantYear = " 2004"
					}
				}
				prepared := prepareProviderName(t, registry, tracker, subject, nil)
				wantPrefix := wantTitle + wantLocale + wantYear + " S01E02 "
				if !strings.HasPrefix(prepared.Projection.UploadReleaseName, wantPrefix) ||
					prepared.Projection.DuplicateCriteria.Name != prepared.Projection.UploadReleaseName {
					t.Fatalf("provider title lost site TV qualifiers: upload=%q search=%q want prefix=%q",
						prepared.Projection.UploadReleaseName, prepared.Projection.DuplicateCriteria.Name, wantPrefix)
				}
			})
		}
	}
}

func TestUnit3DProviderNamesRejectUnboundAutomaticTitles(t *testing.T) {
	registry := MustNewRegistry()
	cases := []struct {
		name   string
		change func(*api.UploadSubject)
	}{
		{"missing metadata", func(s *api.UploadSubject) { s.ProviderMetadata.TMDB, s.ProviderMetadata.IMDB = nil, nil }},
		{"blank title", func(s *api.UploadSubject) { s.ProviderMetadata.TMDB.Title, s.ProviderMetadata.IMDB.Title = " ", " " }},
		{"missing identity ID", func(s *api.UploadSubject) { s.Identity.TMDBID, s.Identity.IMDBID = 0, 0 }},
		{"negative identity ID", func(s *api.UploadSubject) { s.Identity.TMDBID, s.Identity.IMDBID = -1, -1 }},
		{"missing metadata ID", func(s *api.UploadSubject) { s.ProviderMetadata.TMDB.TMDBID, s.ProviderMetadata.IMDB.IMDBID = 0, 0 }},
		{"matching zero IDs", func(s *api.UploadSubject) {
			s.Identity.TMDBID, s.ProviderMetadata.TMDB.TMDBID = 0, 0
			s.Identity.IMDBID, s.ProviderMetadata.IMDB.IMDBID = 0, 0
		}},
		{"matching negative IDs", func(s *api.UploadSubject) {
			s.Identity.TMDBID, s.ProviderMetadata.TMDB.TMDBID = -1, -1
			s.Identity.IMDBID, s.ProviderMetadata.IMDB.IMDBID = -1, -1
		}},
		{"different metadata ID", func(s *api.UploadSubject) { s.ProviderMetadata.TMDB.TMDBID++; s.ProviderMetadata.IMDB.IMDBID++ }},
		{"blank source", func(s *api.UploadSubject) { s.SourcePath = " " }},
		{"blank identity source", func(s *api.UploadSubject) { s.Identity.SourcePath = " " }},
		{"blank metadata source", func(s *api.UploadSubject) { s.ProviderMetadata.SourcePath = " " }},
		{"different identity source", func(s *api.UploadSubject) { s.Identity.SourcePath = filepath.Join("media", "Other.mkv") }},
		{"different metadata source", func(s *api.UploadSubject) { s.ProviderMetadata.SourcePath = filepath.Join("media", "Other.mkv") }},
		{"unversioned snapshots", func(s *api.UploadSubject) { s.Identity.Generation, s.ProviderMetadata.Generation = 0, 0 }},
		{"missing metadata generation", func(s *api.UploadSubject) { s.ProviderMetadata.Generation = 0 }},
		{"different generation", func(s *api.UploadSubject) { s.ProviderMetadata.Generation++ }},
	}
	for _, tracker := range []string{"LST", "HHD"} {
		for _, test := range cases {
			t.Run(tracker+"/"+test.name, func(t *testing.T) {
				subject := providerNameSubject(api.CanonicalCategoryTV)
				test.change(&subject)
				projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: tracker, Meta: subject}, "input", "catalog", "config")
				var rule *trackers.NameRuleError
				if failure == nil || !errors.As(failure, &rule) || rule.Role != api.NameRoleTitle {
					t.Fatalf("expected title NameRuleError, got %v", failure)
				}
				if projection.UploadReady || projection.DupeReady || projection.Readiness != api.ReadinessStatusBlocked ||
					projection.UploadReleaseName != "" || projection.DuplicateCriteria.Name != "" {
					t.Fatalf("unbound title remained usable: %+v", projection)
				}
			})
		}
	}
}

func TestUnit3DProviderNamesRejectWrongTMDBCategory(t *testing.T) {
	registry := MustNewRegistry()
	for _, category := range []string{"", "MOVIE"} {
		subject := providerNameSubject(api.CanonicalCategoryTV)
		subject.ProviderMetadata.TMDB.Category = category
		_, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "LST", Meta: subject}, "input", "catalog", "config")
		var rule *trackers.NameRuleError
		if failure == nil || !errors.As(failure, &rule) || rule.Role != api.NameRoleTitle {
			t.Fatalf("TMDB category %q accepted for TV: %v", category, failure)
		}
	}
}

func TestUnit3DProviderNamesPreserveManualAuthority(t *testing.T) {
	registry := MustNewRegistry()
	for _, tracker := range []string{"LST", "BLU", "AITHER", "HHD", "ULCX", "YUS"} {
		for _, authority := range []string{"components", "metadata", "opaque", "edited opaque", "omitted year"} {
			t.Run(tracker+"/"+authority, func(t *testing.T) {
				subject := providerNameSubject(api.CanonicalCategoryMovie)
				switch authority {
				case "components", "omitted year":
					for index := range subject.GeneratedName.Components {
						component := &subject.GeneratedName.Components[index]
						if component.Role == api.NameRoleTitle || component.Role == api.NameRoleYear {
							component.Manual = true
						}
						if authority == "omitted year" && component.Role == api.NameRoleYear {
							component.Present = false
						}
					}
					subject.ReleaseName = subject.GeneratedName.Render().Name
				case "metadata":
					subject.EffectiveMetadata.TitleProvenance = api.FactProvenanceManual
					subject.EffectiveMetadata.YearProvenance = api.FactProvenanceManual
				case "opaque":
					subject.GeneratedName = nil
				case "edited opaque":
					subject.ReleaseName = "Manually Edited 1998-GRP"
				}
				before := providerSubjectSnapshot(t, subject)
				prepared := prepareProviderName(t, registry, tracker, subject, nil)
				if prepared.Projection.UploadReleaseName != subject.ReleaseName || prepared.Projection.DuplicateCriteria.Name != subject.ReleaseName {
					t.Fatalf("manual name changed: upload=%q search=%q want=%q", prepared.Projection.UploadReleaseName, prepared.Projection.DuplicateCriteria.Name, subject.ReleaseName)
				}
				if !slices.Equal(before, providerSubjectSnapshot(t, subject)) {
					t.Fatal("manual component or metadata provenance changed")
				}
				missing := subject
				missing.ProviderMetadata.TMDB, missing.ProviderMetadata.IMDB = nil, nil
				withoutProvider := prepareProviderName(t, registry, tracker, missing, nil)
				if withoutProvider.Projection.UploadReleaseName != subject.ReleaseName {
					t.Fatal("manual name depends on automatic title-provider data")
				}
			})
		}
	}
}

func TestUnit3DProviderNamesKeepManualUploadAndAutomaticSearch(t *testing.T) {
	registry := MustNewRegistry()
	subject := providerNameSubject(api.CanonicalCategoryMovie)
	requested := "My Reviewed Name 1998-GRP"
	for _, tracker := range []string{"LST", "HHD", "YUS"} {
		t.Run(tracker, func(t *testing.T) {
			automatic := prepareProviderName(t, registry, tracker, subject, nil)
			manual := prepareProviderName(t, registry, tracker, subject, &requested)
			if manual.Projection.UploadReleaseName != requested || manual.Projection.DuplicateCriteria.Name != automatic.Projection.DuplicateCriteria.Name {
				t.Fatalf("manual upload or automatic duplicate projection changed: %+v", manual.Projection)
			}
			if !slices.ContainsFunc(manual.Projection.PolicyDecisions, func(decision api.TrackerPolicyDecision) bool {
				return decision.Code == "release_name_instruction" && decision.Decision == "requested" && decision.Message == requested
			}) {
				t.Fatal("requested-name provenance missing")
			}
			missing := subject
			missing.ProviderMetadata.TMDB, missing.ProviderMetadata.IMDB = nil, nil
			descriptor, _ := registry.LookupDescriptor(tracker)
			_, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
				Tracker:             tracker,
				Meta:                missing,
				RequestedUploadName: &requested,
			}, descriptor.ReleaseNamePolicy)
			var rule *trackers.NameRuleError
			if failure == nil || !errors.As(failure, &rule) {
				t.Fatalf("manual upload bypassed required automatic search evidence: %v", failure)
			}
		})
	}
}

func TestUnit3DProviderNamesPreserveSPSourceAndSceneYear(t *testing.T) {
	registry := MustNewRegistry()
	for _, scene := range []bool{false, true} {
		subject := providerNameSubject(api.CanonicalCategoryMovie)
		subject.Scene = scene
		subject.SceneName = "Scene.Signal.1998.1080p.WEB-DL-GRP"
		want := "Source.Signal.1998.1080p.WEB-DL-GRP"
		if scene {
			want = subject.SceneName
		}
		projection, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "SP", Meta: subject}, "input", "catalog", "config")
		if failure != nil {
			t.Fatalf("SP projection: %v", failure)
		}
		if projection.UploadReleaseName != want || projection.DuplicateCriteria.Name != want {
			t.Fatalf("scene=%t names=%q/%q want=%q", scene, projection.UploadReleaseName, projection.DuplicateCriteria.Name, want)
		}
	}
}

func TestUnit3DProviderNamesPreserveSiteTitlePaths(t *testing.T) {
	registry := MustNewRegistry()
	for _, tracker := range []string{"OTW", "RMC", "RHD", "CBR", "LCD"} {
		t.Run(tracker, func(t *testing.T) {
			subject := providerNameSubject(api.CanonicalCategoryMovie)
			want := "TMDB Signal 2001 "
			switch tracker {
			case "RHD":
				subject.ProviderMetadata.TMDB.LocalizedTitles = map[string]string{"de": "Deutsches Signal"}
				want = "Deutsches Signal 2001 "
			case "CBR", "LCD":
				subject.ProviderMetadata.TMDB.OriginalLanguage = "pt"
				subject.GeneratedName.Components = append(subject.GeneratedName.Components, api.ReleaseNameComponent{
					Role:    api.NameRoleAlternateTitle,
					Value:   "AKA Sinal Português",
					Present: true,
					Join:    " ",
				})
				subject.ReleaseName = subject.GeneratedName.Render().Name
				want = "Sinal Português 2001 "
			}
			prepared := prepareProviderName(t, registry, tracker, subject, nil)
			if !strings.HasPrefix(prepared.Projection.UploadReleaseName, want) {
				t.Fatalf("site title path changed: %q, want prefix %q", prepared.Projection.UploadReleaseName, want)
			}
		})
	}
}

func TestUnit3DProviderNamesRetainReviewedProjection(t *testing.T) {
	registry := MustNewRegistry()
	subject := providerNameSubject(api.CanonicalCategoryMovie)
	before := providerSubjectSnapshot(t, subject)
	first := prepareProviderName(t, registry, "LST", subject, nil)
	_ = prepareProviderName(t, registry, "HHD", subject, nil)
	last := prepareProviderName(t, registry, "LST", subject, nil)
	if first.Projection.UploadReleaseName != last.Projection.UploadReleaseName || !slices.Equal(before, providerSubjectSnapshot(t, subject)) {
		t.Fatal("one tracker changed another tracker's projection or canonical provider data")
	}
	data, err := json.Marshal(first.Projection)
	if err != nil {
		t.Fatal(err)
	}
	var retained api.TrackerReleaseProjection
	if err := json.Unmarshal(data, &retained); err != nil {
		t.Fatal(err)
	}
	descriptor, _ := registry.LookupDescriptor("LST")
	first.Projection = &retained
	reviewed, failure := trackers.PrepareInputWithReleaseNamePolicy(first, descriptor.ReleaseNamePolicy)
	if failure != nil {
		t.Fatalf("retained projection: %v", failure)
	}
	payloadName, err := reviewed.ReviewedUploadName()
	if err != nil || payloadName != retained.DuplicateCriteria.Name {
		t.Fatalf("reviewed payload name=%q search=%q err=%v", payloadName, retained.DuplicateCriteria.Name, err)
	}
	projected, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "LST", Meta: subject}, "input", "catalog", "config")
	if failure != nil {
		t.Fatal(failure)
	}
	subject.ProviderMetadata.TMDB.Title = "Updated TMDB Signal"
	updated, failure := registry.ProjectRelease(t.Context(), trackers.PreparationInput{Tracker: "LST", Meta: subject}, "input", "catalog", "config")
	if failure != nil {
		t.Fatal(failure)
	}
	if projected.ProjectorFingerprint == updated.ProjectorFingerprint || projected.DuplicateTargetFingerprint == updated.DuplicateTargetFingerprint {
		t.Fatal("provider title change did not invalidate naming and duplicate fingerprints")
	}
	if _, failure := trackers.PrepareInputWithReleaseNamePolicy(first, descriptor.ReleaseNamePolicy); failure == nil || failure.Code() != "name_projection_mismatch" {
		t.Fatalf("stale reviewed name remained submittable: %v", failure)
	}
}

func providerSubjectSnapshot(t *testing.T, subject api.UploadSubject) []byte {
	t.Helper()
	data, err := json.Marshal(subject)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func prepareProviderName(t *testing.T, registry *trackers.Registry, tracker string, subject api.UploadSubject, requested *string) trackers.PreparationInput {
	t.Helper()
	descriptor, ok := registry.LookupDescriptor(tracker)
	if !ok {
		t.Fatalf("descriptor missing for %s", tracker)
	}
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             tracker,
		Meta:                subject,
		RequestedUploadName: requested,
	}, descriptor.ReleaseNamePolicy)
	if failure != nil {
		t.Fatalf("prepare %s: %v: %v", tracker, failure, failure.Unwrap())
	}
	return prepared
}

// providerNameSubject models one exact prepared generation with conflicting provider titles and years.
func providerNameSubject(category api.CanonicalCategory) api.UploadSubject {
	source := filepath.Join("media", "Source.Signal.1998.1080p.WEB-DL-GRP.mkv")
	document := &api.ReleaseNameDocument{
		Version: api.ReleaseNameDocumentVersionV1,
		Components: []api.ReleaseNameComponent{
			{
				Role:    api.NameRoleTitle,
				Value:   "TVDB Signal",
				Present: true,
			},
			{
				Role:           api.NameRoleYear,
				Value:          "1998",
				AvailableValue: "1998",
				Present:        true,
				Join:           " ",
			},
			{
				Role:    api.NameRoleSeason,
				Value:   "S01",
				Present: category == api.CanonicalCategoryTV,
				Join:    " ",
			},
			{
				Role:     api.NameRoleEpisode,
				Value:    "E02",
				Present:  category == api.CanonicalCategoryTV,
				Join:     " ",
				AttachTo: []api.ReleaseNameRole{api.NameRoleSeason},
			},
			{
				Role:    api.NameRoleResolution,
				Value:   "1080p",
				Present: true,
				Join:    " ",
			},
			{
				Role:    api.NameRoleVideoFormat,
				Value:   "WEB-DL",
				Present: true,
				Join:    " ",
			},
			{
				Role:    api.NameRoleAudio,
				Value:   "AAC 2.0",
				Present: true,
				Join:    " ",
			},
			{
				Role:    api.NameRoleVideoCodec,
				Value:   "H.264",
				Present: true,
				Join:    " ",
			},
			{
				Role:    api.NameRoleGroup,
				Value:   "-GRP",
				Present: true,
			},
		},
	}
	rendered := document.Render()
	return api.UploadSubject{
		SourcePath:       source,
		Filename:         filepath.Base(source),
		VideoPath:        source,
		GeneratedName:    document,
		ReleaseName:      rendered.Name,
		ReleaseNameNoTag: rendered.NameNoTag,
		ReleaseNameClean: rendered.CleanName,
		Type:             "WEBDL",
		Source:           "WEB",
		Container:        "mkv",
		VideoCodec:       "H.264",
		Audio:            "AAC 2.0",
		Tag:              "-GRP",
		Release: api.ReleaseInfo{
			Title:      "TVDB Signal",
			Year:       1998,
			Category:   string(category),
			Resolution: "1080p",
			Type:       "WEBDL",
		},
		Identity: api.ExternalIdentity{
			SourcePath: source,
			Generation: 3,
			Category:   category,
			TMDBID:     12345,
			IMDBID:     1234567,
			TVDBID:     54321,
		},
		EffectiveMetadata: api.EffectiveMetadata{Title: "TVDB Signal", Year: 1998},
		LanguageFacts: api.LanguageFacts{Tracks: []api.MediaTrackFacts{{
			Kind:         api.MediaTrackAudio,
			Role:         api.AudioRoleProgramme,
			Default:      true,
			DefaultKnown: true,
			Codec:        "AAC",
			AudioLabel:   "AAC 2.0",
		}}},
		ProviderMetadata: api.SourceScopedMetadata{
			SourcePath: source,
			Generation: 3,
			TMDB: &api.TMDBMetadata{
				TMDBID:           12345,
				Category:         string(category),
				Title:            "TMDB Signal",
				Year:             2001,
				OriginalLanguage: "en",
			},
			IMDB: &api.IMDBMetadata{
				IMDBID: 1234567,
				Title:  "IMDb Signal",
				Year:   2002,
			},
			TVDB: &api.TVDBMetadata{
				TVDBID: 54321,
				Name:   "TVDB Signal",
				Year:   1998,
				NameDisambiguation: api.TVDBNameDisambiguation{
					CanonicalName: "TVDB Signal",
					IncludeYear:   true,
					SeriesYear:    1998,
					Status:        api.MetadataEvidenceStatusComplete,
					Source:        "tvdb-name/v1",
				},
			},
		},
	}
}

func TestUnit3DProviderNamesOmitRedundantAutomaticAlias(t *testing.T) {
	registry := MustNewRegistry()
	for _, tracker := range []string{"HHD", "ULCX", "YUS", "LST"} {
		for _, category := range []api.CanonicalCategory{api.CanonicalCategoryMovie, api.CanonicalCategoryTV} {
			for _, manual := range []string{"automatic", "component", "metadata"} {
				t.Run(tracker+"/"+string(category)+"/"+manual, func(t *testing.T) {
					subject := providerNameSubject(category)
					title := subject.ProviderMetadata.IMDB.Title
					if tracker == "LST" {
						title = subject.ProviderMetadata.TMDB.Title
					}
					// IMDb supplies AKA=Title when no distinct original title exists. An
					// automatic canonical name using another provider can retain that AKA.
					subject.ProviderMetadata.IMDB.AKA = title
					subject.GeneratedName.Components = append(subject.GeneratedName.Components, api.ReleaseNameComponent{
						Role:    api.NameRoleAlternateTitle,
						Value:   "AKA " + title,
						Present: true,
						Join:    " ",
						Manual:  manual == "component",
					})
					if manual == "metadata" {
						subject.EffectiveMetadata.AlternateTitleProvenance = api.FactProvenanceManual
					}
					subject.ReleaseName = subject.GeneratedName.Render().Name
					before := providerSubjectSnapshot(t, subject)
					prepared := prepareProviderName(t, registry, tracker, subject, nil)
					hasAlias := strings.Contains(prepared.Projection.UploadReleaseName, "AKA "+title)
					if hasAlias != (manual != "automatic") {
						t.Fatalf("redundant alias authority lost: %q", prepared.Projection.UploadReleaseName)
					}
					if !slices.Equal(before, providerSubjectSnapshot(t, subject)) {
						t.Fatal("alias reconciliation changed canonical facts")
					}
				})
			}
		}
	}
}
