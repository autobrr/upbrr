// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import "testing"

func TestReleaseNameDocumentRenderCloneAndValidation(t *testing.T) {
	t.Parallel()

	document := &ReleaseNameDocument{
		Version: ReleaseNameDocumentVersionV1,
		Components: []ReleaseNameComponent{
			{
				Role:           NameRoleTitle,
				Value:          "Example: Release",
				AvailableValue: "Example: Release",
				Present:        true,
				Join:           " ",
			},
			{
				Role:           NameRoleAlternateTitle,
				AvailableValue: "Example AKA",
				Join:           " ",
			},
			{
				Role:           NameRoleHDR,
				Value:          "HDR10",
				AvailableValue: "HDR10",
				Present:        true,
				Join:           " ",
			},
			{
				Role:           NameRoleGroup,
				Value:          "-GRP",
				AvailableValue: "-GRP",
				Present:        true,
			},
		},
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if got, want := document.Render(), (ReleaseNameVariant{
		NameNoTag: "Example: Release HDR10",
		Name:      "Example: Release HDR10-GRP",
		CleanName: "Example- Release HDR10-GRP",
	}); got != want {
		t.Fatalf("Render() = %#v, want %#v", got, want)
	}

	cloned := document.Clone()
	cloned.Components[0].Value = "Changed"
	if document.Components[0].Value == cloned.Components[0].Value {
		t.Fatal("Clone() shared component storage")
	}
}

func TestReleaseNameDocumentRejectsInvalidComponents(t *testing.T) {
	t.Parallel()

	for _, document := range []*ReleaseNameDocument{
		{Version: ReleaseNameDocumentVersionV1},
		{Version: "release-name-document/v0", Components: []ReleaseNameComponent{{
			Role:    NameRoleTitle,
			Value:   "Example",
			Present: true,
		}}},
		{Version: ReleaseNameDocumentVersionV1, Components: []ReleaseNameComponent{{
			Role:    "unknown",
			Value:   "Example",
			Present: true,
		}}},
		{Version: ReleaseNameDocumentVersionV1, Components: []ReleaseNameComponent{{
			Role:    NameRoleTitle,
			Value:   "Example",
			Present: true,
		}, {
			Role:    NameRoleTitle,
			Value:   "Again",
			Present: true,
		}}},
		{Version: ReleaseNameDocumentVersionV1, Components: []ReleaseNameComponent{{Role: NameRoleTitle, Present: true}}},
	} {
		if err := document.Validate(); err == nil {
			t.Fatalf("Validate() accepted %#v", document)
		}
	}
	if NameRoleVideoFormat.Valid() == false || NameRoleHDR.Valid() == false || NameRoleDubbed.Valid() == false || NameRoleDualAudio.Valid() == false {
		t.Fatal("required naming roles are invalid")
	}
}

func TestReleaseNameDocumentAttachmentUsesFallbackAndClonesAnchors(t *testing.T) {
	t.Parallel()

	document := &ReleaseNameDocument{Version: ReleaseNameDocumentVersionV1, Components: []ReleaseNameComponent{
		{
			Role:    NameRoleTitle,
			Value:   "Example",
			Present: true,
			Join:    " ",
		},
		{
			Role:    NameRoleSeason,
			Value:   "S01",
			Present: true,
			Join:    " ",
		},
		{
			Role:     NameRoleEpisode,
			Value:    "E02",
			Present:  true,
			Join:     " ",
			AttachTo: []ReleaseNameRole{NameRoleSeason},
		},
		{
			Role:     NameRoleThreeD,
			Value:    "3D",
			Present:  true,
			Join:     " ",
			AttachTo: []ReleaseNameRole{NameRoleEpisode, NameRoleSeason},
		},
	}}
	if got, want := document.Render().NameNoTag, "Example S01E023D"; got != want {
		t.Fatalf("attached render = %q, want %q", got, want)
	}
	document.Components[1].Present = false
	document.Components[2].Present = false
	if got, want := document.Render().NameNoTag, "Example 3D"; got != want {
		t.Fatalf("fallback render = %q, want %q", got, want)
	}
	cloned := document.Clone()
	cloned.Components[2].AttachTo[0] = NameRoleTitle
	if document.Components[2].AttachTo[0] != NameRoleSeason {
		t.Fatal("Clone() shared attachment storage")
	}
}

func TestNormalizeEpisodeTitleModeDefaultsToInclude(t *testing.T) {
	t.Parallel()

	if got := NormalizeEpisodeTitleMode(EpisodeTitleModeUnspecified); got != EpisodeTitleModeInclude {
		t.Fatalf("unspecified mode = %q, want include", got)
	}
	if got := NormalizeEpisodeTitleMode(" OMIT "); got != EpisodeTitleModeOmit {
		t.Fatalf("normalized omit mode = %q", got)
	}
}

func TestReleaseNameElementPolicyNormalizedPreservesVersion(t *testing.T) {
	t.Parallel()

	got := (ReleaseNameElementPolicy{
		Version: " release-name-elements/v1 ",
	}).Normalized()
	if got.Version != ReleaseNameElementPolicyVersionV1 || got.EpisodeTitleMode != EpisodeTitleModeInclude {
		t.Fatalf("normalized element policy = %#v", got)
	}
}
