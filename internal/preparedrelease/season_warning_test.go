// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestPrepareWarnsAboutSourceSeasonsAcrossReuseAndRefresh(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	special := filepath.Join(root, "Example.Series.S00E01.mkv")
	for _, name := range []string{"Example.Series.S00E01.mkv", "Example.Series.S01E01.mkv", "Example.Series.S01E02.mkv"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store := newMemoryStore()
	collector := newClientEvidenceTestCollector(clientEvidenceTestSnapshot("synthetic"))
	module := newTestModule(t, store, collector)
	input := api.PrepareInput{SourcePath: root, Policy: api.PreparationPolicy{KeepFolder: true}}
	check := func(want bool) api.PrepareResult {
		t.Helper()
		result, err := module.Prepare(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		var warning string
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Code == "multiple_source_seasons" {
				warning = diagnostic.Message
			}
		}
		if (warning != "") != want {
			t.Fatalf("warning = %q, want present %v", warning, want)
		}
		if want && (!strings.Contains(warning, "S00, S01") || !strings.Contains(warning, "Example.Series.S00E01.mkv")) {
			t.Fatalf("missing season evidence: %q", warning)
		}
		return result
	}
	first := check(true)
	reused := check(true)
	if reused.Release.Generation != first.Release.Generation {
		t.Fatal("expected cached generation")
	}
	module = newTestModule(t, store, collector)
	check(true)
	season := "01"
	title := "Example Manual Title"
	input.Instructions.ReleaseName.Season = &season
	input.Instructions.Metadata.Title = &title
	check(true)
	if err := os.Remove(special); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestSourceSeasonWarningUsesBoundedConflictingFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := make([]string, 0, 30)
	for i := 1; i <= 20; i++ {
		files = append(files, filepath.Join(root, fmt.Sprintf("Example.S01E%02d.mkv", i)))
	}
	for i := 1; i <= 8; i++ {
		files = append(files, filepath.Join(root, "Extras", fmt.Sprintf("Example.S00E%02d.mkv", i)))
	}
	owned := envelope{result: api.PrepareResult{Release: api.PreparedRelease{Source: api.SourceManifest{SourcePath: root}}}, resources: preparationResources{fileList: files}}
	warnings := sourceSeasonDiagnostics(owned)
	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v", warnings)
	}
	message := warnings[0].Message
	if !strings.Contains(message, fmt.Sprintf("%q", filepath.Join("Extras", "Example.S00E01.mkv"))) || !strings.Contains(message, "3 additional season files omitted") {
		t.Fatalf("missing bounded relative evidence: %s", message)
	}
	if strings.Contains(message, root) || strings.Contains(message, "Example.S01E") || strings.Contains(message, "Example.S00E06") {
		t.Fatalf("unbounded or ordinary files in warning: %s", message)
	}
	owned.resources.fileList = files[:20]
	if got := sourceSeasonDiagnostics(owned); len(got) != 0 {
		t.Fatalf("single-season warning: %#v", got)
	}
	owned.resources.fileList = []string{files[0], files[20], files[20]}
	if got := sourceSeasonDiagnostics(owned); len(got) != 1 || strings.Count(got[0].Message, "Example.S00E01") != 1 {
		t.Fatalf("tie/duplicate warning = %#v", got)
	}
}

func TestSourceSeasonWarningRetainsTrackerRestriction(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := []string{filepath.Join(root, "Example.S00E01.mkv"), filepath.Join(root, "Example.S01E01.mkv")}
	owned := envelope{result: api.PrepareResult{Release: api.PreparedRelease{Source: api.SourceManifest{SourcePath: root}}}, resources: preparationResources{fileList: files}}
	if warnings := sourceSeasonDiagnostics(owned); len(warnings) != 1 {
		t.Fatalf("warnings = %#v", warnings)
	}
	subject := api.NewTrackerValidationSubject(api.UploadSubject{
		SourcePath:  root,
		FileList:    files,
		SeasonInt:   1,
		ReleaseName: "Example.S01",
	}, "BTN")
	failures := trackers.ValidateMultiSeasonPackage(subject.PackageFacts, trackers.EvidencePredicatePolicy{ViolationDisposition: api.RuleDispositionStrict})
	if len(failures) != 1 || failures[0].Disposition != api.RuleDispositionStrict {
		t.Fatalf("mixed-season restriction = %#v", failures)
	}
}

func TestSourceSeasonWarningSamplesAgainstKnownSeason(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := []string{filepath.Join(root, "Example.S01E01.mkv"), filepath.Join(root, "Example.S00E01.mkv"), filepath.Join(root, "Example.S00E02.mkv")}
	for _, test := range []struct {
		name    string
		episode api.EpisodeFacts
		sample  string
	}{
		{
			name:    "minority intended season",
			episode: api.EpisodeFacts{Season: 1, SeasonLabel: "S01"},
			sample:  "Example.S00E01",
		},
		{
			name:    "absent intended season",
			episode: api.EpisodeFacts{Season: 2, SeasonLabel: "S02"},
			sample:  "Example.S01E01",
		},
		{
			name:    "unknown season",
			episode: api.EpisodeFacts{},
			sample:  "Example.S01E01",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			owned := envelope{result: api.PrepareResult{Release: api.PreparedRelease{Source: api.SourceManifest{SourcePath: root}, Episode: test.episode}}, resources: preparationResources{fileList: files}}
			warnings := sourceSeasonDiagnostics(owned)
			if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "S00, S01") || !strings.Contains(warnings[0].Message, test.sample) {
				t.Fatalf("warnings = %#v", warnings)
			}
		})
	}
}
