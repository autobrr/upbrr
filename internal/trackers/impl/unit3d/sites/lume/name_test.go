package lume

import (
	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestLumeLanguageMarkersCountKnownProgrammeLanguages(t *testing.T) {
	for _, test := range []struct {
		name      string
		languages []string
		wantAudio string
	}{
		{
			name:      "unknown language is not a second language",
			languages: []string{"Japanese", "Unknown language"},
			wantAudio: "DD 2.0",
		},
		{
			name:      "undetermined language is not a second language",
			languages: []string{"Japanese", "und"},
			wantAudio: "DD 2.0",
		},
		{
			name:      "aggregate label is not a second language",
			languages: []string{"Japanese", "mul"},
			wantAudio: "DD 2.0",
		},
		{
			name:      "original and English remain dual with unknown language",
			languages: []string{"Japanese", "English", "Unknown language"},
			wantAudio: "DD 2.0 Dual-Audio",
		},
		{
			name:      "aliases do not inflate the language count",
			languages: []string{"Japanese", "jpn", "English", "eng"},
			wantAudio: "DD 2.0 Dual-Audio",
		},
		{
			name:      "unknown language leaves non-English composition unresolved",
			languages: []string{"Japanese", "German", "Unknown language"},
			wantAudio: "DD 2.0",
		},
		{
			name:      "two established non-English languages remain multi",
			languages: []string{"Japanese", "German"},
			wantAudio: "Multi DD 2.0",
		},
		{
			name:      "three established languages remain multi",
			languages: []string{"Japanese", "English", "German"},
			wantAudio: "Multi DD 2.0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			media := api.MediaFacts{
				OriginalLanguage:      "Japanese",
				TrackCoverageComplete: true,
			}
			for _, language := range test.languages {
				media.Tracks = append(media.Tracks, api.MediaTrackFacts{
					Kind:      api.MediaTrackAudio,
					Role:      api.AudioRoleProgramme,
					Languages: []string{language},
				})
			}
			subject := lumeSubject(t, api.ReleaseNameRequest{
				Category: "MOVIE",
				Type:     "WEBDL",
				Title:    "Example",
				Year:     2026,
				Audio:    "DD 2.0",
				Tag:      "-GRP",
			})
			subject.LanguageFacts = mediafacts.ResolveLanguages(media)
			if got, want := lumeName(t, subject, nil), "Example 2026 WEB-DL "+test.wantAudio+"-GRP"; got != want {
				t.Fatalf("programme name = %q, want %q", got, want)
			}
		})
	}
}

func TestLumeLanguageMarkersPreserveManualAndDiscNames(t *testing.T) {
	for _, test := range []struct {
		name        string
		manual      bool
		releaseType string
		discType    string
	}{
		{
			name:        "manual marker",
			manual:      true,
			releaseType: "WEBDL",
		},
		{
			name:        "full disc",
			releaseType: "DISC",
			discType:    "BDMV",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			subject := lumeSubject(t, api.ReleaseNameRequest{
				Category: "MOVIE",
				Type:     test.releaseType,
				DiscType: test.discType,
				Title:    "Example",
				Year:     2026,
				Audio:    "Dual-Audio DD 2.0",
				Tag:      "-GRP",
			})
			subject.LanguageFacts = mediafacts.ResolveLanguages(api.MediaFacts{
				OriginalLanguage: "Japanese",
				AudioLanguages:   []string{"Japanese", "Unknown language"},
			})
			for index := range subject.GeneratedName.Components {
				if subject.GeneratedName.Components[index].Role == api.NameRoleDualAudio {
					subject.GeneratedName.Components[index].Manual = test.manual
				}
			}
			if got := lumeName(t, subject, nil); got != subject.ReleaseName {
				t.Fatalf("preserved name = %q, want %q", got, subject.ReleaseName)
			}
		})
	}
}

func TestLumeMultiNameRequiresConsistentProgrammeFacts(t *testing.T) {
	media := api.MediaFacts{
		OriginalLanguage:         "Japanese",
		TrackCoverageComplete:    true,
		AudioLanguages:           []string{"Japanese", "German"},
		AudioLanguagesProvenance: api.FactProvenanceManual,
		Tracks: []api.MediaTrackFacts{{
			Kind:      api.MediaTrackAudio,
			Role:      api.AudioRoleProgramme,
			Languages: []string{"Japanese"},
		}},
	}
	subject := lumeSubject(t, api.ReleaseNameRequest{
		Category: "MOVIE",
		Type:     "WEBDL",
		Title:    "Example",
		Year:     2026,
		Audio:    "DD 2.0",
		Tag:      "-GRP",
	})
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	if subject.LanguageFacts.ProgrammeStatus != api.MetadataEvidenceStatusContradictory {
		t.Fatal("fixture must retain the aggregate/track contradiction")
	}
	if got, want := lumeName(t, subject, nil), "Example 2026 WEB-DL DD 2.0-GRP"; got != want {
		t.Fatalf("contradictory programme name = %q, want %q", got, want)
	}
	media.Tracks = append(media.Tracks, api.MediaTrackFacts{
		Kind:      api.MediaTrackAudio,
		Role:      api.AudioRoleProgramme,
		Languages: []string{"German"},
	})
	subject.LanguageFacts = mediafacts.ResolveLanguages(media)
	if got, want := lumeName(t, subject, nil), "Example 2026 WEB-DL Multi DD 2.0-GRP"; got != want {
		t.Fatalf("consistent programme name = %q, want %q", got, want)
	}
}

