// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNameEditorPresentRolesReflectsEditsWithoutExposingDocument(t *testing.T) {
	subject := structuredSubject()
	document := subject.GeneratedName.Clone()
	for index := range document.Components {
		if document.Components[index].Role == api.NameRoleEdition {
			document.Components[index].Manual = true
			document.Components[index].Present = false
		}
	}
	editor := &NameEditor{document: document}
	if err := editor.Include(api.NameRoleEdition); err != nil {
		t.Fatal(err)
	}
	if err := editor.Omit(api.NameRoleAlternateTitle); err != nil {
		t.Fatal(err)
	}
	if err := editor.InsertBefore(api.NameRoleLanguageMarker, "FRENCH", api.NameRoleResolution); err != nil {
		t.Fatal(err)
	}
	roles := editor.PresentRoles()
	if slices.Contains(roles, api.NameRoleEdition) || slices.Contains(roles, api.NameRoleAlternateTitle) {
		t.Fatalf("absent or protected roles appeared: %v", roles)
	}
	index := slices.Index(roles, api.NameRoleLanguageMarker)
	if index < 0 || index+1 >= len(roles) || roles[index+1] != api.NameRoleResolution {
		t.Fatalf("inserted role order = %v", roles)
	}
	roles[0] = api.NameRoleGroup
	if editor.PresentRoles()[0] != api.NameRoleTitle {
		t.Fatal("returned roles alias the editor document")
	}
}

func TestNameEditorMoveAfterPreservesAuthority(t *testing.T) {
	for _, mandatory := range []bool{false, true} {
		document := &api.ReleaseNameDocument{Version: api.ReleaseNameDocumentVersionV1, Components: []api.ReleaseNameComponent{
			{
				Role:    api.NameRoleTitle,
				Value:   "Example",
				Present: true,
			},
			{
				Role:    api.NameRoleYear,
				Value:   "2001",
				Present: true,
				Manual:  true,
			},
			{
				Role:    api.NameRoleEdition,
				Value:   "Uncut",
				Present: true,
			},
		}}
		editor := &NameEditor{document: document, mandatory: mandatory}
		if err := editor.MoveAfter(api.NameRoleYear, api.NameRoleEdition); mandatory {
			if err == nil {
				t.Fatal("mandatory move accepted undeclared order authority")
			}
			editor.authority = []NameAuthority{{Role: api.NameRoleYear, Aspect: NameOrder}}
			if err := editor.MoveAfter(api.NameRoleYear, api.NameRoleEdition); err != nil {
				t.Fatal(err)
			}
			if editor.PresentRoles()[2] != api.NameRoleYear || len(editor.decisions) != 1 {
				t.Fatal("mandatory move did not move manual year and report decision")
			}
		} else if err != nil || editor.PresentRoles()[1] != api.NameRoleYear {
			t.Fatalf("optional move changed manual year: %v", err)
		}
	}
	editor := &NameEditor{document: structuredSubject().GeneratedName}
	if err := editor.MoveAfter(api.NameRoleTitle, api.NameRoleGroup); err != nil {
		t.Fatal(err)
	}
	roles := editor.PresentRoles()
	if roles[len(roles)-1] != api.NameRoleTitle {
		t.Fatal("move to final position failed")
	}
	if err := editor.MoveAfter(api.NameRoleTitle, api.NameRoleYear); err != nil {
		t.Fatal(err)
	}
	roles = editor.PresentRoles()
	if roles[slices.Index(roles, api.NameRoleYear)+1] != api.NameRoleTitle {
		t.Fatal("move from later position failed")
	}
}

func TestStructuredOpaqueFailureExplainsCause(t *testing.T) {
	for _, cause := range []string{"requested", "missing", "scene"} {
		t.Run(cause, func(t *testing.T) {
			subject := structuredSubject()
			input := ReleaseNameInput{Subject: subject}
			want := "clear the name override and reprepare"
			switch cause {
			case "requested":
				input.RequestedName = new("Opaque Name-GRP")
			case "missing":
				input.Subject.GeneratedName = nil
				want = "components are unavailable; reprepare"
			case "scene":
				input.Subject.Scene = true
				input.Subject.SceneName = "Exact.Scene.Name-GRP"
				want = "must explicitly support rebuilding scene names"
			}
			binding := StructuredReleaseNamePolicy("example/opaque/v1", StructuredNamePolicy{
				Mandatory: func(editor *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
					return editor.Omit(api.NameRoleEdition)
				},
				Authority: []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}},
			})
			_, err := resolveStructuredNames(input, binding)
			var rule *NameRuleError
			if !errors.As(err, &rule) || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "()") {
				t.Fatalf("opaque %s error = %v", cause, err)
			}
		})
	}
}

