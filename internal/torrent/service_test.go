// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"errors"
	"os"
	slashpath "path" //nolint:depguard // Joins torrent-internal slash-delimited metainfo paths.
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestCreateReusesTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	contentPath := filepath.Join(dir, "sample.bin")
	torrentPath := filepath.Join(dir, "sample.torrent")
	createTestTorrent(t, contentPath, torrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{SourcePath: torrentPath})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != torrentPath {
		t.Fatalf("unexpected torrent path: %s", result.Path)
	}
	if result.InfoHash == "" {
		t.Fatalf("expected info hash to be populated")
	}
}

func TestCreateFallbacksToSibling(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	sibling := source + ".torrent"
	createTestTorrent(t, source, sibling)

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{SourcePath: source})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != sibling {
		t.Fatalf("unexpected torrent path: %s", result.Path)
	}
	if result.InfoHash == "" {
		t.Fatalf("expected info hash to be populated")
	}
}

func TestCreateMissingTorrent(t *testing.T) {
	t.Parallel()

	service := NewService(api.NopLogger{}, t.TempDir())
	_, err := service.Create(context.Background(), api.PreparedMetadata{SourcePath: "/missing/file.mkv"})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestCreateNewTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	if err := os.WriteFile(source, []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tmpRoot := t.TempDir()
	service := NewService(api.NopLogger{}, tmpRoot)
	result, err := service.Create(context.Background(), api.PreparedMetadata{SourcePath: source})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result.Path == "" {
		t.Fatalf("expected torrent path, got empty")
	}
	if !strings.HasPrefix(result.Path, tmpRoot) {
		t.Fatalf("expected torrent path under tmp root, got %s", result.Path)
	}
	if _, err := os.Stat(result.Path); err != nil {
		t.Fatalf("expected torrent file to exist, got %v", err)
	}
	if result.InfoHash == "" {
		t.Fatalf("expected info hash to be populated")
	}
}

func TestCreateHonorsMaxPieceSizeOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	content := make([]byte, 10<<20)
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tmpRoot := t.TempDir()
	service := NewService(api.NopLogger{}, tmpRoot)
	maxPiece := 1
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: source,
		TorrentOverrides: api.TorrentOverrides{
			MaxPieceSizeMiB: &maxPiece,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	torrentMeta, err := metainfo.LoadFromFile(result.Path)
	if err != nil {
		t.Fatalf("load torrent: %v", err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if info.PieceLength > 1<<20 {
		t.Fatalf("expected piece length <= 1 MiB, got %d", info.PieceLength)
	}
}

func TestCreateFolderWithSingleWantedVideoHashesFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "Movie.2026.1080p.WEB-DL-GRP")
	video := filepath.Join(sourceDir, "Movie.2026.1080p.WEB-DL-GRP.mkv")
	writeTestFile(t, video, "video")
	writeTestFile(t, filepath.Join(sourceDir, "proof.jpg"), "proof")

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: sourceDir,
		VideoPath:  video,
		FileList:   []string{video},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	name, files := loadTorrentShape(t, result.Path)
	if name != filepath.Base(video) {
		t.Fatalf("expected single-file torrent name %q, got %q", filepath.Base(video), name)
	}
	if len(files) != 0 {
		t.Fatalf("expected single-file torrent file list to be empty, got %#v", files)
	}
}

func TestCreateFolderPackUsesWantedFileList(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
	episode1 := filepath.Join(sourceDir, "Show.S01E01.mkv")
	episode2 := filepath.Join(sourceDir, "Show.S01E02.mkv")
	writeTestFile(t, episode1, "episode 1")
	writeTestFile(t, episode2, "episode 2")
	writeTestFile(t, filepath.Join(sourceDir, "sample.mkv"), "sample")
	writeTestFile(t, filepath.Join(sourceDir, "proof.jpg"), "proof")

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: sourceDir,
		VideoPath:  episode1,
		FileList:   []string{episode1, episode2},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	name, files := loadTorrentShape(t, result.Path)
	if name != filepath.Base(sourceDir) {
		t.Fatalf("expected torrent root name %q, got %q", filepath.Base(sourceDir), name)
	}
	assertStringSliceEqual(t, files, []string{"Show.S01E01.mkv", "Show.S01E02.mkv"})
}

func TestCreateFolderPackEscapesWantedFilePatterns(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
	episode1 := filepath.Join(sourceDir, "Show, [S01E01].mkv")
	episode2 := filepath.Join(sourceDir, "Show.{S01E02}.mkv")
	writeTestFile(t, episode1, "episode 1")
	writeTestFile(t, episode2, "episode 2")
	writeTestFile(t, filepath.Join(sourceDir, "Showx [S01E01].mkv"), "ambiguous extra")
	writeTestFile(t, filepath.Join(sourceDir, "Show.S01E03.mkv"), "extra")

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: sourceDir,
		VideoPath:  episode1,
		FileList:   []string{episode1, episode2},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, files := loadTorrentShape(t, result.Path)
	assertStringSliceEqual(t, files, []string{"Show, [S01E01].mkv", "Show.{S01E02}.mkv"})
}

func TestCreateDiscFolderIgnoresWantedFileList(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Movie.2026.COMPLETE.BLURAY-GRP")
	stream := filepath.Join(sourceDir, "BDMV", "STREAM", "00001.m2ts")
	writeTestFile(t, stream, "stream")
	writeTestFile(t, filepath.Join(sourceDir, "BDMV", "PLAYLIST", "00001.mpls"), "playlist")
	writeTestFile(t, filepath.Join(sourceDir, "CERTIFICATE", "id.bdmv"), "certificate")

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: sourceDir,
		DiscType:   "BDMV",
		VideoPath:  stream,
		FileList:   []string{stream},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	name, files := loadTorrentShape(t, result.Path)
	if name != filepath.Base(sourceDir) {
		t.Fatalf("expected torrent root name %q, got %q", filepath.Base(sourceDir), name)
	}
	assertStringSliceEqual(t, files, []string{
		"BDMV/PLAYLIST/00001.mpls",
		"BDMV/STREAM/00001.m2ts",
		"CERTIFICATE/id.bdmv",
	})
}

func TestCreateFolderSkipsMkbrrIgnoredFilesInValidation(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Movie.2026.1080p.WEB-DL-GRP")
	writeTestFile(t, filepath.Join(sourceDir, "Movie.2026.1080p.WEB-DL-GRP.mkv"), "video")
	writeTestFile(t, filepath.Join(sourceDir, "desktop.ini"), "ignored")
	writeTestFile(t, filepath.Join(sourceDir, ".ds_store"), "ignored")
	writeTestFile(t, filepath.Join(sourceDir, "thumbs.db"), "ignored")
	writeTestFile(t, filepath.Join(sourceDir, "sample.torrent"), "ignored")
	writeTestFile(t, filepath.Join(sourceDir, "movie.mkv.zone.identifier"), "ignored")
	writeTestFile(t, filepath.Join(sourceDir, "@eaDir", "metadata.bin"), "ignored")

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: sourceDir,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, files := loadTorrentShape(t, result.Path)
	assertStringSliceEqual(t, files, []string{"Movie.2026.1080p.WEB-DL-GRP.mkv"})
}

func TestSafeTorrentRootNameRejectsFilesystemRoot(t *testing.T) {
	t.Parallel()

	root := string(filepath.Separator)
	if volume := filepath.VolumeName(t.TempDir()); volume != "" {
		root = volume + string(filepath.Separator)
	}

	if _, err := safeTorrentRootName(root); err == nil {
		t.Fatalf("expected filesystem root to be rejected")
	}
}

func TestCreateNoHashRequiresReusableTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	if err := os.WriteFile(source, []byte("data"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	service := NewService(api.NopLogger{}, t.TempDir())
	reuseOnly := true
	_, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: source,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected nohash to fail without reusable torrent, got %v", err)
	}
}

func TestCreateRehashBypassesReusableTempTorrent(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "video.mkv")
	if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	tmpRoot := t.TempDir()
	service := NewService(api.NopLogger{}, tmpRoot)
	meta := api.PreparedMetadata{SourcePath: source}

	tmpTorrentPath, err := TempTorrentPath(tmpRoot, meta, source)
	if err != nil {
		t.Fatalf("temp torrent path: %v", err)
	}
	createTestTorrent(t, source, tmpTorrentPath)

	oldTime := filepath.Join(sourceDir, "marker")
	if err := os.WriteFile(oldTime, []byte("marker"), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	past := (mustStat(t, oldTime)).ModTime().Add(-2 * time.Hour)
	if err := os.Chtimes(tmpTorrentPath, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	rehash := true
	meta.TorrentOverrides = api.TorrentOverrides{Rehash: &rehash}
	result, err := service.Create(context.Background(), meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != tmpTorrentPath {
		t.Fatalf("expected recreated torrent at temp path %s, got %s", tmpTorrentPath, result.Path)
	}
	if got := mustStat(t, result.Path).ModTime(); !got.After(past) {
		t.Fatalf("expected rehash to recreate torrent, modtime %v was not after %v", got, past)
	}
}

func TestPieceExpForMiB(t *testing.T) {
	t.Parallel()

	cases := map[int]uint{
		1:   20,
		2:   21,
		4:   22,
		8:   23,
		16:  24,
		32:  25,
		64:  26,
		128: 27,
	}

	for input, expected := range cases {
		got, ok := pieceExpForMiB(input)
		if !ok {
			t.Fatalf("expected %d MiB to be supported", input)
		}
		if got != expected {
			t.Fatalf("%d MiB: expected exp %d, got %d", input, expected, got)
		}
	}
	if _, ok := pieceExpForMiB(3); ok {
		t.Fatal("expected unsupported value to return false")
	}
}

func TestApplyTorrentOverridePieceOptionsKeepsUserMax(t *testing.T) {
	t.Parallel()

	maxPiece := 16
	requiredExp := uint(26)

	options := applyTorrentOverridePieceOptions(api.PreparedMetadata{
		TorrentOverrides: api.TorrentOverrides{
			MaxPieceSizeMiB: &maxPiece,
		},
	}, mkbrrPieceOptions{
		maxPieceExp: 27,
		pieceExp:    &requiredExp,
	})

	if options.maxPieceExp != 24 {
		t.Fatalf("expected user max exponent 24, got %d", options.maxPieceExp)
	}
	if options.pieceExp == nil || *options.pieceExp != requiredExp {
		t.Fatalf("expected required piece exponent %d to remain set, got %#v", requiredExp, options.pieceExp)
	}
}

func TestApplyTorrentOverridePieceOptionsCapsToTrackerMax(t *testing.T) {
	t.Parallel()

	maxPiece := 128

	options := applyTorrentOverridePieceOptions(api.PreparedMetadata{
		TorrentOverrides: api.TorrentOverrides{
			MaxPieceSizeMiB: &maxPiece,
		},
	}, mkbrrPieceOptions{
		maxPieceExp: 24,
	})

	if options.maxPieceExp != 24 {
		t.Fatalf("expected tracker max exponent 24, got %d", options.maxPieceExp)
	}
}

func TestCreateReusesAssociatedTempTorrent(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "video.mkv")
	if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	tmpRoot := t.TempDir()
	service := NewService(api.NopLogger{}, tmpRoot)

	meta := api.PreparedMetadata{SourcePath: source}
	tmpTorrentPath, err := TempTorrentPath(tmpRoot, meta, source)
	if err != nil {
		t.Fatalf("temp torrent path: %v", err)
	}
	createTestTorrent(t, source, tmpTorrentPath)

	result, err := service.Create(context.Background(), meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != tmpTorrentPath {
		t.Fatalf("expected temp torrent path %s, got %s", tmpTorrentPath, result.Path)
	}
	if result.InfoHash == "" {
		t.Fatalf("expected info hash to be populated")
	}
}

func TestCreatePrefersClientTorrentOverAssociatedTempTorrent(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "video.mkv")
	if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	tmpRoot := t.TempDir()
	service := NewService(api.NopLogger{}, tmpRoot)
	meta := api.PreparedMetadata{SourcePath: source}

	tmpTorrentPath, err := TempTorrentPath(tmpRoot, meta, source)
	if err != nil {
		t.Fatalf("temp torrent path: %v", err)
	}
	createTestTorrent(t, source, tmpTorrentPath)

	clientTorrentPath := filepath.Join(sourceDir, "client.torrent")
	createTestTorrent(t, source, clientTorrentPath)

	meta.ClientTorrentPath = clientTorrentPath
	result, err := service.Create(context.Background(), meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != clientTorrentPath {
		t.Fatalf("expected client torrent path %s, got %s", clientTorrentPath, result.Path)
	}
	if result.InfoHash == "" {
		t.Fatalf("expected info hash to be populated")
	}
}

func TestCreateRejectsMismatchedClientTorrent(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "video.mkv")
	writeTestFile(t, source, "source-data")

	tmpRoot := t.TempDir()
	service := NewService(api.NopLogger{}, tmpRoot)
	meta := api.PreparedMetadata{SourcePath: source}

	tmpTorrentPath, err := TempTorrentPath(tmpRoot, meta, source)
	if err != nil {
		t.Fatalf("temp torrent path: %v", err)
	}
	createTestTorrent(t, source, tmpTorrentPath)

	clientSource := filepath.Join(sourceDir, "client.bin")
	clientTorrentPath := filepath.Join(sourceDir, "client.torrent")
	createTestTorrent(t, clientSource, clientTorrentPath)

	meta.ClientTorrentPath = clientTorrentPath
	result, err := service.Create(context.Background(), meta)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != tmpTorrentPath {
		t.Fatalf("expected mismatched client torrent to be skipped for temp torrent %s, got %s", tmpTorrentPath, result.Path)
	}
}

func TestCreateRejectsSameNameDifferentSizeClientTorrent(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "video.mkv")
	writeTestFile(t, source, "source-data")

	clientDir := filepath.Join(sourceDir, "client")
	clientSource := filepath.Join(clientDir, "video.mkv")
	writeTestFile(t, clientSource, "different-size-source-data")
	clientTorrentPath := filepath.Join(sourceDir, "client.torrent")
	createTestTorrentFromExisting(t, clientSource, clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		ClientTorrentPath: clientTorrentPath,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path == clientTorrentPath {
		t.Fatalf("expected same-name different-size client torrent to be skipped")
	}
}

func TestCreateRejectsSameNameSameSizeDifferentContentClientTorrent(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "video.mkv")
	writeTestFile(t, source, "source-data")

	clientDir := filepath.Join(sourceDir, "client")
	clientSource := filepath.Join(clientDir, "video.mkv")
	writeTestFile(t, clientSource, "source-evil")
	clientTorrentPath := filepath.Join(sourceDir, "client.torrent")
	createTestTorrentFromExisting(t, clientSource, clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		ClientTorrentPath: clientTorrentPath,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path == clientTorrentPath {
		t.Fatalf("expected same-name same-size different-content client torrent to be skipped")
	}
}

func TestCreateRejectsDifferentRootClientTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "Show.S01.1080p.WEB-DL-GRP")
	episode1 := filepath.Join(sourceDir, "Show.S01E01.mkv")
	episode2 := filepath.Join(sourceDir, "Show.S01E02.mkv")
	writeTestFile(t, episode1, "episode 1")
	writeTestFile(t, episode2, "episode 2")

	clientDir := filepath.Join(dir, "Other.Show.S01.1080p.WEB-DL-GRP")
	writeTestFile(t, filepath.Join(clientDir, "Show.S01E01.mkv"), "episode 1")
	writeTestFile(t, filepath.Join(clientDir, "Show.S01E02.mkv"), "episode 2")
	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createTestTorrentFromExisting(t, clientDir, clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        sourceDir,
		VideoPath:         episode1,
		FileList:          []string{episode1, episode2},
		ClientTorrentPath: clientTorrentPath,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path == clientTorrentPath {
		t.Fatalf("expected different-root client torrent to be skipped")
	}
}

func TestCreateRejectsWantedFileOutsideRoot(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Movie.2026.1080p.WEB-DL-GRP")
	video := filepath.Join(sourceDir, "Movie.2026.1080p.WEB-DL-GRP.mkv")
	writeTestFile(t, video, "video")
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	writeTestFile(t, outside, "outside")

	service := NewService(api.NopLogger{}, t.TempDir())
	_, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: sourceDir,
		VideoPath:  video,
		FileList:   []string{outside},
	})
	if err == nil {
		t.Fatalf("expected outside wanted file to be rejected")
	}
}

