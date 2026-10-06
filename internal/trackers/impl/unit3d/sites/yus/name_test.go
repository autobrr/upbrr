package yus

import (
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
	"strings"
	"testing"
)

func TestYUSStructuredName(t *testing.T) {
	s := yusSubject(t, api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "WEBDL",
		Title:       "Series",
		AltTitle:    "AKA Alt",
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
		TVDB: &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
			CanonicalName: "Series",
			IncludeYear:   true,
			IncludeLocale: true,
			Locale:        "US",
		}},
	}
	if got, want := yusName(t, s, nil), "Series AKA Alt US 2026 S01E02 1080p WEB-DL H.265-GRP"; got != want {
		t.Fatalf("%q want %q", got, want)
	}
	stale := s
	stale.ProviderMetadata.Generation = 2
	if got := yusName(t, stale, nil); got != stale.ReleaseName {
		t.Fatalf("stale metadata changed name: %q", got)
	}
	o := "Opaque-GRP"
	if got := yusName(t, s, &o); got != o {
		t.Fatal(got)
	}
	manualEmptyYear := yusSubject(t, api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "WEBDL",
		Title:       "Series",
		NoAKA:       true,
		Season:      "S01",
		Episode:     "E02",
		Resolution:  "1080p",
		VideoEncode: "H.265",
		Tag:         "-GRP",
	})
	manualEmptyYear.EffectiveMetadata.YearProvenance = api.FactProvenanceManualEmpty
	manualEmptyYear.ProviderMetadata = api.SourceScopedMetadata{
		SourcePath: manualEmptyYear.SourcePath,
		Generation: 1,
		TVDB: &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{
			CanonicalName: "Series",
			IncludeLocale: true,
			Locale:        "US",
		}},
	}
	if got, want := yusName(t, manualEmptyYear, nil), "Series US S01E02 1080p WEB-DL H.265-GRP"; got != want {
		t.Fatalf("manual-empty year locale name = %q, want %q", got, want)
	}
}
func TestYUSPolicy(t *testing.T) {
	p := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if p.ID != "unit3d/yus/v6" || p.Structured == nil {
		t.Fatalf("%#v", p)
	}
}

func TestYUSCanonicalCutPresentationAndEdition(t *testing.T) {
	t.Parallel()
	subject := yusSubject(t, api.ReleaseNameRequest{
		Category:     "MOVIE",
		Type:         "WEBDL",
		Title:        "Collector's Cut IMAX Story",
		Year:         2026,
		Cut:          "Extended Cut",
		Edition:      "Collector's Cut",
		Presentation: "Open Matte IMAX",
		Resolution:   "1080p",
		VideoEncode:  "H.264",
		Tag:          "-GRP",
	})
	if got, want := yusName(t, subject, nil), "Collector's Cut IMAX Story 2026 Extended Cut Open Matte IMAX 1080p WEB-DL H.264-GRP"; got != want {
		t.Fatalf("canonical modifiers = %q, want %q", got, want)
	}
	manual := subject
	manual.GeneratedName = subject.GeneratedName.Clone()
	for index := range manual.GeneratedName.Components {
		component := &manual.GeneratedName.Components[index]
		if component.Role == api.NameRoleEdition {
			component.Manual = true
			continue
		}
		if component.Role == api.NameRoleEditionSet || component.Role == api.NameRoleCut || component.Role == api.NameRolePresentation {
			component.Manual, component.Present = true, false
		}
	}
	manual.ReleaseName = manual.GeneratedName.Render().Name
	if got := yusName(t, manual, nil); got != manual.ReleaseName {
		t.Fatalf("manual modifier choices = %q, want %q", got, manual.ReleaseName)
	}
	opaque := subject
	opaque.GeneratedName = nil
	if got := yusName(t, opaque, nil); got != opaque.ReleaseName {
		t.Fatalf("opaque name = %q, want %q", got, opaque.ReleaseName)
	}
}

