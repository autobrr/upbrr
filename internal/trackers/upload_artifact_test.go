// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package trackers

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestResolveUploadTorrentBasePathWritesCleanBaseCopy(t *testing.T) {
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
		InfoBytes:    testInfoBytes(t, "BHD"),
	})

	got, err := resolveUploadTorrentBasePath(api.UploadSubject{
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
	assertInfoSource(t, cleaned, "")
	assertInfoSourceKeyAbsent(t, cleaned)
	if cleaned.Comment != "uploaded with upbrr" {
		t.Fatalf("expected upbrr comment, got %q", cleaned.Comment)
	}
	if cleaned.CreatedBy != "upbrr with mkbrr" {
		t.Fatalf("expected mkbrr created-by, got %q", cleaned.CreatedBy)
	}

	original := readTestMetaInfo(t, dirtyTorrentPath)
	if original.Comment != "Created by Upload Assistant" {
		t.Fatalf("expected original unchanged, got %q", original.Comment)
	}
	assertInfoSource(t, original, "BHD")
}

func TestResolveUploadTorrentBasePathSamePathRewriteFailurePreservesGuessedTorrent(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	dbPath := filepath.Join(tmp, "state", "upbrr.db")
	meta := api.UploadSubject{SourcePath: sourcePath}
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

	got, err := resolveUploadTorrentBasePath(meta, dbPath)
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

func TestResolveUploadTorrentBasePathFallsBackToExplicitCandidateWhenCleanCopyInvalid(t *testing.T) {
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

	got, err := resolveUploadTorrentBasePath(api.UploadSubject{
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

func TestWriteUploadTorrentCleansTrackerFields(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "source.torrent")
	outputPath := filepath.Join(tmp, "out", "clean.torrent")
	writeTestMetaInfo(t, sourcePath, metainfo.MetaInfo{
		Announce:     "https://tracker.example/announce",
		AnnounceList: metainfo.AnnounceList{{"https://tracker.example/announce"}},
		Nodes:        []metainfo.Node{"127.0.0.1:6881"},
		Comment:      "Created by Upload Assistant",
		UrlList:      metainfo.UrlList{"https://webseed.example/file"},
		InfoBytes:    testInfoBytes(t, "BHD"),
	})

	if err := WriteUploadTorrent(sourcePath, outputPath); err != nil {
		t.Fatalf("write upload torrent: %v", err)
	}

	cleaned := readTestMetaInfo(t, outputPath)
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
	if cleaned.Comment != "uploaded with upbrr" {
		t.Fatalf("expected upbrr comment, got %q", cleaned.Comment)
	}
	if cleaned.CreatedBy != "upbrr" {
		t.Fatalf("expected upbrr created-by, got %q", cleaned.CreatedBy)
	}
	assertInfoSource(t, cleaned, "")
	assertInfoSourceKeyAbsent(t, cleaned)

	original := readTestMetaInfo(t, sourcePath)
	if original.Comment != "Created by Upload Assistant" {
		t.Fatalf("expected original comment unchanged, got %q", original.Comment)
	}
	assertInfoSource(t, original, "BHD")
}

func TestResolveUploadTorrentBasePathWithoutCleanTargetLeavesOriginalUnchanged(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "original.torrent")
	writeTestMetaInfo(t, torrentPath, metainfo.MetaInfo{
		Announce:  "https://tracker.example/announce",
		Comment:   "private tracker comment",
		InfoBytes: testInfoBytes(t, ""),
	})

	got, err := resolveUploadTorrentBasePath(api.UploadSubject{TorrentPath: torrentPath}, "")
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
		InfoBytes:    testInfoBytes(t, "BHD"),
	})

	if err := WritePersonalizedTorrent(sourcePath, outputPath, "https://new.example/announce", "PTP"); err != nil {
		t.Fatalf("write personalized torrent: %v", err)
	}

	updated := readTestMetaInfo(t, outputPath)
	if updated.Announce != "https://new.example/announce" {
		t.Fatal("expected announce set")
	}
	if len(updated.AnnounceList) != 1 || len(updated.AnnounceList[0]) != 1 || updated.AnnounceList[0][0] != "https://new.example/announce" {
		t.Fatal("expected announce-list set")
	}
	if updated.Comment != "uploaded with upbrr" {
		t.Fatalf("expected upbrr comment, got %q", updated.Comment)
	}
	if updated.CreatedBy != "upbrr" {
		t.Fatalf("expected upbrr created-by, got %q", updated.CreatedBy)
	}
	if len(updated.UrlList) != 0 {
		t.Fatalf("expected url-list cleared, got %#v", updated.UrlList)
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
		CreatedBy: "mkbrr",
		InfoBytes: testInfoBytes(t, "old-source"),
	})

	dbPath := filepath.Join(tmp, "state", "upbrr.db")
	meta, err := prepareTrackerUploadTorrentWithRegistry(api.UploadSubject{
		SourcePath:  sourcePath,
		TorrentPath: baseTorrentPath,
	}, dbPath, "HDB", config.TrackerConfig{AnnounceURL: "https://new.example/announce"}, hdbArtifactRegistry(t))
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
	if artifact.Comment != "uploaded with upbrr" {
		t.Fatalf("expected upbrr comment, got %q", artifact.Comment)
	}
	if artifact.CreatedBy != "upbrr with mkbrr" {
		t.Fatalf("expected mkbrr created-by, got %q", artifact.CreatedBy)
	}
	assertInfoSource(t, artifact, "HDBits")

	cleanBase, ok := uploadTorrentCleanPath(api.UploadSubject{SourcePath: sourcePath, TorrentPath: baseTorrentPath}, dbPath)
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

	registry := NewRegistry()
	if err := registry.RegisterDescriptor(Descriptor{
		Name:           "AZ",
		Definition:     stubDefinition{name: "AZ"},
		UploadArtifact: &UploadArtifactPolicy{Source: "AvistaZ", DefaultAnnounce: "https://tracker.avistaz.to/announce"},
	}); err != nil {
		t.Fatalf("register AZ artifact policy: %v", err)
	}
	meta, err := prepareTrackerUploadTorrentWithRegistry(api.UploadSubject{
		SourcePath:  sourcePath,
		TorrentPath: baseTorrentPath,
	}, filepath.Join(tmp, "state", "upbrr.db"), "AZ", config.TrackerConfig{}, registry)
	if err != nil {
		t.Fatalf("prepare tracker torrent: %v", err)
	}
	artifact := readTestMetaInfo(t, meta.TorrentPath)
	if artifact.Announce != "https://tracker.avistaz.to/announce" {
		t.Fatal("expected default announce")
	}
	assertInfoSource(t, artifact, "AvistaZ")
}

