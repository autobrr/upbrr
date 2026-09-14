package hhd

import (
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestHHDStructuredName(t *testing.T) {
	s := hhdSubject(t, api.ReleaseNameRequest{
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
	if got, want := hhdName(t, s, nil), "Series AKA Alt US 2026 S01E02 1080p WEB-DL H.265-GRP"; got != want {
		t.Fatalf("name=%q want=%q", got, want)
	}
	stale := s
	stale.ProviderMetadata.Generation = 2
	if got := hhdName(t, stale, nil); got != stale.ReleaseName {
		t.Fatalf("stale metadata changed name: %q", got)
	}
	o := "Opaque-GRP"
	if got := hhdName(t, s, &o); got != o {
		t.Fatalf("opaque=%q", got)
	}
	manualEmptyYear := hhdSubject(t, api.ReleaseNameRequest{
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
	if got, want := hhdName(t, manualEmptyYear, nil), "Series US S01E02 1080p WEB-DL H.265-GRP"; got != want {
		t.Fatalf("manual-empty year locale name = %q, want %q", got, want)
	}
}
func TestHHDDiscDistributorAndManualEdition(t *testing.T) {
	s := hhdSubject(t, api.ReleaseNameRequest{
		Category:   "MOVIE",
		Type:       "DISC",
		DiscType:   "BDMV",
		Title:      "Example",
		Year:       2026,
		Resolution: "1080p",
		Region:     "USA",
		Source:     "BluRay",
		VideoCodec: "AVC",
		Tag:        "-GRP",
	})
	s.Distributor = "Criterion"
	if got, want := hhdName(t, s, nil), "Example 2026 1080p Criterion USA BluRay AVC-GRP"; got != want {
		t.Fatalf("disc=%q want=%q", got, want)
	}
}
func TestHHDPolicy(t *testing.T) {
	p := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if p.ID != "unit3d/hhd/v3" || p.Structured == nil {
		t.Fatalf("%#v", p)
	}
}
func hhdSubject(t *testing.T, r api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	n := metadata.BuildReleaseName(r, api.NopLogger{})
	if n.GeneratedName == nil {
		t.Fatal("document")
	}
	return api.UploadSubject{
		SourcePath:       "hhd",
		ReleaseName:      n.Name,
		ReleaseNameNoTag: n.NameNoTag,
		GeneratedName:    n.GeneratedName,
		Identity: api.ExternalIdentity{
			SourcePath: "hhd",
			Generation: 1,
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
		Source:      r.Source,
		Region:      r.Region,
		Edition:     r.Edition,
		VideoEncode: r.VideoEncode,
		VideoCodec:  r.VideoCodec,
	}
}
func hhdName(t *testing.T, s api.UploadSubject, o *string) string {
	t.Helper()
	p, f := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "HHD",
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