func TestYUSCanonicalEditionSet(t *testing.T) {
	t.Parallel()
	subject := yusSubject(t, api.ReleaseNameRequest{
		Category:     "MOVIE",
		Type:         "WEBDL",
		Title:        "2in1 Extended Collector's Open Matte Story",
		Year:         2026,
		EditionSet:   "2in1",
		Cut:          "Extended",
		Edition:      "Collector's",
		Presentation: "Open Matte",
		Resolution:   "1080p",
		VideoEncode:  "H.264",
		Tag:          "-GRP",
	})
	if got, want := yusName(t, subject, nil), "2in1 Extended Collector's Open Matte Story 2026 2in1 1080p WEB-DL H.264-GRP"; got != want {
		t.Fatalf("edition set = %q, want %q", got, want)
	}
}
func yusSubject(t *testing.T, r api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	n := metadata.BuildReleaseName(r, api.NopLogger{})
	if n.GeneratedName == nil {
		t.Fatal("document")
	}
	category, err := api.NormalizeCanonicalCategory(r.Category)
	if err != nil {
		t.Fatalf("normalize category %q: %v", r.Category, err)
	}
	audio, _ := n.GeneratedName.Component(api.NameRoleAudio)
	facts := api.LanguageFacts{AudioAbsent: audio.Value == ""}
	if audio.Value != "" {
		facts.Tracks = []api.MediaTrackFacts{{
			Kind:    api.MediaTrackAudio,
			Role:    api.AudioRoleProgramme,
			Default: true,
			Codec:   strings.Fields(audio.Value)[0],
 AudioLabel: audio.Value,
		}}
	}
	return api.UploadSubject{
		LanguageFacts:    facts,
		SourcePath:       "yus",
		ReleaseName:      n.Name,
		ReleaseNameNoTag: n.NameNoTag,
		GeneratedName:    n.GeneratedName,
		Identity: api.ExternalIdentity{
			SourcePath: "yus",
			Generation: 1,
			Category:   category,
		},
		Release: api.ReleaseInfo{
			Category:   r.Category,
			Title:      r.Title,
			Year:       r.Year,
			Resolution: r.Resolution,
		},
		Type:         r.Type,
		DiscType:     r.DiscType,
		EditionSet:   r.EditionSet,
		Cut:          r.Cut,
		Edition:      r.Edition,
		Repack:       r.Repack,
		Presentation: r.Presentation,
		VideoEncode:  r.VideoEncode,
		VideoCodec:   r.VideoCodec,
	}
}
func yusName(t *testing.T, s api.UploadSubject, o *string) string {
	t.Helper()
	p, f := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "YUS",
		Meta:                s,
		RequestedUploadName: o,
	}, unit3d.NewWithProfile(Profile()).ReleaseNamePolicy())
	if f != nil {
		t.Fatal(f)
	}
	n, e := p.ReviewedUploadName()
	if e != nil {
		t.Fatal(e)
	}
	return n
}

func TestYUSCutAndReleaseVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ cut, edition, presentation, want string }{
		{cut: "Extended", want: "Extended "},
		{cut: "Uncut", want: "Uncut "},
		{cut: "Director's Cut", want: "Director's Cut "},
		{cut: "Alternate Version", want: "Alternate Version "},
		{presentation: "IMAX", want: "IMAX "},
		{presentation: "Open Matte", want: "Open Matte "},
		{edition: "20th Anniversary"}, {edition: "Remastered"}, {edition: "Limited"},
		{edition: "Cutting Edge"}, {edition: "Uncut"}, {},
	} {
		t.Run(strings.TrimSpace(tc.cut+" "+tc.edition+" "+tc.presentation), func(t *testing.T) {
			for _, version := range []string{"REPACK", "PROPER", "REPACK2"} {
				subject := yusSubject(t, api.ReleaseNameRequest{
					Category:     "MOVIE",
					Type:         "ENCODE",
					Title:        "Cutting Edge Uncut Story",
					Year:         2026,
					Cut:          tc.cut,
					Edition:      tc.edition,
					Presentation: tc.presentation,
					Repack:       version,
					Resolution:   "1080p",
					Source:       "BluRay",
					Tag:          "-GRP",
				})
				want := "Cutting Edge Uncut Story 2026 " + tc.want + version + " 1080p BluRay-GRP"
				if got := yusName(t, subject, nil); got != want {
					t.Fatalf("%s: name = %q, want %q", version, got, want)
				}
			}
		})
	}
}

