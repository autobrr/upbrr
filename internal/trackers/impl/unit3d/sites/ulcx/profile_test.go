package ulcx

import (
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

func TestULCXStructuredName(t *testing.T) {
	s := ulcxSubject(t, api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "WEBDL",
		Title:       "Series",
		AltTitle:    "AKA Alt",
		Year:        2026,
		SearchYear:  "2026",
		Season:      "S01",
		Episode:     "E02",
		Resolution:  "1080p",
		Edition:     "Hybrid",
		WebDV:       true,
		VideoEncode: "x265",
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
	if got, want := ulcxName(t, s, nil), "Series AKA Alt US S01E02 1080p WEB-DL x265-GRP"; got != want {
		t.Fatalf("%q want %q", got, want)
	}
	stale := s
	stale.ProviderMetadata.Generation = 2
	if got, want := ulcxName(t, stale, nil), "Series 2026 AKA Alt S01E02 1080p WEB-DL x265-GRP"; got != want {
		t.Fatalf("stale metadata layout = %q, want %q", got, want)
	}
	o := "Opaque-GRP"
	if got := ulcxName(t, s, &o); got != o {
		t.Fatal(got)
	}
	manualEmptyYear := ulcxSubject(t, api.ReleaseNameRequest{
		Category:    "TV",
		Type:        "WEBDL",
		Title:       "Series",
		NoAKA:       true,
		Season:      "S01",
		Episode:     "E02",
		Resolution:  "1080p",
		VideoEncode: "x265",
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
	if got, want := ulcxName(t, manualEmptyYear, nil), "Series US S01E02 1080p WEB-DL x265-GRP"; got != want {
		t.Fatalf("manual-empty year locale name = %q, want %q", got, want)
	}
}
func TestULCXPolicy(t *testing.T) {
	p := unit3d.NewWithProfile(Profile()).ReleaseNamePolicy()
	if p.ID != "unit3d/ulcx/v3" || p.Structured == nil {
		t.Fatalf("%#v", p)
	}
}
func ulcxSubject(t *testing.T, r api.ReleaseNameRequest) api.UploadSubject {
	t.Helper()
	n := metadata.BuildReleaseName(r, api.NopLogger{})
	if n.GeneratedName == nil {
		t.Fatal("document")
	}
	return api.UploadSubject{
		SourcePath:       "ulcx",
		ReleaseName:      n.Name,
		ReleaseNameNoTag: n.NameNoTag,
		GeneratedName:    n.GeneratedName,
		Identity: api.ExternalIdentity{
			SourcePath: "ulcx",
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
		Edition:     r.Edition,
		WebDV:       r.WebDV,
		VideoEncode: r.VideoEncode,
		VideoCodec:  r.VideoCodec,
	}
}
func ulcxName(t *testing.T, s api.UploadSubject, o *string) string {
	t.Helper()
	p, f := trackers.PrepareInputWithReleaseNamePolicy(trackers.PreparationInput{
		Tracker:             "ULCX",
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
