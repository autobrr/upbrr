// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package sam

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCategoryID(t *testing.T) {
	resolveCategoryID := Profile().Site.ResolveCategoryID
	if got := resolveCategoryID(api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}, Anime: true}); got != "3" {
		t.Fatalf("anime TV category ID = %q, want 3", got)
	}
	if got := resolveCategoryID(api.UploadSubject{
		Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Anime:    true,
		TVPack:   true,
	}); got != "3" {
		t.Fatalf("anime TV season category ID = %q, want 3", got)
	}
	if got := resolveCategoryID(api.UploadSubject{
		Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryTV},
		Anime:      true,
		EpisodeInt: 1,
	}); got != "3" {
		t.Fatalf("anime TV episode category ID = %q, want 3", got)
	}
	if got := resolveCategoryID(api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryTV}}); got != "2" {
		t.Fatalf("non-anime TV category ID = %q, want 2", got)
	}
	if got := resolveCategoryID(api.UploadSubject{Identity: api.ExternalIdentity{Category: api.CanonicalCategoryMovie}, Anime: true}); got != "1" {
		t.Fatalf("anime movie category ID = %q, want 1", got)
	}
}
