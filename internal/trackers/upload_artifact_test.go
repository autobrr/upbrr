// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveUploadTorrentPathWritesCleanBaseCopy(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dirtyTorrentPath := filepath.Join(tmp, "dirty.torrent")
	writeTestMetaInfo(t, dirtyTorrentPath, metainfo.MetaInfo{
		Announce:     "https://tracker.example/announce",
		AnnounceList: metainfo.AnnounceList{{"https://tracker.example/announce"}},
		Nodes:        []metainfo.Node{"127.0.0.1:6881"},
		Comment:      "Created by Upload Assistant",
		CreatedBy:    "mkbrr using Upload Assistant",
		Encoding:     "UTF-8",
		UrlList:      metainfo.UrlList{"https://webseed.example/file"},
		PieceLayers:  map[string]string{"root": "layer"},
		InfoBytes:    testInfoBytes(t, "BHD"),
	})

	got, err := ResolveUploadTorrentPath(api.PreparedMetadata{
		SourcePath:  sourcePath,
		TorrentPath: dirtyTorrentPath,
	}, filepath.Join(tmp, "state", "upbrr.db"))
	if err != nil {
		t.Fatalf("resolve upload torrent: %v", err)
	}
	if got == dirtyTorrentPath {
		t.Fatal("expected clean temp copy, got original path")
	}

	cleaned := readTestMetaInfo(t, got)
	if cleaned.Announce != "" {
		t.Fatal("expected announce cleared")
	}
	if len(cleaned.AnnounceList) != 0 {
		t.Fatal("expected announce-list cleared")
	}
	if len(cleaned.Nodes) != 0 {
		t.Fatalf("expected nodes cleared, got %#v", cleaned.Nodes)
	}
	if len(cleaned.UrlList) != 0 {
		t.Fatalf("expected url-list cleared, got %#v", cleaned.UrlList)
	}
	expectedPieceLayers := map[string]string{"root": "layer"}
	if !reflect.DeepEqual(cleaned.PieceLayers, expectedPieceLayers) {
		t.Fatalf("expected piece layers preserved, got %#v", cleaned.PieceLayers)
	}
	assertInfoSource(t, cleaned, "")
	assertInfoSourceKeyAbsent(t, cleaned)
	if cleaned.Comment != "upbrr" {
		t.Fatalf("expected upbrr comment, got %q", cleaned.Comment)
	}
	if cleaned.CreatedBy != "uploaded with upbrr" {
		t.Fatalf("expected upbrr created-by, got %q", cleaned.CreatedBy)
	}

	original := readTestMetaInfo(t, dirtyTorrentPath)
	if original.Comment != "Created by Upload Assistant" {
		t.Fatalf("expected original unchanged, got %q", original.Comment)
	}
	if !reflect.DeepEqual(original.PieceLayers, expectedPieceLayers) {
		t.Fatalf("expected original piece layers unchanged, got %#v", original.PieceLayers)
	}
	assertInfoSource(t, original, "BHD")
}

