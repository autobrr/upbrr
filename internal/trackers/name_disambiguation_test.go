// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCurrentTVDBNameDisambiguation(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*api.UploadSubject, *api.ReleaseNameDocument)
		want bool
	}{
		{name: "different provider title", want: true},
		{
			name: "partial established evidence",
			want: true,
			edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
				meta.ProviderMetadata.TVDB.NameDisambiguation.Status = api.MetadataEvidenceStatusPartial
			},
		},
		{
			name: "normalized source keys",
			want: true,
			edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
				meta.Identity.SourcePath = " SOURCE "
				meta.ProviderMetadata.SourcePath = "Source"
			},
		},
		{name: "missing source", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.SourcePath = "" }},
		{name: "unscoped identity", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.Identity.SourcePath = "" }},
		{name: "unscoped provider", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.ProviderMetadata.SourcePath = "" }},
		{name: "different identity source", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.Identity.SourcePath = "other" }},
		{name: "different provider source", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.ProviderMetadata.SourcePath = "other" }},
		{name: "legacy generation", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
			meta.Identity.Generation, meta.ProviderMetadata.Generation = 0, 0
		}},
		{name: "stale generation", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.ProviderMetadata.Generation = 2 }},
		{name: "unversioned provider", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.ProviderMetadata.Generation = 0 }},
		{name: "missing TVDB", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.ProviderMetadata.TVDB = nil }},
		{name: "missing identity ID", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.Identity.TVDBID = 0 }},
		{name: "missing provider ID", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.ProviderMetadata.TVDB.TVDBID = 0 }},
		{name: "different TVDB identity", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) { meta.ProviderMetadata.TVDB.TVDBID = 2 }},
		{name: "missing evidence source", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
			meta.ProviderMetadata.TVDB.NameDisambiguation.Source = ""
		}},
		{name: "missing canonical evidence", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
			meta.ProviderMetadata.TVDB.NameDisambiguation.CanonicalName = ""
		}},
		{name: "missing evidence status", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
			meta.ProviderMetadata.TVDB.NameDisambiguation.Status = ""
		}},
		{name: "unavailable evidence", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
			meta.ProviderMetadata.TVDB.NameDisambiguation.Status = api.MetadataEvidenceStatusUnavailable
		}},
		{name: "conflicting evidence", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
			meta.ProviderMetadata.TVDB.NameDisambiguation.Status = api.MetadataEvidenceStatusContradictory
		}},
		{name: "manual title component", edit: func(_ *api.UploadSubject, document *api.ReleaseNameDocument) { document.Components[0].Manual = true }},
		{name: "manual title provenance", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
			meta.EffectiveMetadata.TitleProvenance = api.FactProvenanceManual
		}},
		{name: "manual empty title", edit: func(meta *api.UploadSubject, _ *api.ReleaseNameDocument) {
			meta.EffectiveMetadata.TitleProvenance = api.FactProvenanceManualEmpty
		}},
		{name: "hidden title", edit: func(_ *api.UploadSubject, document *api.ReleaseNameDocument) { document.Components[0].Present = false }},
		{name: "missing title", edit: func(_ *api.UploadSubject, document *api.ReleaseNameDocument) { document.Components = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := api.UploadSubject{
				SourcePath: "source",
				Identity: api.ExternalIdentity{
					SourcePath: "source",
					Generation: 1,
					TVDBID:     1,
				},
				ProviderMetadata: api.SourceScopedMetadata{
					SourcePath: "source",
					Generation: 1,
					TVDB: &api.TVDBMetadata{
						TVDBID: 1,
						NameDisambiguation: api.TVDBNameDisambiguation{
							CanonicalName: "TVDB Series",
							SeriesYear:    2026,
							Locale:        "US",
							IncludeYear:   true,
							IncludeLocale: true,
							Status:        api.MetadataEvidenceStatusComplete,
							Source:        "tvdb-name/v1",
						},
					},
				},
			}
			document := &api.ReleaseNameDocument{Components: []api.ReleaseNameComponent{{
				Role:    api.NameRoleTitle,
				Value:   "Other Provider Series",
				Present: true,
			}}}
			if test.edit != nil {
				test.edit(&meta, document)
			}
			evidence, ok := CurrentTVDBNameDisambiguation(&NameEditor{document: document}, meta)
			if ok != test.want {
				t.Fatalf("current evidence = %v, want %v", ok, test.want)
			}
			if ok && evidence != meta.ProviderMetadata.TVDB.NameDisambiguation {
				t.Fatalf("evidence changed: %+v", evidence)
			}
		})
	}
}
