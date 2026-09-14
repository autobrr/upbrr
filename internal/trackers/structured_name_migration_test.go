// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNameEditorMigrationOperationsPreserveIsolationAndAuthority(t *testing.T) {
	document := structuredSubject().GeneratedName.Clone()
	editor := &NameEditor{document: document}
	if err := editor.InsertAfter(api.NameRoleSubtitleMarker, "[No subs]", api.NameRoleGroup); err != nil {
		t.Fatal(err)
	}
	roles := editor.PresentRoles()
	if roles[len(roles)-1] != api.NameRoleSubtitleMarker {
		t.Fatal(roles)
	}
	if err := editor.InsertAfter(api.NameRoleLocale, "US", api.NameRoleTitle); err != nil {
		t.Fatal(err)
	}
	for i := range document.Components {
		if document.Components[i].Role == api.NameRoleLocale {
			document.Components[i].AttachTo = []api.ReleaseNameRole{api.NameRoleTitle}
		}
	}
	if err := editor.SetJoin(api.NameRoleLocale, "-"); err != nil {
		t.Fatal(err)
	}
	component, ok := editor.Component(api.NameRoleLocale)
	if !ok || component.Join != "-" {
		t.Fatal(component)
	}
	component.Value = "changed"
	component.Join = "bad"
	component.AttachTo[0] = api.NameRoleGroup
	current, _ := editor.Component(api.NameRoleLocale)
	if current.Value != "US" || current.Join != "-" || current.AttachTo[0] != api.NameRoleTitle {
		t.Fatal("snapshot aliases component")
	}
	for i := range document.Components {
		if document.Components[i].Role == api.NameRoleLocale {
			document.Components[i].Manual = true
		}
	}
	if err := editor.InsertAfter(api.NameRoleLocale, "UK", api.NameRoleGroup); err != nil {
		t.Fatal(err)
	}
	if err := editor.SetJoin(api.NameRoleLocale, " "); err != nil {
		t.Fatal(err)
	}
	current, _ = editor.Component(api.NameRoleLocale)
	if current.Value != "US" || current.Join != "-" {
		t.Fatal("manual component changed")
	}
	editor.mandatory = true
	if err := editor.InsertAfter(api.NameRoleLocale, "UK", api.NameRoleGroup); err == nil {
		t.Fatal("undeclared insertion accepted")
	}
	if err := editor.SetJoin(api.NameRoleLocale, " "); err == nil {
		t.Fatal("undeclared layout change accepted")
	}
	editor.authority = []NameAuthority{
		{Role: api.NameRoleLocale, Aspect: NamePresence},
		{Role: api.NameRoleLocale, Aspect: NameValue},
		{Role: api.NameRoleLocale, Aspect: NameOrder},
	}
	if err := editor.InsertAfter(api.NameRoleLocale, "UK", api.NameRoleGroup); err != nil {
		t.Fatal(err)
	}
	if err := editor.SetJoin(api.NameRoleLocale, " "); err != nil {
		t.Fatal(err)
	}
	current, _ = editor.Component(api.NameRoleLocale)
	if current.Value != "UK" || current.Join != " " {
		t.Fatal("declared authority not applied")
	}
	if err := editor.InsertAfter(api.NameRoleLocale, "UK", api.NameRoleDistributor); err == nil {
		t.Fatal("missing mandatory anchor accepted")
	}
}

func TestStructuredExactNamePreservesRequestedAndMandatoryAuthority(t *testing.T) {
	policy := StructuredNamePolicy{
		ExactName: func(api.UploadSubject, config.TrackerConfig) string { return "Questionnaire DV Name" },
		Defaults: func(*NameEditor, api.UploadSubject, config.TrackerConfig) error {
			t.Fatal("defaults ran on exact name")
			return nil
		},
	}
	binding := StructuredReleaseNamePolicy("test/exact/v1", policy)
	input := PreparationInput{Meta: structuredSubject()}
	got, err := resolveReleaseNames(input, binding)
	if err != nil || got.Upload != "Questionnaire DV Name" {
		t.Fatalf("exact=%+v err=%v", got, err)
	}
	requested := "Requested Name"
	input.RequestedUploadName = &requested
	got, err = resolveReleaseNames(input, binding)
	if err != nil || got.Upload != requested {
		t.Fatalf("requested=%+v err=%v", got, err)
	}
	binding.Structured.Mandatory = func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
		return editor.Omit(api.NameRoleEdition)
	}
	binding.Structured.Authority = []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}}
	if _, err := resolveReleaseNames(input, binding); err == nil {
		t.Fatal("mandatory opaque rule bypassed")
	}
	input.RequestedUploadName = nil
	if _, err := resolveReleaseNames(input, binding); err == nil {
		t.Fatal("exact selector bypassed mandatory opaque rejection")
	}
	binding.Structured.Opaque = OpaqueNameRebuild
	binding.Structured.Defaults = nil
	got, err = resolveReleaseNames(input, binding)
	if err != nil || got.Upload == "Questionnaire DV Name" {
		t.Fatalf("exact rebuild=%+v err=%v", got, err)
	}
}

func TestMigrationNameRolesValidate(t *testing.T) {
	for _, role := range []api.ReleaseNameRole{api.NameRoleLocale, api.NameRoleDistributor, api.NameRoleSubtitleMarker, api.NameRoleOriginalGroup} {
		document := &api.ReleaseNameDocument{Version: api.ReleaseNameDocumentVersionV1, Components: []api.ReleaseNameComponent{{
			Role:    role,
			Value:   "Example",
			Present: true,
		}}}
		if err := document.Validate(); err != nil {
			t.Fatalf("%s: %v", role, err)
		}
	}
}

func TestSearchGeneratedNameChangeInvalidatesReviewedNames(t *testing.T) {
	policy := StructuredNamePolicy{}
	binding := StructuredReleaseNamePolicy("test/search-generated/v1", policy)
	prepared, failure := PrepareInputWithReleaseNamePolicy(PreparationInput{Tracker: "EXAMPLE", Meta: structuredSubject()}, binding)
	if failure != nil {
		t.Fatal(failure)
	}
	policy.SearchGeneratedName = true
	if _, failure := PrepareInputWithReleaseNamePolicy(prepared, StructuredReleaseNamePolicy(binding.ID, policy)); failure == nil {
		t.Fatal("changed generated-search policy accepted a previously reviewed identical name")
	}
}