func TestResolveUploadTorrentPathSamePathRewriteFailurePreservesGuessedTorrent(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dbPath := filepath.Join(tmp, "state", "upbrr.db")
	meta := api.PreparedMetadata{SourcePath: sourcePath}
	guessed, ok := uploadTorrentCleanPath(meta, dbPath)
	if !ok {
		t.Fatal("expected clean upload torrent path")
	}
	writeTestMetaInfo(t, guessed, metainfo.MetaInfo{
		Announce:  "https://tracker.example/announce",
		Comment:   "original",
		InfoBytes: testInvalidInfoBytes(t),
	})
	before, err := os.ReadFile(guessed)
	if err != nil {
		t.Fatalf("read guessed torrent: %v", err)
	}

	got, err := ResolveUploadTorrentPath(meta, dbPath)
	if err == nil {
		t.Fatalf("expected rewrite error, got path %q", got)
	}
	if got != "" {
		t.Fatalf("expected no resolved path on rewrite failure, got %q", got)
	}
	after, err := os.ReadFile(guessed)
	if err != nil {
		t.Fatalf("read guessed torrent after failure: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("expected guessed torrent bytes preserved after rewrite failure")
	}
}

func TestResolveUploadTorrentPathFallsBackToExplicitCandidateWhenCleanCopyInvalid(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	invalidTorrentPath := filepath.Join(tmp, "invalid.torrent")
	if err := os.WriteFile(invalidTorrentPath, []byte("not a torrent"), 0o600); err != nil {
		t.Fatalf("write invalid torrent: %v", err)
	}

	got, err := ResolveUploadTorrentPath(api.PreparedMetadata{
		SourcePath:  sourcePath,
		TorrentPath: invalidTorrentPath,
	}, filepath.Join(tmp, "state", "upbrr.db"))
	if err != nil {
		t.Fatalf("resolve upload torrent: %v", err)
	}
	if got != invalidTorrentPath {
		t.Fatalf("expected explicit invalid torrent fallback, got %q", got)
	}
}

func TestWriteUploadTorrentPreservesPieceLayers(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "source.torrent")
	outputPath := filepath.Join(tmp, "out", "clean.torrent")
	expectedPieceLayers := map[string]string{
		"root-a": "layer-a",
		"root-b": "layer-b",
	}
	writeTestMetaInfo(t, sourcePath, metainfo.MetaInfo{
		Announce:     "https://tracker.example/announce",
		AnnounceList: metainfo.AnnounceList{{"https://tracker.example/announce"}},
		Nodes:        []metainfo.Node{"127.0.0.1:6881"},
		Comment:      "Created by Upload Assistant",
		UrlList:      metainfo.UrlList{"https://webseed.example/file"},
		PieceLayers:  expectedPieceLayers,
		InfoBytes:    testInfoBytes(t, "BHD"),
	})

	if err := WriteUploadTorrent(sourcePath, outputPath); err != nil {
		t.Fatalf("write upload torrent: %v", err)
	}

	cleaned := readTestMetaInfo(t, outputPath)
	if !reflect.DeepEqual(cleaned.PieceLayers, expectedPieceLayers) {
		t.Fatalf("expected piece layers preserved, got %#v", cleaned.PieceLayers)
	}
	if cleaned.Announce != "" {
		t.Fatal("expected announce cleared")
	}
	if len(cleaned.AnnounceList) != 0 {
		t.Fatal("expected announce-list cleared")
	}
	if len(cleaned.Nodes) != 0 {
		t.Fatalf("expected nodes cleared, got %#v", cleaned.Nodes)
	}
	if len(cleaned.UrlList) != 0 {
		t.Fatalf("expected url-list cleared, got %#v", cleaned.UrlList)
	}
	if cleaned.Comment != "upbrr" {
		t.Fatalf("expected upbrr comment, got %q", cleaned.Comment)
	}
	assertInfoSource(t, cleaned, "")
	assertInfoSourceKeyAbsent(t, cleaned)

	original := readTestMetaInfo(t, sourcePath)
	if original.Comment != "Created by Upload Assistant" {
		t.Fatalf("expected original comment unchanged, got %q", original.Comment)
	}
	if !reflect.DeepEqual(original.PieceLayers, expectedPieceLayers) {
		t.Fatalf("expected original piece layers unchanged, got %#v", original.PieceLayers)
	}
	assertInfoSource(t, original, "BHD")
}

func TestResolveUploadTorrentPathWithoutCleanTargetLeavesOriginalUnchanged(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "original.torrent")
	writeTestMetaInfo(t, torrentPath, metainfo.MetaInfo{
		Announce:  "https://tracker.example/announce",
		Comment:   "private tracker comment",
		InfoBytes: testInfoBytes(t, ""),
	})

	got, err := ResolveUploadTorrentPath(api.PreparedMetadata{TorrentPath: torrentPath}, "")
	if err != nil {
		t.Fatalf("resolve upload torrent: %v", err)
	}
	if got != torrentPath {
		t.Fatalf("expected original path, got %q", got)
	}

	original := readTestMetaInfo(t, torrentPath)
	if original.Announce != "https://tracker.example/announce" {
		t.Fatal("expected original announce unchanged")
	}
	if original.Comment != "private tracker comment" {
		t.Fatalf("expected original comment unchanged, got %q", original.Comment)
	}
}