func TestPrepareTrackerUploadTorrentUsesBTNAnnounceURL(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	baseTorrentPath := filepath.Join(tmp, "base.torrent")
	writeTestMetaInfo(t, baseTorrentPath, metainfo.MetaInfo{InfoBytes: testInfoBytes(t, "")})

	meta, err := prepareTrackerUploadTorrentWithRegistry(api.UploadSubject{
		SourcePath:  sourcePath,
		TorrentPath: baseTorrentPath,
	}, filepath.Join(tmp, "state", "upbrr.db"), "BTN", config.TrackerConfig{AnnounceURL: "https://tracker.btn.example/announce/passkey"}, btnArtifactRegistry(t))
	if err != nil {
		t.Fatalf("prepare tracker torrent: %v", err)
	}
	if meta.TorrentPath == "" || meta.TorrentPath == baseTorrentPath {
		t.Fatalf("expected BTN tracker artifact path, got %q", meta.TorrentPath)
	}
	artifact := readTestMetaInfo(t, meta.TorrentPath)
	if artifact.Announce != "https://tracker.btn.example/announce/passkey" {
		t.Fatal("expected BTN announce set")
	}
	assertInfoSource(t, artifact, "BTN")
}

func TestPrepareTrackerUploadTorrentFailsBTNWithoutRequiredAnnounceURL(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	baseTorrentPath := filepath.Join(tmp, "base.torrent")
	writeTestMetaInfo(t, baseTorrentPath, metainfo.MetaInfo{InfoBytes: testInfoBytes(t, "")})

	dbPath := filepath.Join(tmp, "state", "upbrr.db")
	meta := api.UploadSubject{
		SourcePath:  sourcePath,
		TorrentPath: baseTorrentPath,
	}
	got, err := prepareTrackerUploadTorrentWithRegistry(meta, dbPath, "BTN", config.TrackerConfig{}, btnArtifactRegistry(t))
	if err == nil || !strings.Contains(err.Error(), "required announce URL is missing") {
		t.Fatalf("prepare tracker torrent error = %v", err)
	}
	if got.TorrentPath != "" {
		t.Fatalf("expected no prepared subject, got %q", got.TorrentPath)
	}
	artifactPath, err := ResolveTrackerTorrentArtifactPath(meta, dbPath, "BTN")
	if err != nil {
		t.Fatalf("resolve BTN artifact path: %v", err)
	}
	if _, err := os.Stat(artifactPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("expected no BTN artifact without announce URL")
	}
}