func TestCreateRegeneratesNonCompliantPTPTorrent(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "movie.mkv")
	content := make([]byte, 70<<20)
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	clientTorrentPath := filepath.Join(sourceDir, "client.torrent")
	wrongPiece := uint(16)
	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:           source,
		OutputPath:     clientTorrentPath,
		IsPrivate:      true,
		PieceLengthExp: &wrongPiece,
	})
	if err != nil {
		t.Fatalf("create client torrent: %v", err)
	}

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		SourceSize:        int64(len(content)),
		Trackers:          []string{"PTP"},
		ClientTorrentPath: clientTorrentPath,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path == clientTorrentPath {
		t.Fatalf("expected non-compliant client torrent to be regenerated")
	}

	torrentMeta, err := metainfo.LoadFromFile(result.Path)
	if err != nil {
		t.Fatalf("load torrent: %v", err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if info.PieceLength != 1<<17 {
		t.Fatalf("expected 128 KiB piece size, got %d", info.PieceLength)
	}
}

func TestPTPPiecePolicyBoundaries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		size uint64
		exp  uint
	}{
		{size: 58 << 20, exp: 16},
		{size: 59 << 20, exp: 17},
		{size: 122 << 20, exp: 17},
		{size: 123 << 20, exp: 18},
		{size: 213 << 20, exp: 18},
		{size: 214 << 20, exp: 19},
		{size: 444 << 20, exp: 19},
		{size: 445 << 20, exp: 20},
		{size: 922 << 20, exp: 20},
		{size: 923 << 20, exp: 21},
		{size: 3977 << 20, exp: 21},
		{size: 3978 << 20, exp: 22},
		{size: 6861 << 20, exp: 22},
		{size: 6862 << 20, exp: 23},
		{size: 14234 << 20, exp: 23},
		{size: 14235 << 20, exp: 24},
	}

	for _, tc := range cases {
		meta := api.PreparedMetadata{Trackers: []string{"PTP"}, SourceSize: int64(tc.size)}
		policy := resolveTrackerPolicy(meta)
		got, ok := policy.requiredPieceExp(meta)
		if !ok {
			t.Fatalf("expected piece exponent for size %d", tc.size)
		}
		if got != tc.exp {
			t.Fatalf("size %d: expected exp %d, got %d", tc.size, tc.exp, got)
		}
	}
}

