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
	if p.ID != "unit3d/yus/v4" || p.Structured == nil {
		t.Fatalf("%#v", p)
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
	return api.UploadSubject{
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
		Type:        r.Type,
		DiscType:    r.DiscType,
		Edition:     r.Edition,
		Repack:      r.Repack,
		VideoEncode: r.VideoEncode,
		VideoCodec:  r.VideoCodec,
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
	for _, tc := range []struct {
		edition string
		want    string
	}{
		{edition: "Extended", want: "Extended "},
		{edition: "Uncut", want: "Uncut "},
		{edition: "Director's Cut", want: "Director's Cut "},
		{edition: "IMAX", want: "IMAX "},
		{edition: "Open Matte", want: "Open Matte "},
		{edition: "20th Anniversary"},
		{edition: "Remastered"},
		{edition: "Limited"},
		{},
	} {
		t.Run(tc.edition, func(t *testing.T) {
			for _, version := range []string{"REPACK", "PROPER", "REPACK2"} {
				subject := yusSubject(t, api.ReleaseNameRequest{
					Category:   "MOVIE",
					Type:       "ENCODE",
					Title:      "Cut Version",
					Year:       2026,
					Edition:    tc.edition,
					Repack:     version,
					Resolution: "1080p",
					Source:     "BluRay",
					Tag:        "-GRP",
				})
				want := "Cut Version 2026 " + tc.want + version + " 1080p BluRay-GRP"
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
		Edition:    "Extended",
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
		Edition:  "Uncut",
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
			Edition:            "Extended",
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
				Edition:    "Uncut",
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
				if component.Role == api.NameRoleEdition {
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
