// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package architecturepolicy

import "testing"

func TestStructuredNameCallbackRejectsWrongOwner(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "internal/trackers/impl/standalone/example/profile.go", `package example
func customize(e *trackers.NameEditor, meta api.UploadSubject) error {
 return e.Set(api.NameRoleTitle, meta.Title)
}
`)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	assertViolationContains(t, violations, "structured naming callbacks belong in name.go")
}

func TestStructuredNameCallbackRejectsRenderedNameAccess(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "internal/trackers/impl/standalone/example/name.go", `package example
func customize(e *trackers.NameEditor, meta api.UploadSubject) error {
 name := strings.ReplaceAll(meta.ReleaseName, meta.Edition, "")
 return e.Set(api.NameRoleTitle, name)
}
`)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	assertViolationContains(t, violations, "structured naming callbacks must target components")
}

func TestStructuredNameCallbackAllowsSelectedFactFormatting(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, "internal/trackers/impl/standalone/example/name.go", `package example
func customize(e *trackers.NameEditor, meta api.UploadSubject) error {
 return e.Set(api.NameRoleAudio, strings.ReplaceAll(meta.Audio, "DD+", "DDP"))
}
`)
	violations, err := CheckRepository(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("unexpected violations: %+v", violations)
	}
}
