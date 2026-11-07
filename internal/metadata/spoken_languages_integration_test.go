// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package metadata

import (
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata/mediainfo"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ptp"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSpokenLanguagesSurviveCorrectionsRefreshAndRestart(t *testing.T) {
	fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Movie.2026.1080p.WEB-DL.H264-GRP.mkv")
	fixture.input.Instructions.Category = new(api.CanonicalCategoryMovie)
	fixture.tmdbSpokenLanguages = []string{" FR ", "fra", "de", "und", "mul", "mis", "zxx", "", "not-a-language"}
	fixture.mediaInfo = mediainfo.NewService(nil, &programmeReportAnalyzer{report: `{"media":{"track":[
		{"@type":"General","UniqueID":"1234567890","Format":"Matroska"},
		{"@type":"Video","Format":"AVC","Width":"1920","Height":"1080","FrameRate":"24","ScanType":"Progressive","BitDepth":"8"},
		{"@type":"Audio","ID":"1","StreamOrder":"1","Language":"fr","Format":"AAC","Channels":"2","Default":"Yes"}
	]}}`})
	module, repo := fixture.open(t)
	registry := impl.MustNewRegistry()
	var generation api.PreparedGeneration
	for _, step := range []struct {
		name    string
		want    []string
		patch   *api.ReleaseCorrectionPatch
		refresh bool
		spoken  []string
		legacy  bool
	}{
		{name: "automatic set", want: []string{"de", "fr"}},
		{
			name:   "legacy saved generation",
			want:   []string{"de", "fr"},
			legacy: true,
		},
		{
			name:  "manual",
			want:  []string{"ja"},
			patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{OriginalLanguage: new("ja")}}},
		},
		{
			name:    "manual survives refresh",
			want:    []string{"ja"},
			refresh: true,
			spoken:  []string{"fr"},
		},
		{name: "explicit clear", patch: &api.ReleaseCorrectionPatch{Values: api.ReleaseCorrectionValues{Metadata: api.MetadataOverrides{OriginalLanguage: new("")}}}},
		{
			name:    "clear survives refresh",
			refresh: true,
			spoken:  []string{"de"},
		},
		{
			name:  "Auto restores provider",
			want:  []string{"de"},
			patch: &api.ReleaseCorrectionPatch{ResetFields: []api.CorrectionFieldRef{{Field: api.CorrectionFieldMetadataOriginalLanguage}}},
		},
		{
			name:    "refreshed multilingual set",
			want:    []string{"en", "fr"},
			refresh: true,
			spoken:  []string{"en", "fr"},
		},
		{
			name:    "unusable set falls back",
			want:    []string{"en"},
			refresh: true,
			spoken:  []string{"und", "mul", "mis", "zxx", "custom"},
		},
		{
			name:    "missing set falls back",
			want:    []string{"en"},
			refresh: true,
		},
	} {
		t.Run(step.name, func(t *testing.T) {
			input := fixture.input
			update := api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit}
			if step.legacy {
				stale, err := repo.LoadPreparedRelease(t.Context(), input.SourcePath)
				if err != nil {
					t.Fatal(err)
				}
				stale.Compatibility.ContractVersion = "prepared-release-v81-category-default"
				stale.Identity.Resolution.ContractVersion = "external-identity-v15-category-default"
				stale.ProviderMetadata.TMDB.SpokenLanguages = nil
				stale.Media.TitleLanguages = nil
				stale.Media.LanguageFacts = mediafacts.ResolveLanguages(stale.Media)
				if err := repo.CommitPreparedRelease(t.Context(), stale); err != nil {
					t.Fatal(err)
				}
			}
			if generation != 0 {
				if err := repo.Close(); err != nil {
					t.Fatal(err)
				}
				module, repo = fixture.open(t)
				input = api.PrepareInput{SourcePath: input.SourcePath, ExternalFreshness: api.ExternalFreshnessReuse}
			}
			if step.patch != nil {
				update = api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdatePatch, Patch: step.patch}
			}
			if step.refresh {
				fixture.tmdbSpokenLanguages = step.spoken
				input.ExternalFreshness = api.ExternalFreshnessRefresh
			}
			result := prepareCorrectionEvidence(t, module, input, update)
			if step.legacy && result.Release.Generation <= generation {
				t.Fatal("legacy language semantics reused the old generation")
			}
			if generation == 0 {
				preserveCorrectionGeneration(t, &result.Release)
			}
			generation = result.Release.Generation
			facts := result.Release.Media.LanguageFacts
			keys := languageutil.LanguageMatchKeys(facts.OriginalLanguages)
			slices.Sort(keys)
			if !slices.Equal(keys, step.want) || facts.OriginalLanguagesKnown != (len(step.want) > 0) {
				t.Fatalf("preferred title evidence: want %v, got %v known=%t", step.want, facts.OriginalLanguages, facts.OriginalLanguagesKnown)
			}
			if result.Release.ProviderMetadata.TMDB.OriginalLanguage != "en" {
				t.Fatal("spoken languages replaced the raw original-language snapshot")
			}
			if !slices.Equal(languageutil.LanguageMatchKeys(facts.ProgrammeLanguages), []string{"fr"}) || len(facts.Tracks) != 1 || facts.HasEnglishDub() {
				t.Fatalf("title evidence invented programme tracks or dubbing: %#v", facts)
			}
			ref := api.ReleaseRef{SourcePath: input.SourcePath, Generation: generation}
			subject, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: ref})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(subject.LanguageFacts.OriginalLanguages, facts.OriginalLanguages) {
				t.Fatal("upload subject lost preferred title evidence")
			}
			if len(step.want) == 0 || slices.Contains(step.want, "fr") {
				readiness, err := trackers.EvaluateInputReadiness(registry, []api.TrackerID{"AZ", "PTP"}, subject)
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range readiness.Fields {
					if field.CorrectionField != nil && (*field.CorrectionField == api.CorrectionFieldMetadataTrackLanguages || *field.CorrectionField == api.CorrectionFieldMetadataTrackAudioOrigins) {
						t.Fatalf("matching spoken language or unknown title forced comparison review: %#v", field)
					}
				}
			}
		})
	}
}