func TestPrepareTrackerUploadTorrentWithoutPolicyStillCreatesSpecificArtifact(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Example.Release.2026.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	baseTorrentPath := filepath.Join(tmp, "base.torrent")
	writeTestMetaInfo(t, baseTorrentPath, metainfo.MetaInfo{InfoBytes: testInfoBytes(t, "")})
	got, err := prepareTrackerUploadTorrentWithRegistry(api.UploadSubject{
		SourcePath:  sourcePath,
		TorrentPath: baseTorrentPath,
	}, filepath.Join(tmp, "state", "upbrr.db"), "EXAMPLE", config.TrackerConfig{}, nil)
	if err != nil {
		t.Fatalf("prepare tracker torrent: %v", err)
	}
	if got.TorrentPath == "" || got.TorrentPath == baseTorrentPath {
		t.Fatalf("expected tracker-specific copy, got %q", got.TorrentPath)
	}
	assertInfoSource(t, readTestMetaInfo(t, got.TorrentPath), "EXAMPLE")
}

func TestPreparedUploadTorrentPathNeverFallsBack(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	clientPath := filepath.Join(tmp, "client.torrent")
	writeTestMetaInfo(t, clientPath, metainfo.MetaInfo{InfoBytes: testInfoBytes(t, "")})
	if _, err := PreparedUploadTorrentPath(api.UploadSubject{ClientTorrentPath: clientPath}); err == nil {
		t.Fatal("expected missing prepared tracker torrent error")
	}
	preparedPath := filepath.Join(tmp, "[example].release.torrent")
	writeTestMetaInfo(t, preparedPath, metainfo.MetaInfo{InfoBytes: testInfoBytes(t, "EXAMPLE")})
	got, err := PreparedUploadTorrentPath(api.UploadSubject{TorrentPath: preparedPath, ClientTorrentPath: clientPath})
	if err != nil {
		t.Fatalf("prepared tracker torrent: %v", err)
	}
	if got != preparedPath {
		t.Fatalf("prepared tracker torrent path = %q", got)
	}
}

func btnArtifactRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	policy := &UploadArtifactPolicy{Source: "BTN", RequireAnnounce: true}
	if err := registry.RegisterDescriptor(Descriptor{
		Name:           "BTN",
		Definition:     stubDefinition{name: "BTN"},
		UploadArtifact: policy,
	}); err != nil {
		t.Fatalf("register BTN artifact policy: %v", err)
	}
	return registry
}

func hdbArtifactRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry()
	policy := &UploadArtifactPolicy{Source: "HDBits"}
	if err := registry.RegisterDescriptor(Descriptor{
		Name:           "HDB",
		Definition:     stubDefinition{name: "HDB"},
		UploadArtifact: policy,
	}); err != nil {
		t.Fatalf("register HDB artifact policy: %v", err)
	}
	return registry
}