func TestWritePersonalizedTorrentSetsTrackerFields(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "base.torrent")
	outputPath := filepath.Join(tmp, "out", "release.ptp.torrent")
	writeTestMetaInfo(t, sourcePath, metainfo.MetaInfo{
		Announce:     "https://old.example/announce",
		AnnounceList: metainfo.AnnounceList{{"https://old.example/announce"}},
		Comment:      "Created by Upload Assistant",
		UrlList:      metainfo.UrlList{"https://webseed.example/file"},
		PieceLayers:  map[string]string{"root": "layer"},
		InfoBytes:    testInfoBytes(t, "BHD"),
	})

	if err := WritePersonalizedTorrent(sourcePath, outputPath, "https://new.example/announce", "https://tracker.example/torrents/123", "PTP"); err != nil {
		t.Fatalf("write personalized torrent: %v", err)
	}

	updated := readTestMetaInfo(t, outputPath)
	if updated.Announce != "https://new.example/announce" {
		t.Fatal("expected announce set")
	}
	if len(updated.AnnounceList) != 1 || len(updated.AnnounceList[0]) != 1 || updated.AnnounceList[0][0] != "https://new.example/announce" {
		t.Fatal("expected announce-list set")
	}
	if updated.Comment != "https://tracker.example/torrents/123" {
		t.Fatalf("expected tracker comment, got %q", updated.Comment)
	}
	if updated.CreatedBy != "uploaded with upbrr" {
		t.Fatalf("expected upbrr created-by, got %q", updated.CreatedBy)
	}
	if len(updated.UrlList) != 0 {
		t.Fatalf("expected url-list cleared, got %#v", updated.UrlList)
	}
	expectedPieceLayers := map[string]string{"root": "layer"}
	if !reflect.DeepEqual(updated.PieceLayers, expectedPieceLayers) {
		t.Fatalf("expected piece layers preserved, got %#v", updated.PieceLayers)
	}
	assertInfoSource(t, updated, "PTP")
}

func TestPrepareTrackerUploadTorrentCreatesSpecificArtifact(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	baseTorrentPath := filepath.Join(tmp, "base.torrent")
	writeTestMetaInfo(t, baseTorrentPath, metainfo.MetaInfo{
		Announce:  "https://old.example/announce",
		Comment:   "private comment",
		InfoBytes: testInfoBytes(t, "old-source"),
	})

	dbPath := filepath.Join(tmp, "state", "upbrr.db")
	meta, err := PrepareTrackerUploadTorrent(api.PreparedMetadata{
		SourcePath:  sourcePath,
		TorrentPath: baseTorrentPath,
	}, dbPath, "HDB", config.TrackerConfig{AnnounceURL: "https://new.example/announce"})
	if err != nil {
		t.Fatalf("prepare tracker torrent: %v", err)
	}
	if meta.TorrentPath == "" || meta.TorrentPath == baseTorrentPath {
		t.Fatalf("expected tracker artifact path, got %q", meta.TorrentPath)
	}

	artifact := readTestMetaInfo(t, meta.TorrentPath)
	if artifact.Announce != "https://new.example/announce" {
		t.Fatal("expected announce set")
	}
	assertInfoSource(t, artifact, "HDBits")

	cleanBase, ok := uploadTorrentCleanPath(api.PreparedMetadata{SourcePath: sourcePath, TorrentPath: baseTorrentPath}, dbPath)
	if !ok {
		t.Fatal("expected clean base path")
	}
	cleaned := readTestMetaInfo(t, cleanBase)
	if cleaned.Announce != "" {
		t.Fatal("expected clean base announce cleared")
	}
	assertInfoSource(t, cleaned, "")
}

func TestPrepareTrackerUploadTorrentUsesDefaultAnnounce(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	baseTorrentPath := filepath.Join(tmp, "base.torrent")
	writeTestMetaInfo(t, baseTorrentPath, metainfo.MetaInfo{InfoBytes: testInfoBytes(t, "")})

	meta, err := PrepareTrackerUploadTorrent(api.PreparedMetadata{
		SourcePath:  sourcePath,
		TorrentPath: baseTorrentPath,
	}, filepath.Join(tmp, "state", "upbrr.db"), "AZ", config.TrackerConfig{})
	if err != nil {
		t.Fatalf("prepare tracker torrent: %v", err)
	}
	artifact := readTestMetaInfo(t, meta.TorrentPath)
	if artifact.Announce != "https://tracker.avistaz.to/announce" {
		t.Fatal("expected default announce")
	}
	assertInfoSource(t, artifact, "AvistaZ")
}