func TestYUSCutVersionDiscDistributor(t *testing.T) {
	subject := yusSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "DISC",
		DiscType:   "BDMV",
		Title:      "Example Film",
		Year:       2026,
		Cut:        "Extended",
		Edition:    "Limited",
		Repack:     "PROPER",
		Resolution: "1080p",
		Source:     "BluRay",
		Tag:        "-GRP",
	})
	subject.Distributor = "Example Distributor"
	want := "Example Film 2026 Extended PROPER 1080p Example Distributor BluRay-GRP"
	if got := yusName(t, subject, nil); got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

func TestYUSCutVersionDVDOrder(t *testing.T) {
	subject := yusSubject(t, api.ReleaseNameRequest{
		Category: "MOVIE",
		Type:     "DISC",
		DiscType: "DVD",
		Title:    "Example Film",
		Year:     2026,
		Cut:      "Uncut",
		Repack:   "PROPER",
		Source:   "PAL DVD",
		DVDSize:  "DVD9",
		Tag:      "-GRP",
	})
	want := "Example Film 2026 Uncut PROPER PAL DVD9-GRP"
	if got := yusName(t, subject, nil); got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
}

func TestYUSEditionAndVersionManualChoices(t *testing.T) {
	for _, present := range []bool{true, false} {
		subject := yusSubject(t, api.ReleaseNameRequest{
			Category:   "MOVIE",
			Type:       "ENCODE",
			Title:      "Example Film",
			Year:       2026,
			Edition:    "Limited",
			Repack:     "PROPER",
			Resolution: "1080p",
			Source:     "BluRay",
			Tag:        "-GRP",
		})
		for i := range subject.GeneratedName.Components {
			component := &subject.GeneratedName.Components[i]
			if component.Role == api.NameRoleEdition || component.Role == api.NameRoleRepack {
				component.Manual, component.Present = true, present
			}
		}
		subject.ReleaseName = subject.GeneratedName.Render().Name
		if got := yusName(t, subject, nil); got != subject.ReleaseName {
			t.Fatalf("manual present=%v: name = %q, want %q", present, got, subject.ReleaseName)
		}
		opaque := "Exact.Edition.PROPER.2026-GRP"
		if got := yusName(t, subject, &opaque); got != opaque {
			t.Fatalf("opaque name = %q, want %q", got, opaque)
		}
	}
}

func TestYUSVersionTVIdentityAndDVDRip(t *testing.T) {
	for _, tc := range []struct {
		request api.ReleaseNameRequest
		want    string
	}{
		{request: api.ReleaseNameRequest{
			Category:           "TV",
			Type:               "WEBDL",
			Title:              "Example Series",
			Year:               2026,
			SearchYear:         "2026",
			Season:             "S01",
			Episode:            "E02",
			EpisodeTitle:       "A New Day",
			ManualEpisodeTitle: true,
			Cut:                "Extended",
			Repack:             "PROPER",
			Resolution:         "1080p",
			Tag:                "-GRP",
		}, want: "Example Series 2026 S01E02 A New Day Extended PROPER 1080p WEB-DL-GRP"},
		{request: api.ReleaseNameRequest{
			Category:   "MOVIE",
			Type:       "DVDRIP",
			Title:      "Example Film",
			Year:       2026,
			Repack:     "REPACK2",
			Resolution: "480p",
			Source:     "DVD",
			Tag:        "-GRP",
		}, want: "Example Film 2026 REPACK2 480p DVDRip-GRP"},
	} {
		if got := yusName(t, yusSubject(t, tc.request), nil); got != tc.want {
			t.Fatalf("name = %q, want %q", got, tc.want)
		}
	}
}