func TestResolveTrackerTorrentArtifactPathPrefixesTrackerName(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Example.Movie.2026.BluRay.1080p.DTS.x264-GRP.mkv")
	got, err := ResolveTrackerTorrentArtifactPath(api.UploadSubject{SourcePath: sourcePath}, filepath.Join(tmp, "state", "upbrr.db"), "BTN")
	if err != nil {
		t.Fatalf("resolve tracker torrent artifact: %v", err)
	}
	if filepath.Base(got) != "[btn].Example.Movie.2026.BluRay.1080p.DTS.x264-GRP.mkv.torrent" {
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

func TestWritePersonalizedTorrentRenamesContentWithoutTouchingPieces(t *testing.T) {
	t.Parallel()

	rename := func(name string, _ ContentNameKind) string { return strings.Replace(name, "H.264", "DDP5.1.H.264", 1) }
	private := true
	pieces := bytes.Repeat([]byte{7}, 40)
	tests := []struct {
		name  string
		info  metainfo.Info
		check func(t *testing.T, info metainfo.Info)
	}{
		{
			name: "single file",
			info: metainfo.Info{
PieceLength: 16 * 1024,
 Pieces: pieces,
 Name: "Show.H.264-GRP.mkv",
 Length: 4,
 Private: &private,
},
			check: func(t *testing.T, info metainfo.Info) {
				t.Helper()
				if info.Name != "Show.DDP5.1.H.264-GRP.mkv" {
					t.Fatalf("name = %q", info.Name)
				}
			},
		},
		{
			name: "multi file",
			info: metainfo.Info{
				PieceLength: 16 * 1024,
 Pieces: pieces,
 Name: "Show.S01.H.264-GRP",
 Private: &private,
				Files: []metainfo.FileInfo{
					{Length: 2, Path: []string{"Show.S01E01.H.264-GRP.mkv"}},
					{
Length: 2,
 Path: []string{"Show.S01E02.H.264-GRP.mkv"},
 PathUtf8: []string{"Show.S01E02.H.264-GRP.mkv"},
},
				},
			},
			check: func(t *testing.T, info metainfo.Info) {
				t.Helper()
				if info.Name != "Show.S01.DDP5.1.H.264-GRP" {
					t.Fatalf("name = %q", info.Name)
				}
				if got := info.Files[0].Path[0]; got != "Show.S01E01.DDP5.1.H.264-GRP.mkv" {
					t.Fatalf("file 0 = %q", got)
				}
				if got := info.Files[1].PathUtf8[0]; got != "Show.S01E02.DDP5.1.H.264-GRP.mkv" {
					t.Fatalf("file 1 utf8 = %q", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "base.torrent")
			outputPath := filepath.Join(dir, "out.torrent")
			infoBytes, err := bencode.Marshal(tt.info)
			if err != nil {
				t.Fatalf("marshal info: %v", err)
			}
			writeTestMetaInfo(t, sourcePath, metainfo.MetaInfo{InfoBytes: infoBytes})

			if err := writePersonalizedTorrent(sourcePath, outputPath, "https://new.example/announce", "ASC", rename); err != nil {
				t.Fatalf("write renamed torrent: %v", err)
			}
			out := readTestMetaInfo(t, outputPath)
			info := testMetaInfoInfo(t, out)
			tt.check(t, info)
			if !bytes.Equal(info.Pieces, pieces) || info.PieceLength != tt.info.PieceLength {
				t.Fatal("renaming must not change piece layout")
			}
			if info.TotalLength() != tt.info.TotalLength() {
				t.Fatalf("total length = %d, want %d", info.TotalLength(), tt.info.TotalLength())
			}
			assertInfoSource(t, out, "ASC")

			// The shared base torrent used by other trackers keeps its original names.
			if baseInfo := testMetaInfoInfo(t, readTestMetaInfo(t, sourcePath)); baseInfo.Name != tt.info.Name {
				t.Fatalf("base torrent name changed to %q", baseInfo.Name)
			}
		})
	}
}

func TestWritePersonalizedTorrentRenameOfHybridTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "base.torrent")
	infoBytes, err := bencode.Marshal(metainfo.Info{
PieceLength: 16 * 1024,
 Name: "Show.H.264-GRP.mkv",
 Length: 4,
 MetaVersion: 2,
})
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	writeTestMetaInfo(t, sourcePath, metainfo.MetaInfo{InfoBytes: infoBytes})

	changing := func(name string, _ ContentNameKind) string { return strings.Replace(name, "H.264", "DDP5.1.H.264", 1) }
	if err := writePersonalizedTorrent(sourcePath, filepath.Join(dir, "changed.torrent"), "", "ASC", changing); err == nil || !strings.Contains(err.Error(), "v2") {
		t.Fatalf("expected v2 rejection when the rename changes a name, got %v", err)
	}
	// An already-compliant hybrid torrent is accepted unchanged.
	unchanged := func(name string, _ ContentNameKind) string { return name }
	if err := writePersonalizedTorrent(sourcePath, filepath.Join(dir, "same.torrent"), "", "ASC", unchanged); err != nil {
		t.Fatalf("hybrid torrent with nothing to rename must still be accepted: %v", err)
	}
}

func TestWritePersonalizedTorrentRejectsInvalidRenamerOutput(t *testing.T) {
	t.Parallel()

	for name, renamed := range map[string]string{
		"empty":     "",
		"dot":       ".",
		"dotdot":    "..",
		"slash":     "a/b.mkv",
		"backslash": `a\b.mkv`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "base.torrent")
			writeTestMetaInfo(t, sourcePath, metainfo.MetaInfo{InfoBytes: testInfoBytes(t, "")})
			err := writePersonalizedTorrent(sourcePath, filepath.Join(dir, "out.torrent"), "", "ASC", func(string, ContentNameKind) string { return renamed })
			if err == nil || !strings.Contains(err.Error(), "invalid renamed path component") {
				t.Fatalf("expected invalid component error, got %v", err)
			}
		})
	}
}