func TestStructuredSearchReceivesPresentationCopy(t *testing.T) {
	for _, requested := range []*string{nil, new("Requested.Name-GRP")} {
		subject := structuredSubject()
		subject.SeasonInt = 3
		subject.EpisodeInt = 4
		subject.Release.Year = 2026
		subject.NamePresentation = api.ReleaseNamePresentation{
			Version:           api.ReleaseNamePresentationVersionV1,
			OmitSeasonEpisode: true,
			OmitYear:          true,
		}
		policy := StructuredReleaseNamePolicy("test/search-presentation/v1", StructuredNamePolicy{
			Defaults: func(_ *NameEditor, facts api.UploadSubject, _ config.TrackerConfig) error {
				if facts.SeasonInt != 3 || facts.Release.Year != 2026 {
					t.Fatal("presentation changed canonical policy facts")
				}
				return nil
			},
			Search: func(facts api.UploadSubject, _ config.TrackerConfig) string {
				if requested == nil {
					if facts.SeasonInt != 0 || facts.EpisodeInt != 0 || facts.Release.Year != 0 {
						t.Fatal("automatic search ignored presentation")
					}
				} else if facts.SeasonInt != 3 || facts.EpisodeInt != 4 || facts.Release.Year != 2026 {
					t.Fatal("requested resolver lost historical search bypass")
				}
				return "independent search"
			},
		})
		resolved, err := resolveReleaseNames(PreparationInput{Meta: subject, RequestedUploadName: requested}, policy)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Duplicate != "independent search" || subject.SeasonInt != 3 || subject.Release.Year != 2026 {
			t.Fatalf("resolution mutated input: %#v", resolved)
		}
	}
}

func structuredSubject() api.UploadSubject {
	doc := &api.ReleaseNameDocument{Version: api.ReleaseNameDocumentVersionV1, Components: []api.ReleaseNameComponent{
		{
			Role:    api.NameRoleTitle,
			Value:   "The Uncut Signal",
			Present: true,
			Join:    " ",
		},
		{
			Role:    api.NameRoleYear,
			Value:   "2026",
			Present: true,
			Join:    " ",
		},
		{
			Role:    api.NameRoleAlternateTitle,
			Value:   "AKA Uncut Nights",
			Present: true,
			Join:    " ",
		},
		{
			Role:    api.NameRoleEpisodeTitle,
			Value:   "The Uncut Version",
			Present: true,
			Join:    " ",
		},
		{
			Role:           api.NameRoleEdition,
			Value:          "Uncut",
			AvailableValue: "Uncut",
			Present:        true,
			Join:           " ",
		},
		{
			Role:    api.NameRoleResolution,
			Value:   "576p",
			Present: true,
			Join:    " ",
		},
		{
			Role:    api.NameRoleSource,
			Value:   "DVD",
			Present: true,
			Join:    " ",
		},
		{
			Role:    api.NameRoleAudio,
			Value:   "AC3 2.0",
			Present: true,
			Join:    " ",
		},
		{
			Role:    api.NameRoleVideoCodec,
			Value:   "x264",
			Present: true,
			Join:    " ",
		},
		{
			Role:    api.NameRoleGroup,
			Value:   "-GRP",
			Present: true,
			Join:    "",
		},
	}}
	return api.UploadSubject{GeneratedName: doc, ReleaseName: doc.Render().Name}
}

