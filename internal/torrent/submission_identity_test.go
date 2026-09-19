// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestSubmissionContentIdentityUsesCanonicalWantedInventory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := filepath.Join(root, "episode-01.mkv")
	second := filepath.Join(root, "nested", "episode-02.mkv")
	writeSubmissionIdentityFile(t, first, "one")
	writeSubmissionIdentityFile(t, second, "two")
	verified := verifySubmissionSource(t, root)

	forward, ok, err := ResolveSubmissionContentInventory(api.TorrentSubject{SourcePath: root, FileList: []string{first, second}})
	if err != nil || !ok {
		t.Fatalf("forward inventory = %#v, %t, %v", forward, ok, err)
	}
	reversed, ok, err := ResolveSubmissionContentInventory(api.TorrentSubject{SourcePath: root, FileList: []string{second, first}})
	if err != nil || !ok {
		t.Fatalf("reversed inventory = %#v, %t, %v", reversed, ok, err)
	}
	forwardIdentity, err := SubmissionContentIdentity(forward, verified)
	if err != nil {
		t.Fatalf("forward identity: %v", err)
	}
	reversedIdentity, err := SubmissionContentIdentity(reversed, verified)
	if err != nil {
		t.Fatalf("reversed identity: %v", err)
	}
	if forwardIdentity.Version != api.SubmissionContentIdentityVersion || forwardIdentity.Digest != reversedIdentity.Digest || forwardIdentity.Scope != api.SubmissionContentScopeFileSet ||
		!slices.EqualFunc(forwardIdentity.Files, reversedIdentity.Files, func(left, right api.SubmissionContentFile) bool { return left == right }) {
		t.Fatalf("canonical identities = %#v and %#v", forwardIdentity, reversedIdentity)
	}

	expected, ok, err := expectedTorrentFiles(api.TorrentSubject{SourcePath: root, FileList: []string{second, first}})
	if err != nil || !ok || len(expected) != len(reversed.Files) {
		t.Fatalf("expected torrent files = %#v, %t, %v", expected, ok, err)
	}
	for index := range expected {
		if expected[index].path != reversed.Files[index].TorrentPath || expected[index].length != reversed.Files[index].Size {
			t.Fatalf("expected torrent file %d = %#v, inventory = %#v", index, expected[index], reversed.Files[index])
		}
	}
}

func TestSubmissionContentIdentityFullDiscIgnoresPlaylistSelection(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "BDMV")
	stream := filepath.Join(root, "STREAM", "00001.m2ts")
	playlist := filepath.Join(root, "PLAYLIST", "00001.mpls")
	writeSubmissionIdentityFile(t, stream, "stream")
	writeSubmissionIdentityFile(t, playlist, "playlist")
	writeSubmissionIdentityFile(t, filepath.Join(root, "existing.torrent"), "metainfo")
	verified := verifySubmissionSource(t, root)

	first, ok, err := ResolveSubmissionContentInventory(api.TorrentSubject{
		SourcePath: root,
		DiscType:   "BDMV",
		FileList:   []string{stream},
	})
	if err != nil || !ok {
		t.Fatalf("first disc inventory = %#v, %t, %v", first, ok, err)
	}
	second, ok, err := ResolveSubmissionContentInventory(api.TorrentSubject{
		SourcePath: root,
		DiscType:   "BDMV",
		FileList:   []string{playlist},
	})
	if err != nil || !ok {
		t.Fatalf("second disc inventory = %#v, %t, %v", second, ok, err)
	}
	firstIdentity, err := SubmissionContentIdentity(first, verified)
	if err != nil {
		t.Fatalf("first disc identity: %v", err)
	}
	secondIdentity, err := SubmissionContentIdentity(second, verified)
	if err != nil {
		t.Fatalf("second disc identity: %v", err)
	}
	if first.Scope != api.SubmissionContentScopeFullDisc || firstIdentity.Digest != secondIdentity.Digest || len(first.Files) != 2 {
		t.Fatalf("disc identities = %#v and %#v", firstIdentity, secondIdentity)
	}
}

func TestSubmissionContentIdentityFailsWithoutFreshFileVerification(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSubmissionIdentityFile(t, source, "content")
	inventory, ok, err := ResolveSubmissionContentInventory(api.TorrentSubject{SourcePath: source})
	if err != nil || !ok {
		t.Fatalf("inventory = %#v, %t, %v", inventory, ok, err)
	}
	_, err = SubmissionContentIdentity(inventory, api.SourceContentIdentity{
		Version: api.SourceContentIdentityVersion,
		Digest:  "verified-source",
	})
	if err == nil {
		t.Fatal("expected missing verified file to fail")
	}
}

func TestSubmissionContentIdentityRejectsLegacyFullSourceEvidence(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "release.mkv")
	writeSubmissionIdentityFile(t, source, "content")
	inventory, ok, err := ResolveSubmissionContentInventory(api.TorrentSubject{SourcePath: source})
	if err != nil || !ok {
		t.Fatalf("inventory = %#v, %t, %v", inventory, ok, err)
	}
	legacy := verifySubmissionSource(t, source)
	legacy.Version = "source-content-v1"
	_, err = SubmissionContentIdentity(inventory, legacy)
	if err == nil || !strings.Contains(err.Error(), "sampled source content identity") {
		t.Fatalf("legacy source evidence error = %v", err)
	}
}

func verifySubmissionSource(t *testing.T, source string) api.SourceContentIdentity {
	t.Helper()
	verified, err := preparedrelease.VerifyInputSource(context.Background(), api.PrepareInput{SourcePath: source})
	if err != nil {
		t.Fatalf("verify source %q: %v", source, err)
	}
	return verified.Identity
}

func writeSubmissionIdentityFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create submission identity parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write submission identity file: %v", err)
	}
}