func TestSpokenLanguagesKeepMultilingualOriginalName(t *testing.T) {
	fixture := newCorrectionEvidenceFixtureForSource(t, "Example.Movie.2026.1080p.WEB-DL.H264-GRP.mkv")
	fixture.input.Instructions.Category = new(api.CanonicalCategoryMovie)
	fixture.tmdbSpokenLanguages = []string{"en", "fr"}
	fixture.mediaInfo = mediainfo.NewService(nil, &programmeReportAnalyzer{report: `{"media":{"track":[
		{"@type":"General","UniqueID":"1234567890","Format":"Matroska"},
		{"@type":"Video","Format":"AVC","Width":"1920","Height":"1080","FrameRate":"24","ScanType":"Progressive","BitDepth":"8"},
		{"@type":"Audio","ID":"1","StreamOrder":"1","Language":"fr, en","Format":"AAC","Channels":"2","Default":"Yes"}
	]}}`})
	module, _ := fixture.open(t)
	result := prepareCorrectionEvidence(t, module, fixture.input, api.ReleaseCorrectionUpdate{Mode: api.ReleaseCorrectionUpdateInherit})
	subject, err := module.ResolveUploadSubject(t.Context(), api.UploadSubjectInput{Release: api.ReleaseRef{SourcePath: fixture.input.SourcePath, Generation: result.Release.Generation}})
	if err != nil {
		t.Fatal(err)
	}
	if !subject.LanguageFacts.HasOriginalAudio() || subject.LanguageFacts.HasEnglishDub() {
		t.Fatalf("multilingual original classification: %#v", subject.LanguageFacts)
	}
	prepared, failure := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{Tracker: "PTP", Meta: subject}, ptp.Profile().ReleaseNamePolicy)
	if failure != nil {
		t.Fatal(failure)
	}
	for _, name := range []string{subject.ReleaseName, prepared.Projection.UploadReleaseName} {
		if strings.Contains(name, "Dual-Audio") || strings.Contains(name, "Dubbed") {
			t.Fatalf("multilingual original acquired a dub marker: %q", name)
		}
	}
}
