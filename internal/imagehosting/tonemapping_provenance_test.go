// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehosting

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/imageevidence"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadPreservesOnlyContentBoundTonemapping(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  api.ScreenshotTonemapping
		replace bool
	}{
		{"tonemapped", api.ScreenshotTonemapped, false},
		{"not tonemapped", api.ScreenshotNotTonemapped, false},
		{"unknown", "", false},
		{"replaced", api.ScreenshotTonemapped, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "same.png")
			if err := os.WriteFile(path, []byte("first"), 0o600); err != nil {
				t.Fatal(err)
			}
			digest, err := imageevidence.Digest(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.replace {
				if err := os.WriteFile(path, []byte("other"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			repo := &recordingRepo{}
			svc := &Service{repo: repo, uploaders: map[string]uploader{"test": &fakeUploader{result: uploadResult{RawURL: "https://example.invalid/raw.png"}}}}
			got, err := svc.Upload(context.Background(), imageHostingTestSubject("source"), "test", "tracker:HDB", []api.ScreenshotImage{{
				Path:          path,
				Tonemapping:   tc.status,
				ContentSHA256: digest,
			}})
			if err != nil {
				t.Fatal(err)
			}
			currentDigest, digestErr := imageevidence.Digest(path)
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			want := tc.status
			if tc.replace {
				want = ""
			}
			if len(got) != 1 || got[0].Tonemapping != want || got[0].ContentSHA256 != currentDigest || len(repo.savedImages) != 1 || repo.savedImages[0].Tonemapping != want || repo.savedImages[0].ContentSHA256 != currentDigest {
				t.Fatalf("uploaded=%#v persisted=%#v, want %q", got, repo.savedImages, want)
			}
		})
	}
}

func TestUploadRecoversCurrentLocalEvidenceWithoutChangingOldURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "regenerated.png")
	if err := os.WriteFile(path, []byte("new pixels"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := imageevidence.Digest(path)
	if err != nil {
		t.Fatal(err)
	}
	old := api.UploadedImageLink{
		ImagePath:     path,
		RawURL:        "https://example.invalid/old.png",
		ContentSHA256: "old",
		Tonemapping:   api.ScreenshotNotTonemapped,
	}
	repo := &recordingRepo{screens: []api.Screenshot{{
		ImagePath:     path,
		ContentSHA256: digest,
		Tonemapping:   api.ScreenshotTonemapped,
	}}, uploads: []api.UploadedImageLink{old}}
	svc := &Service{repo: repo, uploaders: map[string]uploader{"test": &fakeUploader{result: uploadResult{RawURL: "https://example.invalid/new.png"}}}}
	got, err := svc.Upload(t.Context(), imageHostingTestSubject("source"), "test", "global", []api.ScreenshotImage{{
		Path:          path,
		RawURL:        old.RawURL,
		Tonemapping:   old.Tonemapping,
		ContentSHA256: old.ContentSHA256,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tonemapping != api.ScreenshotTonemapped || got[0].ContentSHA256 != digest {
		t.Fatalf("current local evidence lost: %+v", got)
	}
	if repo.uploads[0] != old {
		t.Fatal("old hosted evidence changed")
	}
}

type failedScreenshotEvidenceRepo struct{ recordingRepo }

func (r *failedScreenshotEvidenceRepo) ListScreenshotsByPath(context.Context, api.PreparedMediaBinding) ([]api.Screenshot, error) {
	return r.screens, errors.New("lookup failed")
}

func TestUploadDoesNotTrustFailedScreenshotLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.png")
	if err := os.WriteFile(path, []byte("pixels"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := imageevidence.Digest(path)
	if err != nil {
		t.Fatal(err)
	}
	repo := &failedScreenshotEvidenceRepo{recordingRepo{screens: []api.Screenshot{{
		ImagePath:     path,
		ContentSHA256: digest,
		Tonemapping:   api.ScreenshotTonemapped,
	}}}}
	svc := &Service{repo: repo, uploaders: map[string]uploader{"test": &fakeUploader{result: uploadResult{RawURL: "https://example.invalid/unknown.png"}}}}
	got, err := svc.Upload(t.Context(), imageHostingTestSubject("source"), "test", "global", []api.ScreenshotImage{{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Tonemapping != "" {
		t.Fatalf("failed lookup established processing: %+v", got)
	}
}