func TestPrepareTrackerUploadTorrentAppliesTrackerRename(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	sourcePath := filepath.Join(tmp, "Release.mkv")
	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	baseTorrentPath := filepath.Join(tmp, "base.torrent")
	writeTestMetaInfo(t, baseTorrentPath, metainfo.MetaInfo{InfoBytes: testInfoBytes(t, "")})

	registry := NewRegistry()
	var seenSubject api.UploadSubject
	if err := registry.RegisterDescriptor(Descriptor{
		Name:       "REN",
		Definition: stubDefinition{name: "REN"},
		UploadArtifact: &UploadArtifactPolicy{Source: "REN"},
		ContentRenamer: func(meta api.UploadSubject, name string, _ ContentNameKind) string {
			seenSubject = meta
			return strings.Replace(name, "Release", "Renamed."+meta.Audio, 1)
		},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	meta, err := prepareTrackerUploadTorrentWithRegistry(api.UploadSubject{
		SourcePath: sourcePath,
 TorrentPath: baseTorrentPath,
 Audio: "DD+ 5.1",
	}, filepath.Join(tmp, "state", "upbrr.db"), "REN", config.TrackerConfig{}, registry)
	if err != nil {
		t.Fatalf("prepare tracker torrent: %v", err)
	}
	info := testMetaInfoInfo(t, readTestMetaInfo(t, meta.TorrentPath))
	if info.Name != "Renamed.DD+ 5.1.mkv" || seenSubject.Audio != "DD+ 5.1" {
		t.Fatalf("renamed torrent name = %q", info.Name)
	}
	if baseInfo := testMetaInfoInfo(t, readTestMetaInfo(t, baseTorrentPath)); baseInfo.Name != "Release.mkv" {
		t.Fatalf("base torrent name changed to %q", baseInfo.Name)
	}
}

// testMetaInfoInfo decodes the info dictionary of a test torrent.
func testMetaInfoInfo(t *testing.T, meta metainfo.MetaInfo) metainfo.Info {
	t.Helper()
	info, err := meta.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	return info
}

func TestWritePersonalizedTorrentKeepsAbsentUTF8PathsAbsent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "base.torrent")
	private := true
	infoBytes, err := bencode.Marshal(metainfo.Info{
		PieceLength: 16 * 1024,
		Pieces:      make([]byte, 20),
		Name:        "Season 03",
		Private:     &private,
		Files: []metainfo.FileInfo{
			{Length: 2, Path: []string{"Show.S03E01.1080p.WEB-DL.H.264-GRP.mkv"}},
			{Length: 2, Path: []string{"Show.S03E02.1080p.WEB-DL.H.264-GRP.mkv"}},
		},
	})
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	writeTestMetaInfo(t, sourcePath, metainfo.MetaInfo{InfoBytes: infoBytes})
	outputPath := filepath.Join(dir, "out.torrent")
	rename := func(name string, _ ContentNameKind) string { return strings.Replace(name, "H.264", "DDP5.1.H.264", 1) }
	if err := writePersonalizedTorrent(sourcePath, outputPath, "", "ASC", rename); err != nil {
		t.Fatalf("write renamed torrent: %v", err)
	}

	out := readTestMetaInfo(t, outputPath)
	if bytes.Contains(out.InfoBytes, []byte("path.utf-8")) {
		t.Fatalf("an absent path.utf-8 was written as an empty list: %q", out.InfoBytes)
	}
	info := testMetaInfoInfo(t, out)
	for _, file := range info.Files {
		if len(file.BestPath()) != 1 || !strings.Contains(file.BestPath()[0], "DDP5.1") {
			t.Fatalf("file path = %v", file.BestPath())
		}
	}
}

func TestRenameTorrentInfoContentPassesComponentKinds(t *testing.T) {
	t.Parallel()

	seen := map[string]ContentNameKind{}
	record := func(name string, kind ContentNameKind) string {
		seen[name] = kind
		return name
	}
	multi := metainfo.Info{
		Name: "Season 03",
		Files: []metainfo.FileInfo{
			{Length: 1, Path: []string{"Extras", "Show.S03E01.mkv"}},
		},
	}
	if _, err := renameTorrentInfoContent(&multi, record); err != nil {
		t.Fatalf("rename multi-file: %v", err)
	}
	if seen["Season 03"] != ContentRootFolderName || seen["Extras"] != ContentSubfolderName || seen["Show.S03E01.mkv"] != ContentFileName {
		t.Fatalf("multi-file kinds = %v", seen)
	}

	single := metainfo.Info{Name: "Show.S03E01.mkv", Length: 1}
	clear(seen)
	if _, err := renameTorrentInfoContent(&single, record); err != nil {
		t.Fatalf("rename single-file: %v", err)
	}
	if seen["Show.S03E01.mkv"] != ContentFileName {
		t.Fatalf("single-file root kind = %v", seen["Show.S03E01.mkv"])
	}
}