func TestLumeStructuredName(t *testing.T) {
	s := lumeSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Hi10P Title",
		Year:        2026,
		Edition:     "Hybrid",
		WebDV:       true,
		Resolution:  "2160p",
		HDR:         "DV PQ10",
		VideoEncode: "Hi10P H.265",
		Tag:         "-GRP",
	})
	s.HDRFacts = api.HDRFacts{Formats: []api.HDRFormat{api.HDRFormatDolbyVision, api.HDRFormatPQ10}, Status: api.HDREvidenceComplete}
	if got, want := lumeName(t, s, nil), "Hi10P Title 2026 2160p WEB-DL DV HDR H.265-GRP"; got != want {
		t.Fatalf("%q want %q", got, want)
	}
	o := "Opaque-GRP"
	if got := lumeName(t, s, &o); got != o {
		t.Fatal(got)
	}
}

func TestLumeOmitsExactHi10PWithoutOverwritingManualComponent(t *testing.T) {
	s := lumeSubject(t, api.ReleaseNameRequest{
		Category:    "MOVIE",
		Type:        "WEBDL",
		Title:       "Example",
		Year:        2026,
		Resolution:  "1080p",
		VideoEncode: "Hi10P",
		Tag:         "-GRP",
	})
	if got, want := lumeName(t, s, nil), "Example 2026 1080p WEB-DL-GRP"; got != want {
		t.Fatalf("exact Hi10P name = %q, want %q", got, want)
	}
	manual := s
	manual.GeneratedName = manual.GeneratedName.Clone()
	marked := false
	for index := range manual.GeneratedName.Components {
		if manual.GeneratedName.Components[index].Role == api.NameRoleVideoEncode {
			manual.GeneratedName.Components[index].Manual = true
			marked = true
			break
		}
	}
	if !marked {
		t.Fatal("generated name is missing video encode")
	}
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got, want := lumeName(t, manual, nil), "Example 2026 1080p WEB-DL Hi10P-GRP"; got != want {
		t.Fatalf("manual exact Hi10P name = %q, want %q", got, want)
	}
}
func TestLumePolicy(t *testing.T) {
	p := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if p.ID != "unit3d/lume/v5" || p.Structured == nil {
		t.Fatalf("%#v", p)
	}
}

func TestLumeTVDBRejectsStaleMetadata(t *testing.T) {
	s := lumeSubject(t, api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "WEBDL",
		Title:       "Series",
		AltTitle:    "CA AKA Alt",
		Year:        2026,
		SearchYear:  "2026",
		Season:      "S01",
		Episode:     "E02",
		Resolution:  "1080p",
		VideoEncode: "H.265",
		Tag:         "-GRP",
	})
	s.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: s.SourcePath,
		Generation: 1,
		TVDB: &api.TVDBMetadata{
			TVDBID:      1,
			NameEnglish: "TVDB Series",
			NameDisambiguation: api.TVDBNameDisambiguation{
				CanonicalName: "TVDB Series",
				Status:        api.MetadataEvidenceStatusComplete,
				Source:        "tvdb-name/v1",
				IncludeYear:   true,
			},
		},
	}
	if got, want := lumeName(t, s, nil), "Series CA AKA Alt 2026 S01E02 1080p WEB-DL H.265-GRP"; got != want {
		t.Fatalf("TVDB name = %q, want %q", got, want)
	}
	s.ProviderMetadata.Generation = 2
	if got := lumeName(t, s, nil); got != s.ReleaseName {
		t.Fatalf("stale metadata changed name: %q", got)
	}
}
func lumeSubject(t *testing.T, r api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	n := metadata.BuildReleaseName(r, api.NopLogger{})
	if n.GeneratedName == nil {
		t.Fatal("document")
	}
	return api.UploadSubject{
		SourcePath:       "lume",
		ReleaseName:      n.Name,
		ReleaseNameNoTag: n.NameNoTag,
		GeneratedName:    n.GeneratedName,
		Identity: api.ExternalIdentity{
			SourcePath: "lume",
			Generation: 1,
			TVDBID:     1,
			Category:   api.CanonicalCategory(r.Category),
		},
		Release: api.ReleaseInfo{
			Category:   r.Category,
			Title:      r.Title,
			Year:       r.Year,
			Resolution: r.Resolution,
		},
		Type:        r.Type,
		DiscType:    r.DiscType,
		Edition:     r.Edition,
		WebDV:       r.WebDV,
		HDR:         r.HDR,
		VideoEncode: r.VideoEncode,
	}
}
func lumeName(t *testing.T, s api.UploadSubject, o *string) string {
	t.Helper()
	p, f := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "LUME",
		Meta:                s,
		RequestedUploadName: o,
	}, namePolicy())
	if f != nil {
		t.Fatal(f)
	}
	n, e := p.ReviewedUploadName()
	if e != nil {
		t.Fatal(e)
	}
	return n
}
