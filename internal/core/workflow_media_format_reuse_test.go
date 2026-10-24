// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestReusableMediaMaterializationRestoresDecodedFormat(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "unknown", "truncated"} {
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
				if format == "unknown" || format == "truncated" {
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
	case "png", "truncated":
		err = png.Encode(&payload, source)
	case "jpeg":
		err = jpeg.Encode(&payload, source, nil)
	default:
		return []byte("not an image")
	}
	if err != nil {
		t.Fatal(err)
	}
	if format == "truncated" {
		return payload.Bytes()[:33]
	}
	return payload.Bytes()
}

func TestWorkflowMediaHydrationReinspectsFormatWithoutChangingReferences(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "truncated.png")
	if err := os.WriteFile(path, reusableFormatEvidenceBytes(t, "truncated"), 0o600); err != nil {
		t.Fatal(err)
	}
	original := api.ScreenshotImage{
		Path:   path,
		Format: "png",
		RawURL: "https://example.invalid/synthetic.png",
		Width:  19,
		Height: 11,
	}
	payload, err := json.Marshal(persistedWorkflowMediaArtifacts{
		Screenshots:    []api.ScreenshotImage{original},
		DVDMenus:       []api.DVDMenuCaptureImage{{ScreenshotImage: original}},
		ArtifactImages: map[api.PublicResourceID]api.ScreenshotImage{"shot": original},
	})
	if err != nil {
		t.Fatal(err)
	}
	builder := workflowMediaBuilder{media: &mediaModule{}}
	var codec releaseworkflow.PrivateResourceCodec
	for _, candidate := range workflowPrivateResourceCodecs(builder, workflowAudioAnalysisBuilder{}) {
		if candidate.Kind == workflowPrivateResourceKindMedia {
			codec = candidate
		}
	}
	decoded, err := codec.Decode(payload)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, ok := decoded.(workflowMediaPrivateArtifacts)
	if !ok {
		t.Fatalf("resource type %T", decoded)
	}
	want := original
	want.Format = ""
	if claim, found := artifacts.legacyImageFormats[path]; !found || claim.Format != "png" || claim.Conflicting {
		t.Fatalf("hydration lost exact old label: %+v", artifacts.legacyImageFormats)
	}
	if len(artifacts.Screenshots) != 1 || artifacts.Screenshots[0] != want || artifacts.ArtifactImages["shot"] != want || len(artifacts.DVDMenus) != 1 || artifacts.DVDMenus[0].ScreenshotImage != want {
		t.Fatalf("hydration changed references or trusted stale format: %+v", artifacts)
	}
	released, err := codec.DecodeForRelease(payload)
	if err != nil {
		t.Fatal(err)
	}
	cleanup, ok := released.(workflowMediaPrivateArtifacts)
	if !ok || cleanup.Screenshots[0] != original || cleanup.DVDMenus[0].ScreenshotImage != original {
		t.Fatalf("cleanup inspected or changed resource: %+v", released)
	}
	vaultPath := filepath.Join(root, "vault")
	firstVault, err := releaseworkflow.NewPrivateArtifactVault(vaultPath, codec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := firstVault.Put("owner", "workflow", "media", cleanup, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	restartedVault, err := releaseworkflow.NewPrivateArtifactVault(vaultPath, codec)
	if err != nil {
		t.Fatal(err)
	}
	first, err := restartedVault.Get("owner", "workflow", "media", now)
	if err != nil {
		t.Fatal(err)
	}
	firstMedia, ok := first.(workflowMediaPrivateArtifacts)
	if !ok || firstMedia.Screenshots[0] != want {
		t.Fatalf("restart trusted legacy evidence: %+v", first)
	}
	if err := os.WriteFile(path, reusableFormatEvidenceBytes(t, "png"), 0o600); err != nil {
		t.Fatal(err)
	}
	cached, err := restartedVault.Get("owner", "workflow", "media", now)
	if err != nil {
		t.Fatal(err)
	}
	cachedMedia, ok := cached.(workflowMediaPrivateArtifacts)
	if !ok || cachedMedia.Screenshots[0] != want {
		t.Fatalf("cached poll repeated inspection: %+v", cached)
	}
}

func TestWorkflowLegacyImageFormatsRemainPrivateAndConservative(t *testing.T) {
	var original workflowMediaPrivateArtifacts
	recordLegacyImageFormat(&original, "blank.png", "")
	recordLegacyImageFormat(&original, "known.png", "png")
	cloned := cloneWorkflowMediaPrivateArtifacts(original)
	recordLegacyImageFormat(&cloned, "blank.png", "png")
	recordLegacyImageFormat(&cloned, "blank.png", "")
	recordLegacyImageFormat(&cloned, "new.png", "jpeg")
	if claim, found := original.legacyImageFormats["blank.png"]; !found || claim.Format != "" || claim.Conflicting {
		t.Fatalf("clone changed original blank claim: %+v", original.legacyImageFormats)
	}
	if claim := cloned.legacyImageFormats["blank.png"]; !claim.Conflicting || claim.Format != "" {
		t.Fatalf("conflicting duplicate regained authority: %+v", claim)
	}
	if _, found := original.legacyImageFormats["new.png"]; found {
		t.Fatal("legacy map clone aliases original")
	}
	kind, payload, err := cloned.MarshalPrivateResource()
	if err != nil || kind != workflowPrivateResourceKindMedia {
		t.Fatalf("marshal resource: kind=%q err=%v", kind, err)
	}
	if bytes.Contains(payload, []byte("legacyImageFormats")) || bytes.Contains(payload, []byte("blank.png")) {
		t.Fatalf("transient labels were persisted: %s", payload)
	}
	decoded, err := decodeWorkflowMediaPrivateArtifacts(workflowMediaBuilder{media: &mediaModule{}}, payload)
	if err != nil || len(decoded.legacyImageFormats) != 0 {
		t.Fatalf("transient labels survived serialization: %+v err=%v", decoded.legacyImageFormats, err)
	}
}
