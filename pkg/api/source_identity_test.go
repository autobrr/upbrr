// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestContentIdentityVersionsDescribeBoundedSamples(t *testing.T) {
	t.Parallel()

	if SourceContentIdentitySampleBytes != 1<<20 {
		t.Fatalf("source sample bytes = %d, want 1048576", SourceContentIdentitySampleBytes)
	}
	if !strings.Contains(SourceContentIdentityVersion, "sample") || !strings.Contains(SubmissionContentIdentityVersion, "sample") {
		t.Fatalf("content identity versions must describe sample evidence: source=%q submission=%q", SourceContentIdentityVersion, SubmissionContentIdentityVersion)
	}
}

func TestNewSubmissionContentIdentityCanonicalizesFileSets(t *testing.T) {
	t.Parallel()

	first, err := NewSubmissionContentIdentity(SubmissionContentScopeFileSet, []SubmissionContentFile{
		{
			RelativePath: "season\\episode-02.mkv",
			Size:         2,
			SHA256:       testSHA256("b"),
		},
		{
			RelativePath: "season/episode-01.mkv",
			Size:         1,
			SHA256:       testSHA256("a"),
		},
	})
	if err != nil {
		t.Fatalf("first identity: %v", err)
	}
	second, err := NewSubmissionContentIdentity(SubmissionContentScopeFileSet, []SubmissionContentFile{
		{
			RelativePath: "season/episode-01.mkv",
			Size:         1,
			SHA256:       testSHA256("a"),
		},
		{
			RelativePath: "season/episode-02.mkv",
			Size:         2,
			SHA256:       testSHA256("b"),
		},
	})
	if err != nil {
		t.Fatalf("second identity: %v", err)
	}
	if first.Version != SubmissionContentIdentityVersion || first.Digest != second.Digest || len(first.Files) != 2 || first.Files[0].RelativePath != "season/episode-01.mkv" {
		t.Fatalf("canonical identities = %#v and %#v", first, second)
	}
}

func TestNewSubmissionContentIdentitySingleFileExcludesName(t *testing.T) {
	t.Parallel()

	first, err := NewSubmissionContentIdentity(SubmissionContentScopeSingleFile, []SubmissionContentFile{
		{
			RelativePath: "first-name.mkv",
			Size:         4,
			SHA256:       testSHA256("same"),
		},
	})
	if err != nil {
		t.Fatalf("first identity: %v", err)
	}
	second, err := NewSubmissionContentIdentity(SubmissionContentScopeSingleFile, []SubmissionContentFile{
		{
			RelativePath: "renamed.mkv",
			Size:         4,
			SHA256:       testSHA256("same"),
		},
	})
	if err != nil {
		t.Fatalf("second identity: %v", err)
	}
	if first.Digest != second.Digest || first.Files[0].RelativePath != "" {
		t.Fatalf("single-file identities = %#v and %#v", first, second)
	}
}

func TestNewSubmissionContentIdentityRejectsAmbiguousPaths(t *testing.T) {
	t.Parallel()

	_, err := NewSubmissionContentIdentity(SubmissionContentScopeFileSet, []SubmissionContentFile{
		{
			RelativePath: "../escape.mkv",
			Size:         1,
			SHA256:       testSHA256("a"),
		},
	})
	if err == nil {
		t.Fatal("expected escaping path to fail")
	}
	_, err = NewSubmissionContentIdentity(SubmissionContentScopeFileSet, []SubmissionContentFile{
		{
			RelativePath: "same.mkv",
			Size:         1,
			SHA256:       testSHA256("a"),
		},
		{
			RelativePath: "same.mkv",
			Size:         1,
			SHA256:       testSHA256("b"),
		},
	})
	if err == nil {
		t.Fatal("expected duplicate path to fail")
	}
}

func TestPreparedReleaseSourceIdentityStaysPrivateAndClones(t *testing.T) {
	t.Parallel()

	release := PreparedRelease{SourceIdentity: SourceContentIdentity{
		Version:             SourceContentIdentityVersion,
		Digest:              testSHA256("private"),
		ManifestFingerprint: "manifest",
		Files:               []VerifiedSourceFile{{LocalPath: "C:\\private\\release.mkv", SHA256: testSHA256("file")}},
	}}
	payload, err := json.Marshal(release)
	if err != nil {
		t.Fatalf("marshal prepared release: %v", err)
	}
	if strings.Contains(string(payload), release.SourceIdentity.Digest) || strings.Contains(string(payload), "private") {
		t.Fatalf("prepared release leaked source identity: %s", payload)
	}
	cloned, err := release.Clone()
	if err != nil {
		t.Fatalf("clone prepared release: %v", err)
	}
	if cloned.SourceIdentity.Digest != release.SourceIdentity.Digest || len(cloned.SourceIdentity.Files) != 1 ||
		cloned.SourceIdentity.Files[0].LocalPath != release.SourceIdentity.Files[0].LocalPath {
		t.Fatalf("cloned source identity = %#v", cloned.SourceIdentity)
	}
}

func TestMediaCompatibilityKeyRequiresVerifiedSourceAndExcludesProviderFacts(t *testing.T) {
	t.Parallel()

	release := PreparedRelease{
		SourceIdentity: SourceContentIdentity{
			Version: SourceContentIdentityVersion,
			Digest:  testSHA256("source"),
		},
		Disc: DiscFacts{Items: []DiscItemFacts{{
			ID:      "disc-a",
			Reports: []DiscReportFacts{{Playlist: PlaylistInfo{ID: "00001.MPLS", DiscID: "disc-a"}}},
		}}},
	}
	first, err := release.MediaCompatibilityKey()
	if err != nil || !first.Valid() {
		t.Fatalf("media compatibility key = %q, err=%v", first, err)
	}
	changedProvider := release
	changedProvider.Naming.ReleaseName = "Corrected.Release.Name"
	second, err := changedProvider.MediaCompatibilityKey()
	if err != nil || first != second {
		t.Fatalf("provider-only media key = %q, err=%v; want %q", second, err, first)
	}
	changedPlaylist := release
	changedPlaylist.Disc.Items = append([]DiscItemFacts(nil), release.Disc.Items...)
	changedPlaylist.Disc.Items[0].Reports = append([]DiscReportFacts(nil), release.Disc.Items[0].Reports...)
	changedPlaylist.Disc.Items[0].Reports[0].Playlist.ID = "00002.MPLS"
	third, err := changedPlaylist.MediaCompatibilityKey()
	if err != nil || third == first {
		t.Fatalf("playlist-changed media key = %q, err=%v; want distinct", third, err)
	}
	if _, err := (PreparedRelease{}).MediaCompatibilityKey(); err == nil {
		t.Fatal("missing verified source identity produced a media compatibility key")
	}
}

func testSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