func TestStructuredNameTargetsRolesAndCopiesDocument(t *testing.T) {
	subject := structuredSubject()
	before := subject.GeneratedName.Clone()
	binding := StructuredReleaseNamePolicy("test/structured/v1", StructuredNamePolicy{Defaults: func(e *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
		if err := e.Omit(api.NameRoleEdition); err != nil {
			return err
		}
		if err := e.MoveBefore(api.NameRoleAlternateTitle, api.NameRoleYear); err != nil {
			return err
		}
		return e.Set(api.NameRoleSource, "DVDRip")
	}})
	got, err := resolveReleaseNames(PreparationInput{Meta: subject}, binding)
	if err != nil {
		t.Fatal(err)
	}
	want := "The Uncut Signal AKA Uncut Nights 2026 The Uncut Version 576p DVDRip AC3 2.0 x264-GRP"
	if got.Upload != want || got.Duplicate != want {
		t.Fatalf("names = %+v; want %q", got, want)
	}
	if !reflect.DeepEqual(before, subject.GeneratedName) {
		t.Fatal("policy mutated canonical document")
	}
}

func TestStructuredNameManualAndMandatoryPresence(t *testing.T) {
	for _, mandatory := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "mandatory"}[mandatory], func(t *testing.T) {
			subject := structuredSubject()
			for i := range subject.GeneratedName.Components {
				c := &subject.GeneratedName.Components[i]
				if c.Role == api.NameRoleEdition {
					c.Value = ""
					c.Present = false
					c.Manual = true
				}
			}
			subject.ReleaseName = subject.GeneratedName.Render().Name
			callback := func(e *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
				return e.Include(api.NameRoleEdition)
			}
			policy := StructuredNamePolicy{Defaults: callback}
			if mandatory {
				policy = StructuredNamePolicy{Mandatory: callback, Authority: []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}}}
			}
			got, err := resolveReleaseNames(PreparationInput{Meta: subject}, StructuredReleaseNamePolicy("test/authority/v1", policy))
			if err != nil {
				t.Fatal(err)
			}
			want := subject.ReleaseName
			if mandatory {
				want = structuredSubject().ReleaseName
			}
			if got.Upload != want {
				t.Fatalf("got %q want %q", got.Upload, want)
			}
			if notices := namingOverrideDecisions(got.Decisions); mandatory && (len(notices) != 1 || notices[0].NamingRole != "edition") {
				t.Fatalf("missing authority notice: %+v", got.Decisions)
			}
		})
	}
}

func TestStructuredNameOpaqueAuthority(t *testing.T) {
	for _, mode := range []OpaqueNameMode{OpaqueNameReject, OpaqueNameRebuild} {
		t.Run(string(mode), func(t *testing.T) {
			policy := StructuredNamePolicy{
				Opaque:    mode,
				Authority: []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}},
				Mandatory: func(e *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
					return e.Omit(api.NameRoleEdition)
				},
			}
			requested := "Uncut Uncut 2026-GRP"
			got, err := resolveReleaseNames(PreparationInput{Meta: structuredSubject(), RequestedUploadName: &requested}, StructuredReleaseNamePolicy("test/opaque/v1", policy))
			if mode == OpaqueNameReject {
				if _, ok := errors.AsType[*NameRuleError](err); !ok {
					t.Fatalf("expected typed rejection, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if notices := namingOverrideDecisions(got.Decisions); got.Upload == requested || len(notices) != 2 || notices[0].Decision != "rebuilt" {
				t.Fatalf("rebuild result: %+v", got)
			}
		})
	}
	requested := "Exact.Uncut.Name-GRP"
	got, err := resolveReleaseNames(PreparationInput{Meta: structuredSubject(), RequestedUploadName: &requested}, StructuredReleaseNamePolicy("test/opaque/v1", StructuredNamePolicy{}))
	if err != nil || got.Upload != requested {
		t.Fatalf("opaque default = %+v, %v", got, err)
	}
}

func TestStructuredPolicyAuthorityChangesInvalidateReviewedNames(t *testing.T) {
	policy := StructuredNamePolicy{}
	binding := StructuredReleaseNamePolicy("test/authority/v1", policy)
	input, failure := PrepareInputWithReleaseNamePolicy(PreparationInput{Tracker: "EXAMPLE", Meta: structuredSubject()}, binding)
	if failure != nil {
		t.Fatal(failure)
	}
	policy.Authority = []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}}
	policy.Mandatory = func(*NameEditor, api.UploadSubject, config.TrackerConfig) error { return nil }
	if _, failure = PrepareInputWithReleaseNamePolicy(input, StructuredReleaseNamePolicy(binding.ID, policy)); failure == nil {
		t.Fatal("changed authority accepted a previously reviewed identical name")
	}
}

