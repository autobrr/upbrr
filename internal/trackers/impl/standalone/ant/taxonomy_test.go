// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package ant

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveAudioFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		audio string
		want  string
	}{
		{name: "no audio", want: "NoAudio"},
		{
			name:  "DTS X",
			audio: "DTS:X 7.1",
			want:  "DTSMA",
		},
		{
			name:  "DTS HD HRA",
			audio: "DTS-HD HRA 5.1",
			want:  "DTSMA",
		},
		{
			name:  "DTS HD MA",
			audio: "DTS-HD MA 7.1",
			want:  "DTSMA",
		},
		{
			name:  "DTS",
			audio: "DTS 5.1",
			want:  "DTS",
		},
		{
			name:  "unknown",
			audio: "VORBIS 2.0",
			want:  "Other",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := resolveAudioFormat(api.UploadSubject{Audio: test.audio}); got != test.want {
				t.Fatalf("resolveAudioFormat() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveTagsAndAdultPreferManualGenres(t *testing.T) {
	t.Parallel()

	meta := api.UploadSubject{
		Release:          api.ReleaseInfo{Genre: "Porn"},
		ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{Genres: "Action, Porn"}},
		EffectiveMetadata: api.EffectiveMetadata{
			Genres: []string{"Drama"}, GenresProvenance: api.FactProvenanceManual,
		},
	}
	if got, manual := resolveTags(meta, nil); got != "drama" || manual {
		t.Fatalf("manual tags = %q, manual = %t", got, manual)
	}
	meta.EffectiveMetadata.Genres = nil
	meta.EffectiveMetadata.GenresProvenance = api.FactProvenanceManualEmpty
	if got, _ := resolveTags(meta, nil); got != "" {
		t.Fatalf("manual-empty tags = %q", got)
	}
	if detectAdult(meta) {
		t.Fatal("manual-empty genres retained provider adult classification")
	}
}