func TestYUSDVDRipRetainsAvailableCuts(t *testing.T) {
	for _, category := range []string{"MOVIE", "TV"} {
		t.Run(category, func(t *testing.T) {
			subject := yusSubject(t, api.ReleaseNameRequest{
				Category:   category,
				Type:       "DVDRIP",
				Title:      "Example Release",
				Year:       2026,
				SearchYear: "2026",
				Cut:        "Uncut",
				Repack:     "PROPER",
				Resolution: "480p",
				Source:     "DVD",
				Tag:        "-GRP",
			})
			if got := yusName(t, subject, nil); !strings.Contains(got, "Uncut PROPER 480p") {
				t.Fatalf("name=%q", got)
			}
			for index := range subject.GeneratedName.Components {
				component := &subject.GeneratedName.Components[index]
				if component.Role == api.NameRoleCut {
					component.Manual = true
					component.Present = false
				}
			}
			if got := yusName(t, subject, nil); strings.Contains(got, "Uncut") {
				t.Fatalf("manual omission changed: %q", got)
			}
		})
	}
}

func TestYUSReleaseVersionKeepsEditionSetAtomic(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"WEBDL", "DVDRIP", "DISC"} {
		t.Run(typ, func(t *testing.T) {
			req := api.ReleaseNameRequest{
				Category:     "MOVIE",
				Type:         typ,
				Title:        "Example Film",
				Year:         2026,
				EditionSet:   "2in1",
				Cut:          "Extended",
				Edition:      "Collector's",
				Presentation: "IMAX",
				Repack:       "PROPER",
				Resolution:   "480p",
				Source:       "PAL DVD",
				Tag:          "-GRP",
			}
			if typ == "DISC" {
				req.DiscType = "DVD"
				req.DVDSize = "DVD9"
			}
			got := yusName(t, yusSubject(t, req), nil)
			if !strings.Contains(got, "2in1 PROPER") {
				t.Fatalf("set/version ordering=%q", got)
			}
			for _, hidden := range []string{"Extended", "Collector's", "IMAX"} {
				if strings.Contains(got, hidden) {
					t.Fatalf("set exposed hidden detail %q: %q", hidden, got)
				}
			}
		})
	}
}

func TestYUSProgrammeLanguageAndDefaultAudioNaming(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                  string
		languages             []string
		status                api.MetadataEvidenceStatus
		discType, releaseType string
		wantMulti             bool
	}{
		{
			name:      "two unrelated languages",
			languages: []string{"German", "French"},
			wantMulti: true,
		},
		{
			name:      "three languages",
			languages: []string{"Japanese", "English", "German"},
			wantMulti: true,
		},
		{name: "one language", languages: []string{"Japanese"}},
		{
			name:      "unknown second label",
			languages: []string{"English", "unknown-label"},
			status:    api.MetadataEvidenceStatusPartial,
		},
		{
			name:      "contradictory",
			languages: []string{"Japanese", "English"},
			status:    api.MetadataEvidenceStatusContradictory,
		},
		{
			name:        "canonical full disc",
			languages:   []string{"Japanese", "English"},
			releaseType: "DISC",
		},
		{
			name:        "disc sourced remux",
			languages:   []string{"Japanese", "English"},
			discType:    "BDMV",
			releaseType: "REMUX",
			wantMulti:   true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := yusSubject(t, api.ReleaseNameRequest{
				Category:    "MOVIE",
				Type:        "WEBDL",
				Source:      "WEB",
				Resolution:  "1080p",
				VideoEncode: "H.265",
				Tag:         "-GRP",
				Title:       "Example",
				Year:        2026,
				Audio:       "AAC 2.0",
			})
			s.Type = test.releaseType
			s.DiscType = test.discType
			s.LanguageFacts = api.LanguageFacts{
				ProgrammeLanguages: test.languages,
				ProgrammeStatus:    test.status,
				Tracks: []api.MediaTrackFacts{{
					Kind:    api.MediaTrackAudio,
					Role:    api.AudioRoleProgramme,
					Default: true,
					Codec:   "DD+",
 AudioLabel: "DD+ 5.1",
				}},
			}
			got := yusName(t, s, nil)
			if strings.Contains(got, "Multi-Audio") != test.wantMulti {
				t.Fatalf("marker mismatch: %s", got)
			}
			wantAudio := "DD+ 5.1"
			if test.releaseType == "DISC" {
				wantAudio = "AAC 2.0"
			}
			if !strings.Contains(got, wantAudio) {
				t.Fatalf("default audio missing: %s", got)
			}
		})
	}
}