func TestStructuredDVLStyleInsertionPreservesTitleAndAnchorCollision(t *testing.T) {
	subject := structuredSubject()
	subject.GeneratedName.Components[0].Value = "FRENCH 576p The Uncut Signal"
	subject.ReleaseName = subject.GeneratedName.Render().Name
	binding := StructuredReleaseNamePolicy("test/dvl/v1", StructuredNamePolicy{Defaults: func(e *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
		return e.InsertBefore(api.NameRoleLanguageMarker, "FRENCH", api.NameRoleResolution)
	}})
	got, err := resolveReleaseNames(PreparationInput{Meta: subject}, binding)
	if err != nil {
		t.Fatal(err)
	}
	want := "FRENCH 576p The Uncut Signal 2026 AKA Uncut Nights The Uncut Version Uncut FRENCH 576p DVD AC3 2.0 x264-GRP"
	if got.Upload != want {
		t.Fatalf("got %q want %q", got.Upload, want)
	}
}

func TestStructuredProviderYearChangesOnlyYearRole(t *testing.T) {
	subject := structuredSubject()
	subject.Identity.Category = api.CanonicalCategoryMovie
	subject.ProviderMetadata = api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Year: 2025}}
	subject.GeneratedName.Components[0].Value = "The 2026 Signal"
	subject.GeneratedName.Components[2].Value = "AKA 2026 Nights"
	subject.ReleaseName = subject.GeneratedName.Render().Name
	binding := WithMovieYearProvider(StructuredReleaseNamePolicy("test/year/v1", StructuredNamePolicy{}), api.IdentityProviderTMDB)
	got, err := resolveReleaseNames(PreparationInput{Meta: subject}, binding)
	if err != nil {
		t.Fatal(err)
	}
	want := "The 2026 Signal 2025 AKA 2026 Nights The Uncut Version Uncut 576p DVD AC3 2.0 x264-GRP"
	if got.Upload != want {
		t.Fatalf("got %q want %q", got.Upload, want)
	}
	for i := range subject.GeneratedName.Components {
		if subject.GeneratedName.Components[i].Role == api.NameRoleYear {
			subject.GeneratedName.Components[i].Manual = true
		}
	}
	got, err = resolveReleaseNames(PreparationInput{Meta: subject}, binding)
	if err != nil || got.Upload != subject.ReleaseName {
		t.Fatalf("manual year not preserved: %+v %v", got, err)
	}
}

func TestStructuredNameAuthorityRejectsUndeclaredAndUnavailable(t *testing.T) {
	policy := StructuredNamePolicy{Authority: []NameAuthority{{Role: api.NameRoleEdition, Aspect: NamePresence}}, Mandatory: func(e *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
		return e.Set(api.NameRoleTitle, "Replacement")
	}}
	if _, err := resolveReleaseNames(PreparationInput{Meta: structuredSubject()}, StructuredReleaseNamePolicy("test/authority/v1", policy)); err == nil {
		t.Fatal("undeclared value change accepted")
	}
	policy.Mandatory = func(e *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
		return e.Include(api.NameRoleEdition)
	}
	subject := structuredSubject()
	for i := range subject.GeneratedName.Components {
		c := &subject.GeneratedName.Components[i]
		if c.Role == api.NameRoleEdition {
			c.Value = ""
			c.AvailableValue = ""
			c.Present = false
		}
	}
	subject.ReleaseName = subject.GeneratedName.Render().Name
	if _, err := resolveReleaseNames(PreparationInput{Meta: subject}, StructuredReleaseNamePolicy("test/authority/v1", policy)); err == nil {
		t.Fatal("missing mandatory value accepted")
	}
}

func TestStructuredNameRejectsUnknownAuthority(t *testing.T) {
	policy := StructuredNamePolicy{Authority: []NameAuthority{{Role: "invented", Aspect: NamePresence}}, Mandatory: func(*NameEditor, api.UploadSubject, config.TrackerConfig) error { return nil }}
	if err := validateReleaseNamePolicy(StructuredReleaseNamePolicy("test/invalid/v1", policy)); err == nil {
		t.Fatal("unknown role accepted")
	}
}

