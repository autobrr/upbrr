// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// CurrentTVDBNameDisambiguation returns established qualifier evidence for the
// current TVDB identity and prepared generation. Automatic titles may use another
// provider's wording; manual titles retain their existing qualifier choices.
func CurrentTVDBNameDisambiguation(editor *NameEditor, meta api.UploadSubject) (api.TVDBNameDisambiguation, bool) {
	title, ok := editor.Component(api.NameRoleTitle)
	if !ok || !title.Present || title.Manual || meta.EffectiveMetadata.TitleProvenance.IsManual() ||
		!currentNameProviderMetadata(meta) || meta.ProviderMetadata.TVDB == nil || meta.Identity.TVDBID <= 0 ||
		meta.ProviderMetadata.TVDB.TVDBID != meta.Identity.TVDBID {
		return api.TVDBNameDisambiguation{}, false
	}
	evidence := meta.ProviderMetadata.TVDB.NameDisambiguation
	if strings.TrimSpace(evidence.CanonicalName) == "" || strings.TrimSpace(evidence.Source) == "" ||
		(evidence.Status != api.MetadataEvidenceStatusComplete && evidence.Status != api.MetadataEvidenceStatusPartial) {
		return api.TVDBNameDisambiguation{}, false
	}
	return evidence, true
}

func currentNameProviderMetadata(meta api.UploadSubject) bool {
	return strings.TrimSpace(meta.SourcePath) != "" && strings.TrimSpace(meta.Identity.SourcePath) != "" &&
		strings.TrimSpace(meta.ProviderMetadata.SourcePath) != "" && meta.Identity.Generation > 0 &&
		meta.ProviderMetadata.IsCurrentFor(meta.SourcePath, meta.Identity)
}