func TestPrepareTrackerUploadTorrentNoSpecLeavesMetaUnchanged(t *testing.T) {
	t.Parallel()

	meta := api.PreparedMetadata{TorrentPath: filepath.Join(t.TempDir(), "base.torrent")}
	got, err := PrepareTrackerUploadTorrent(meta, "", "BTN", config.TrackerConfig{})
	if err != nil {
		t.Fatalf("prepare tracker torrent: %v", err)
	}
	if got.TorrentPath != meta.TorrentPath {
		t.Fatalf("expected torrent path unchanged, got %q", got.TorrentPath)
	}
}

func TestPrepareDryRunInjectionTorrentCreatesGenericTrackerArtifact(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	baseTorrentPath := filepath.Join(tmp, "base.torrent")
	writeTestMetaInfo(t, baseTorrentPath, metainfo.MetaInfo{InfoBytes: testInfoBytes(t, "")})

	meta, err := PrepareDryRunInjectionTorrent(api.PreparedMetadata{
		SourcePath:  sourcePath,
		TorrentPath: baseTorrentPath,
	}, filepath.Join(tmp, "state", "upbrr.db"), "LUME", config.TrackerConfig{AnnounceURL: "https://luminarr.me/announce/passkey"})
	if err != nil {
		t.Fatalf("prepare dry-run injection torrent: %v", err)
	}
	if meta.TorrentPath == "" || meta.TorrentPath == baseTorrentPath {
		t.Fatalf("expected dry-run tracker artifact path, got %q", meta.TorrentPath)
	}

	artifact := readTestMetaInfo(t, meta.TorrentPath)
	if artifact.Announce != "https://luminarr.me/announce/passkey" {
		t.Fatal("expected announce set")
	}
	assertInfoSource(t, artifact, "LUME")
}

func TestResolveTrackerTorrentArtifactPathPrefixesTrackerName(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "A.Better.Life.2011.BluRay.1080p.DTS.x264-CHD.mkv")
	got, err := ResolveTrackerTorrentArtifactPath(api.PreparedMetadata{SourcePath: sourcePath}, filepath.Join(tmp, "state", "upbrr.db"), "MTV")
	if err != nil {
		t.Fatalf("resolve tracker torrent artifact: %v", err)
	}
	if filepath.Base(got) != "[mtv].A.Better.Life.2011.BluRay.1080p.DTS.x264-CHD.mkv.torrent" {
		t.Fatalf("expected tracker-prefixed artifact name, got %q", filepath.Base(got))
	}
}

func writeTestMetaInfo(t *testing.T, path string, meta metainfo.MetaInfo) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create torrent dir: %v", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}
	defer file.Close()
	if err := meta.Write(file); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
}

func readTestMetaInfo(t *testing.T, path string) metainfo.MetaInfo {
	t.Helper()

	meta, err := metainfo.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load torrent %s: %v", path, err)
	}
	return *meta
}

func testInfoBytes(t *testing.T, source string) []byte {
	t.Helper()

	private := true
	info := metainfo.Info{
		PieceLength: 16 * 1024,
		Pieces:      make([]byte, 20),
		Name:        "Release.mkv",
		Length:      4,
		Private:     &private,
		Source:      source,
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	return infoBytes
}

func testInvalidInfoBytes(t *testing.T) []byte {
	t.Helper()

	infoBytes, err := bencode.Marshal("not-info")
	if err != nil {
		t.Fatalf("marshal invalid info: %v", err)
	}
	return infoBytes
}

func assertInfoSource(t *testing.T, meta metainfo.MetaInfo, expected string) {
	t.Helper()

	info, err := meta.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if info.Source != expected {
		t.Fatalf("expected info source %q, got %q", expected, info.Source)
	}
}

func assertInfoSourceKeyAbsent(t *testing.T, meta metainfo.MetaInfo) {
	t.Helper()

	if bytes.Contains(meta.InfoBytes, []byte("6:source")) {
		t.Fatalf("expected raw info source key absent, got %q", string(meta.InfoBytes))
	}
}