func TestCreateRegeneratesOversizedANTTorrent(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "movie.mkv")
	file, err := os.Create(source)
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	const sourceSize = int64(900 << 20)
	if err := file.Truncate(sourceSize); err != nil {
		_ = file.Close()
		t.Fatalf("truncate source: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}

	clientTorrentPath := filepath.Join(sourceDir, "client.torrent")
	wrongPiece := uint(16)
	_, err = mkbrr.Create(mkbrr.CreateOptions{
		Path:           source,
		OutputPath:     clientTorrentPath,
		IsPrivate:      true,
		MaxPieceLength: &wrongPiece,
		PieceLengthExp: &wrongPiece,
	})
	if err != nil {
		t.Fatalf("create client torrent: %v", err)
	}
	info, err := os.Stat(clientTorrentPath)
	if err != nil {
		t.Fatalf("stat client torrent: %v", err)
	}
	if info.Size() <= antMaxTorrentBytes {
		t.Fatalf("expected oversized ANT torrent fixture, got %d bytes", info.Size())
	}

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		SourceSize:        sourceSize,
		Trackers:          []string{"ANT"},
		ClientTorrentPath: clientTorrentPath,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path == clientTorrentPath {
		t.Fatalf("expected oversized client torrent to be regenerated")
	}
	regenerated, err := os.Stat(result.Path)
	if err != nil {
		t.Fatalf("stat regenerated torrent: %v", err)
	}
	if regenerated.Size() > antMaxTorrentBytes {
		t.Fatalf("expected regenerated torrent <= %d bytes, got %d", antMaxTorrentBytes, regenerated.Size())
	}
}

func createTestTorrent(t *testing.T, sourcePath, torrentPath string) {
	t.Helper()

	if err := os.WriteFile(sourcePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:       sourcePath,
		OutputPath: torrentPath,
		IsPrivate:  true,
	})
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}
}

func createTestTorrentFromExisting(t *testing.T, sourcePath, torrentPath string) {
	t.Helper()

	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:       sourcePath,
		OutputPath: torrentPath,
		IsPrivate:  true,
	})
	if err != nil {
		t.Fatalf("create torrent: %v", err)
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("make parent dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func loadTorrentShape(t *testing.T, torrentPath string) (string, []string) {
	t.Helper()

	torrentMeta, err := metainfo.LoadFromFile(torrentPath)
	if err != nil {
		t.Fatalf("load torrent: %v", err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	files := make([]string, 0, len(info.Files))
	for _, file := range info.Files {
		//pathpolicy:allow torrent metainfo paths are slash-delimited data
		files = append(files, slashpath.Join(file.BestPath()...))
	}
	slices.Sort(files)
	return info.BestName(), files
}

func assertStringSliceEqual(t *testing.T, got []string, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Fatalf("unexpected torrent files:\nwant %#v\ngot  %#v", want, got)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info
}
