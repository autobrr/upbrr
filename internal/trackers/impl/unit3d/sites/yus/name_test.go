package yus

import (
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
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
		Type:         r.Type,
		DiscType:     r.DiscType,
		EditionSet:   r.EditionSet,
		Cut:          r.Cut,
		Edition:      r.Edition,
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
