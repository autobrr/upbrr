// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveCategoryUsesCanonicalIdentityDespiteEmptyTVMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata api.SourceScopedMetadata
	}{
		{name: "TVDB", metadata: api.SourceScopedMetadata{TVDB: &api.TVDBMetadata{}}},
		{name: "TVmaze", metadata: api.SourceScopedMetadata{TVmaze: &api.TVmazeMetadata{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			meta := api.RuleSubject{
				Identity:         api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
				ProviderMetadata: tt.metadata,
			}
			if got := resolveCategory(meta); got != "movie" {
				t.Fatalf("expected canonical movie category, got %q", got)
			}
		})
	}
}
