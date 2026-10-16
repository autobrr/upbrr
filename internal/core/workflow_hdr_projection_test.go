// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/hdranalysis"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRFreshV3LegacyPicturesAndSourceFreeReplay(t *testing.T) {
	if os.Getenv("UPBRR_HDR_DECODE_CALIBRATION") != "1" {
		t.Skip("requires calibrated actual decoder")
	}
	for _, automatic := range []bool{false, true} {
		name := "manual"
		if automatic {
			name = "automatic"
		}
		t.Run(name, func(t *testing.T) {
			builder, resolver, _ := hdrFixture(t)
			data, err := os.ReadFile(filepath.Join("..", "services", "hdranalysis", "testdata", "decoded-pq-cuts-25.mkv"))
			if err != nil {
				t.Fatal(err)
			}
			source, reference := filepath.Join(t.TempDir(), "Synthetic.Primary.mkv"), filepath.Join(t.TempDir(), "Synthetic.Reference.mkv")
			for _, path := range []string{source, reference} {
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			facts, err := hdranalysis.InspectFileSource(t.Context(), source)
			if err != nil {
				t.Fatal(err)
			}
			target := &resolver.subject.Targets[0]
			target.Path, target.DiscRoot, target.Target.Evidence = source, "", facts
			verified, err := releaseworkflow.VerifyHDRComparisonSource(t.Context(), reference, verifyWorkflowInput)
			if err != nil {
				t.Fatal(err)
			}
			request := api.HDRAnalysisInstructions{
				Release:        resolver.subject.Release,
				TargetIDs:      []string{target.Target.ID},
				ProfileVersion: api.HDRAnalysisProfileV3,
				Decoded: &api.HDRDecodedOptions{
					Automatic:  automatic,
					SceneSync:  !automatic,
					ActiveArea: !automatic,
					Comparison: &api.HDRComparisonOptions{
						SourceID:    "reference",
						FrameOffset: new(0),
						Crop: api.HDRCrop{
							Top:    12,
							Width:  192,
							Height: 88,
						},
						Grade:  true,
						Colour: true,
					},
				},
			}
			if request.Decoded.RequiresV3() {
				t.Fatal("regression requires legacy options in explicit v3 envelope")
			}
			entries := make(map[api.HDRExtractionID]releaseworkflow.HDRExtractionRecord)
			resources := make(map[api.HDRExtractionID]releaseworkflow.RetainedHDRExtractionResource)
			publish := func(record releaseworkflow.HDRExtractionRecord, resource releaseworkflow.RetainedHDRExtractionResource) error {
				entries[record.ID], resources[record.ID] = record, resource
				return nil
			}
			store := releaseworkflow.HDRMeasurementStore{
				Records:   make(map[string]releaseworkflow.HDRMeasurementRecord),
				Resources: make(map[string]releaseworkflow.RetainedHDRMeasurementResource),
				Comparison: &releaseworkflow.HDRComparisonSource{
					Ref: api.HDRComparisonSourceRef{
						ID:          "reference",
						Release:     request.Release,
						Fingerprint: verified.SourceVersion,
					},
					Path:     verified.CanonicalPath,
					Evidence: facts,
				},
			}
			store.Publish = func(record releaseworkflow.HDRMeasurementRecord, resource releaseworkflow.RetainedHDRMeasurementResource) error {
				if _, err := restoreHDRMeasurement(t.Context(), record, resource); err != nil {
					return err
				}
				store.Records[record.ID], store.Resources[record.ID] = record, resource
				return nil
			}
			var prior *api.HDRAnalysisResult
			count := 0
			for _, phase := range []string{"fresh", "source-free"} {
				if phase == "source-free" {
					for _, path := range []string{source, reference} {
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					}
				}
				result, _, err := builder.Build(t.Context(), releaseworkflow.HDRPreparation{Release: request.Release}, request,
					name+"-"+phase, time.Now(), prior, nil, entries, resources, publish, &store)
				if err != nil {
					t.Fatal(err)
				}
				result.ID, result.WorkflowID, result.Revision = "analysis", "workflow", 1
				if err := result.Validate(); err != nil {
					t.Fatalf("%s invalid v3 envelope: %v", phase, err)
				}
				measuredPictures := 0
				for _, decoded := range result.Targets[0].Decoded {
					if decoded.Mode != api.HDRDecodedAudio && decoded.Measured > 0 {
						measuredPictures++
						if decoded.Coverage == "" || len(decoded.Artifacts) == 0 {
							t.Fatalf("lost picture output: %+v", decoded)
						}
					}
				}
				if measuredPictures == 0 {
					t.Fatal("fixture did not measure any pictures")
				}
				for _, artifact := range result.Targets[0].Artifacts {
					if artifact.Profile != api.HDRAnalysisProfileV3 {
						t.Fatalf("legacy artifact in v3: %+v", artifact)
					}
				}
				if phase == "source-free" && len(store.Records) != count {
					t.Fatal("source-free replay published fresh measurements")
				}
				count = len(store.Records)
				prior = &result
			}
		})
	}
}
