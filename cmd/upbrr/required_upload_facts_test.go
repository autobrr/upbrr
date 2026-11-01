// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func requiredFactsCLIResult() releaseworkflow.CommandResult {
	return releaseworkflow.CommandResult{
		Workflow: api.ReleaseWorkflow{ID: "required-facts", Revision: 1},
		Release: &api.ReleaseSnapshot{Release: api.PreparedRelease{
			Generation: 1,
			Source:     api.SourceManifest{SourcePath: "example.mkv"},
			Identity:   api.ExternalIdentity{Category: api.CanonicalCategoryMovie},
			Media:      api.MediaFacts{Type: "ENCODE", Source: "BluRay"},
			Naming: api.NamingFacts{
				Type:       "ENCODE",
				Source:     "BluRay",
				Resolution: "720p",
			},
		}},
		FactInstructions: &api.ReleaseFactInstructionSnapshot{CorrectionRevision: 1},
		Selection:        &api.TrackerSelection{},
	}
}

func TestCLIUnattendedRequiredFactsDoesNotPromptOrUpload(t *testing.T) {
	for _, missing := range []string{"category", "type", "source", "resolution"} {
		t.Run(missing, func(t *testing.T) {
			args := []string{"--unattended", "example.mkv"}
			opts, visited, _, err := parseCLIOptions(args)
			if err != nil {
				t.Fatal(err)
			}
			current := requiredFactsCLIResult()
			switch missing {
			case "category":
				current.Release.Release.Identity.Category = api.CanonicalCategoryUnknown
			case "type":
				current.Release.Release.Media.Type = " \t"
			case "source":
				current.Release.Release.Media.Source = ""
			case "resolution":
				current.Release.Release.Naming.Resolution = " "
			}
			coreSvc := &cliWorkflowCoreFake{current: current}
			coreSvc.continueFn = func(api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) { return current, nil }
			var output strings.Builder
			_, err = runCLIWorkflowInteractive(t.Context(), coreSvc, args, opts, visited, "example.mkv", api.PlaylistInstruction{}, 0, config.Config{}, cliIO{in: strings.NewReader("y\ncontinue\n"), out: &output}, api.NopLogger{})
			if err == nil || !strings.Contains(err.Error(), "unattended") {
				t.Fatalf("missing facts error = %v", err)
			}
			if len(coreSvc.uploadRequests) != 0 || strings.Contains(output.String(), "Metadata correct?") || strings.Contains(output.String(), "Input args") {
				t.Fatalf("unattended missing facts prompted/uploaded: %q", output.String())
			}
			for _, field := range []string{"category", "type", "source", "resolution"} {
				if got := strings.Contains(output.String(), "--"+field+" <value>"); got != (field == missing) {
					t.Fatalf("wrong correction guidance: %q err=%v", output.String(), err)
				}
			}
		})
	}
}

func TestCLIRequiredFactsCorrectionCannotContinue(t *testing.T) {
	for _, mode := range []string{"interactive", "uac"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"example.mkv"}
			if mode == "uac" {
				args = []string{"--uac", "example.mkv"}
			}
			opts, visited, _, err := parseCLIOptions(args)
			if err != nil {
				t.Fatal(err)
			}
			initial := requiredFactsCLIResult()
			initial.Release.Release.Media.Type = ""
			coreSvc := &cliWorkflowCoreFake{current: initial}
			corrected := false
			coreSvc.continueFn = func(request api.ContinueReleaseWorkflowRequest) (releaseworkflow.CommandResult, error) {
				if facts := request.Intent.FactInstructions; facts != nil && facts.ReleaseName.Type != nil && *facts.ReleaseName.Type == "ENCODE" {
					coreSvc.current = requiredFactsCLIResult()
					coreSvc.current.Release.Release.Generation = 2
					corrected = true
				}
				return coreSvc.current, nil
			}
			var output strings.Builder
			_, err = runCLIWorkflowInteractive(t.Context(), coreSvc, args, opts, visited, "example.mkv", api.PlaylistInstruction{}, 0, config.Config{}, cliIO{in: strings.NewReader("y\ncontinue\n--type ENCODE\ny\n"), out: &output}, api.NopLogger{})
			if err != nil {
				t.Fatalf("correction: %v output=%q", err, output.String())
			}
			if !corrected || strings.Count(output.String(), "Missing fields") != 3 || strings.Count(output.String(), "Metadata correct?") != 1 {
				t.Fatalf("missing facts bypass or failed correction: %q", output.String())
			}
		})
	}
}

func TestMetadataPreviewUsesActualUploadFacts(t *testing.T) {
	release := requiredFactsCLIResult().Release.Release
	release.Naming.Type, release.Naming.Source = "REMUX", "DVD"
	var output strings.Builder
	printMetadataPreview(&output, api.MetadataPreview{}, release, false)
	if !strings.Contains(output.String(), "Type: ENCODE\nResolution: 720p\nSource: BluRay\n") || strings.Contains(output.String(), "REMUX") || strings.Contains(output.String(), "DVD") {
		t.Fatalf("preview substituted naming evidence: %q", output.String())
	}
}