func TestStructuredNameDefaultOperationsRejectUnknownRoles(t *testing.T) {
	for name, operation := range map[string]func(*NameEditor) error{
		"omit":          func(e *NameEditor) error { return e.Omit("unknown") },
		"include":       func(e *NameEditor) error { return e.Include("unknown") },
		"set":           func(e *NameEditor) error { return e.Set("unknown", "value") },
		"move target":   func(e *NameEditor) error { return e.MoveBefore("unknown", api.NameRoleTitle) },
		"move anchor":   func(e *NameEditor) error { return e.MoveBefore(api.NameRoleEdition, "unknown") },
		"insert target": func(e *NameEditor) error { return e.InsertBefore("unknown", "value", api.NameRoleTitle) },
		"insert anchor": func(e *NameEditor) error { return e.InsertBefore(api.NameRoleEdition, "value", "unknown") },
	} {
		t.Run(name, func(t *testing.T) {
			binding := StructuredReleaseNamePolicy("test/unknown/v1", StructuredNamePolicy{
				Defaults: func(e *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error { return operation(e) },
			})
			_, err := resolveReleaseNames(PreparationInput{Meta: structuredSubject()}, binding)
			if failure, ok := errors.AsType[*NameRuleError](err); !ok || failure.Rule != binding.ID || failure.Role != "unknown" {
				t.Fatalf("expected unknown-role rule error, got %v", err)
			}
		})
	}
}

func TestStructuredNameOmissionPreservesEpisodeAttachments(t *testing.T) {
	for _, test := range []struct {
		name string
		omit []api.ReleaseNameRole
		want string
	}{
		{"season", []api.ReleaseNameRole{api.NameRoleSeason}, "Example Show 2026 E023D PAL DVD9-GRP"},
		{"episode", []api.ReleaseNameRole{api.NameRoleEpisode}, "Example Show 2026 S013D PAL DVD9-GRP"},
		{"both", []api.ReleaseNameRole{api.NameRoleSeason, api.NameRoleEpisode}, "Example Show 2026 3D PAL DVD9-GRP"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := &api.ReleaseNameDocument{Version: api.ReleaseNameDocumentVersionV1, Components: []api.ReleaseNameComponent{
				{
					Role:    api.NameRoleTitle,
					Value:   "Example Show",
					Present: true,
					Join:    " ",
				},
				{
					Role:    api.NameRoleYear,
					Value:   "2026",
					Present: true,
					Join:    " ",
				},
				{
					Role:    api.NameRoleSeason,
					Value:   "S01",
					Present: true,
					Join:    " ",
				},
				{
					Role:     api.NameRoleEpisode,
					Value:    "E02",
					Present:  true,
					Join:     " ",
					AttachTo: []api.ReleaseNameRole{api.NameRoleSeason},
				},
				{
					Role:     api.NameRoleThreeD,
					Value:    "3D",
					Present:  true,
					Join:     " ",
					AttachTo: []api.ReleaseNameRole{api.NameRoleEpisode, api.NameRoleSeason},
				},
				{
					Role:    api.NameRoleDVDSystem,
					Value:   "PAL",
					Present: true,
					Join:    " ",
				},
				{
					Role:    api.NameRoleDVDSize,
					Value:   "DVD9",
					Present: true,
					Join:    " ",
				},
				{
					Role:    api.NameRoleGroup,
					Value:   "-GRP",
					Present: true,
				},
			}}
			subject := api.UploadSubject{GeneratedName: document, ReleaseName: document.Render().Name}
			binding := StructuredReleaseNamePolicy("test/episode/v1", StructuredNamePolicy{
				Defaults: func(e *NameEditor, _ api.UploadSubject, _ config.TrackerConfig) error {
					for _, role := range test.omit {
						if err := e.Omit(role); err != nil {
							return err
						}
					}
					return nil
				},
			})
			got, err := resolveReleaseNames(PreparationInput{Meta: subject}, binding)
			if err != nil || got.Upload != test.want {
				t.Fatalf("omission = %q, %v; want %q", got.Upload, err, test.want)
			}
			if document.Render().Name != "Example Show 2026 S01E023D PAL DVD9-GRP" {
				t.Fatal("policy mutated canonical episode components")
			}
		})
	}
}
