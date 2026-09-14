package lume

import (
	"github.com/autobrr/upbrr/internal/metadata"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/unit3d"
	"github.com/autobrr/upbrr/pkg/api"
	"testing"
)

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
	if p.ID != "unit3d/lume/v3" || p.Structured == nil {
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
		TVDB:       &api.TVDBMetadata{NameDisambiguation: api.TVDBNameDisambiguation{CanonicalName: "Series", IncludeYear: true}},
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
