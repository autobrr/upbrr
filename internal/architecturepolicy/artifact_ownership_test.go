// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package architecturepolicy

import "testing"

func TestReleaseArtifactOwnership(t *testing.T) {
	for _, test := range []struct {
		name, source string
		invalid      bool
	}{
		{"independent feature root", `func write(dbPath string) { root,_:=db.Subdir(dbPath,"tmp"); dir:=filepath.Join(root,"renamed-cache",key); os.MkdirAll(dir,0700) }`, true},
		{"request root", `func write(req Request) { dir:=filepath.Join(req.TempRoot,"other-feature"); os.WriteFile(dir,data,0600) }`, true},
		{"aliased root", `func write(tmpRoot string) { var renamed=tmpRoot; os.MkdirTemp(renamed,"different-cache-") }`, true},
		{"direct database tmp", `func write(dbPath string) { os.MkdirAll(filepath.Join(filepath.Dir(dbPath),"tmp","renamed-cache"),0700) }`, true},
		{"nested database tmp", `func write(cfg Config) { dir:=filepath.Join(filepath.Join(filepath.Dir(cfg.MainSettings.DBPath),"tmp"),"renamed-cache"); os.MkdirAll(dir,0700) }`, true},
		{"owned nested directory", `func write(tmpRoot,source string) { owner,_,_:=paths.ReleaseTempDirFor(tmpRoot,source,release); dir:=filepath.Join(owner,"analysis",generation);os.MkdirAll(dir,0700) }`, false},
		{"owned nested tmp", `func write(dbPath,source string) { root:=filepath.Join(filepath.Dir(dbPath),"tmp"); owner,_,_:=paths.ReleaseTempDirFor(root,source,release); os.MkdirAll(filepath.Join(owner,"tmp","scratch"),0700) }`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writePolicyFixture(t, root, "internal/example/artifacts.go", "package example\n"+test.source)
			violations, err := CheckRepository(root)
			if err != nil {
				t.Fatal(err)
			}
			if test.invalid {
				assertViolationContains(t, violations, "release artifact roots must come from")
			} else if len(violations) > 0 {
				t.Fatal(violations)
			}
		})
	}
}
