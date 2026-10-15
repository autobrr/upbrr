// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package preparedrelease

import (
	"testing"

	"github.com/autobrr/upbrr/internal/mediafacts"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestHDRTargetAudioUsesOnlyPersistedExactResource(t *testing.T) {
	path := `E:\Synthetic\Episode.One.mkv`
	resource := mediafacts.TrackResourceID(path)
	release := api.PreparedRelease{Media: api.MediaFacts{
		TrackCoverageComplete: true,
		PrimaryAudioTrackID:   "main",
		Tracks: []api.MediaTrackFacts{
			{
				ID:                  "main",
				Kind:                api.MediaTrackAudio,
				ResourceID:          resource,
				Ordinal:             1,
				ManifestFingerprint: "manifest",
				Languages:           []string{"en"},
			},
			{
				ID:                  "alternate",
				Kind:                api.MediaTrackAudio,
				ResourceID:          resource,
				Ordinal:             2,
				ManifestFingerprint: "manifest",
			},
		},
	}}
	target := api.HDRAnalysisTargetSubject{Path: path}
	subject := hdrTargetAudioSubject(release, api.ReleaseRef{}, api.PreparedMediaBinding{}, target)
	if subject == nil || subject.PrimaryTrackID != "main" || len(subject.Tracks) != 2 || subject.Tracks[1].ID != "alternate" {
		t.Fatalf("lost prepared audio authority: %+v", subject)
	}
	subject.Tracks[0].Languages[0] = "fr"
	if release.Media.Tracks[0].Languages[0] != "en" {
		t.Fatal("projection aliases persisted language facts")
	}
	target.Path = `E:\Synthetic\Episode.Two.mkv`
	if hdrTargetAudioSubject(release, api.ReleaseRef{}, api.PreparedMediaBinding{}, target) != nil {
		t.Fatal("uninspected pack sibling borrowed first episode audio")
	}
	target.Path, target.DiscRoot = path, `E:\Synthetic\Disc`
	if hdrTargetAudioSubject(release, api.ReleaseRef{}, api.PreparedMediaBinding{}, target) != nil {
		t.Fatal("disc ordinal treated as decoder stream authority")
	}
	target.DiscRoot = ""
	release.Media.Tracks[1].ManifestFingerprint = "other"
	if hdrTargetAudioSubject(release, api.ReleaseRef{}, api.PreparedMediaBinding{}, target) != nil {
		t.Fatal("mixed inventory accepted")
	}
}

func TestHDRTargetAudioPreservesProvenAbsence(t *testing.T) {
	path := `E:\Synthetic\Silent.mkv`
	release := api.PreparedRelease{
		Source: api.SourceManifest{Entries: []api.SourceManifestEntry{{Path: path, Type: api.SourceEntryTypeFile}}},
		Media:  api.MediaFacts{AudioAbsent: true, TrackCoverageComplete: true},
	}
	target := api.HDRAnalysisTargetSubject{Path: path}
	if subject := hdrTargetAudioSubject(release, api.ReleaseRef{}, api.PreparedMediaBinding{}, target); subject == nil || len(subject.Tracks) != 0 {
		t.Fatal("lost proven single-file audio absence")
	}
	for _, mutate := range []func(*api.PreparedRelease, *api.HDRAnalysisTargetSubject){
		func(r *api.PreparedRelease, _ *api.HDRAnalysisTargetSubject) { r.Media.AudioAbsent = false },
		func(r *api.PreparedRelease, _ *api.HDRAnalysisTargetSubject) { r.Media.TrackCoverageComplete = false },
		func(_ *api.PreparedRelease, target *api.HDRAnalysisTargetSubject) {
			target.Path = `E:\Synthetic\Uninspected.mkv`
		},
		func(_ *api.PreparedRelease, target *api.HDRAnalysisTargetSubject) {
			target.DiscRoot = `E:\Synthetic\Disc`
		},
	} {
		r, target := release, target
		mutate(&r, &target)
		if hdrTargetAudioSubject(r, api.ReleaseRef{}, api.PreparedMediaBinding{}, target) != nil {
			t.Fatal("unknown scope claimed audio absence")
		}
	}
}
