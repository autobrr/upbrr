// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReusableMediaMaterializationRestoresDecodedFormat(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "unknown"} {
		for _, alreadyOwned := range []bool{false, true} {
			name := format + "/copied"
			if alreadyOwned {
				name = format + "/already_owned"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				cfg := config.Config{MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(root, "workflow.db")}}
				binding := api.PreparedMediaBinding{
					SourcePath:       filepath.Join(root, "absent-source.mkv"),
					CompatibilityKey: api.MediaCompatibilityKey(workflowMediaTestSHA256("compatible source")),
				}
				directory := root
				if alreadyOwned {
					tmpRoot, err := db.Subdir(cfg.MainSettings.DBPath, "tmp")
					if err != nil {
						t.Fatal(err)
					}
					directory, err = reusableMediaTempRoot(tmpRoot, binding.SourcePath)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(directory, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				filename := "synthetic.png"
				if format == "png" {
					filename = "synthetic.jpg"
				}
				pathValue := filepath.Join(directory, filename)
				payload := reusableFormatEvidenceBytes(t, format)
				if err := os.WriteFile(pathValue, payload, 0o600); err != nil {
					t.Fatal(err)
				}
				shot := api.ScreenshotImage{
					Path:    pathValue,
					Purpose: api.ScreenshotPurposeFinal,
					Width:   1200,
					Height:  700,
				}
				wantFormat := format
				if format == "unknown" {
					shot.Format, wantFormat = "png", ""
				}
				fingerprint, err := workflowMediaReuseCaptureFingerprint(cfg, binding.CompatibilityKey, api.MediaArtifactScreenshot, shot)
				if err != nil {
					t.Fatal(err)
				}
				builder := workflowMediaBuilder{config: cfg}
				got, err := builder.materializeReusableMediaImage(t.Context(), binding, shot, fingerprint, workflowMediaTestSHA256(string(payload)))
				if err != nil {
					t.Fatal(err)
				}
				if got.Format != wantFormat || got.Width != shot.Width || got.Height != shot.Height {
					t.Fatalf("reused evidence = format %q, %dx%d", got.Format, got.Width, got.Height)
				}
				after, err := workflowMediaReuseCaptureFingerprint(cfg, binding.CompatibilityKey, api.MediaArtifactScreenshot, got)
				if err != nil || after != fingerprint {
					t.Fatalf("reinspection changed capture identity: err=%v", err)
				}
				artifact := publicMediaArtifact(fingerprint, "restored-screenshot", 0, api.MediaArtifactScreenshot, api.ScreenshotPurposeFinal, got)
				exact, err := resolveWorkflowExactMedia(workflowMediaPrivateArtifacts{Screenshots: []api.ScreenshotImage{got}},
					api.MediaArtifactSet{Artifacts: []api.MediaArtifact{artifact}})
				if err != nil || len(exact.Screenshots) != 1 || exact.Screenshots[0].Format != wantFormat {
					t.Fatalf("exact media lost restored evidence: err=%v", err)
				}
			})
		}
	}
}

func reusableFormatEvidenceBytes(t *testing.T, format string) []byte {
	t.Helper()
	var payload bytes.Buffer
	var err error
	source := image.NewRGBA(image.Rect(0, 0, 19, 11))
	switch format {
	case "png":
		err = png.Encode(&payload, source)
	case "jpeg":
		err = jpeg.Encode(&payload, source, nil)
	default:
		return []byte("not an image")
	}
	if err != nil {
		t.Fatal(err)
	}
	return payload.Bytes()
}
