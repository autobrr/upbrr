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

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/merkle"
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

func TestCreateEmptySourceDoesNotEmitProgress(t *testing.T) {
	t.Parallel()

	updates := make([]api.UploadProgressUpdate, 0)
	ctx := api.WithUploadProgressReporter(context.Background(), func(update api.UploadProgressUpdate) {
		updates = append(updates, update)
	})

	service := NewService(api.NopLogger{}, t.TempDir())
	_, err := service.Create(ctx, api.PreparedMetadata{SourcePath: " \t "})
	if !errors.Is(err, internalerrors.ErrInvalidInput) {
		t.Fatalf("expected invalid input, got %v", err)
	}
	if len(updates) != 0 {
		t.Fatalf("expected no progress updates, got %#v", updates)
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
	torrentMeta, err := metainfo.LoadFromFile(result.Path)
	if err != nil {
		t.Fatalf("load torrent: %v", err)
	}
	if torrentMeta.CreatedBy != "uploaded with upbrr using mkbrr" {
		t.Fatalf("expected mkbrr created-by, got %q", torrentMeta.CreatedBy)
	}
	if torrentMeta.Comment != "" {
		t.Fatalf("expected mkbrr comment to be empty, got %q", torrentMeta.Comment)
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

func TestCreateFolderPackRejectsMissingWantedFile(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
	episode1 := filepath.Join(sourceDir, "Show.S01E01.mkv")
	missing := filepath.Join(sourceDir, "Show.S01E02.mkv")
	writeTestFile(t, episode1, "episode 1")

	service := NewService(api.NopLogger{}, t.TempDir())
	_, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: sourceDir,
		VideoPath:  episode1,
		FileList:   []string{missing},
	})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected missing wanted file to fail with not found, got %v", err)
	}
}

func TestCreateFolderPackRejectsPartialMissingWantedFile(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
	episode1 := filepath.Join(sourceDir, "Show.S01E01.mkv")
	missing := filepath.Join(sourceDir, "Show.S01E02.mkv")
	writeTestFile(t, episode1, "episode 1")

	service := NewService(api.NopLogger{}, t.TempDir())
	_, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath: sourceDir,
		VideoPath:  episode1,
		FileList:   []string{episode1, missing},
	})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected partial missing wanted file to fail with not found, got %v", err)
	}
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

func TestCreateDiscMarkerFolderUsesSelectedRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		discType string
		files    []string
	}{
		{
			name:     "BDMV",
			discType: "BDMV",
			files: []string{
				filepath.Join("PLAYLIST", "00001.mpls"),
				filepath.Join("STREAM", "00001.m2ts"),
			},
		},
		{
			name:     "VIDEO_TS",
			discType: "VIDEO_TS",
			files: []string{
				"VIDEO_TS.IFO",
				"VTS_01_1.VOB",
			},
		},
		{
			name:     "HVDVD_TS",
			discType: "HVDVD_TS",
			files: []string{
				"HVA00001.EVO",
				"HVDVD_TS.IFO",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parent := filepath.Join(t.TempDir(), "Movie.2026.COMPLETE.DISC-GRP")
			sourceDir := filepath.Join(parent, tt.name)
			for _, rel := range tt.files {
				writeTestFile(t, filepath.Join(sourceDir, rel), "disc data")
			}
			writeTestFile(t, filepath.Join(parent, "SIBLING", "extra.bin"), "sibling")
			writeTestFile(t, filepath.Join(parent, "outside.txt"), "outside")

			service := NewService(api.NopLogger{}, t.TempDir())
			result, err := service.Create(context.Background(), api.PreparedMetadata{
				SourcePath: sourceDir,
				DiscType:   tt.discType,
				VideoPath:  filepath.Join(sourceDir, tt.files[0]),
				FileList:   []string{filepath.Join(sourceDir, tt.files[0])},
			})
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			name, files := loadTorrentShape(t, result.Path)
			if name != tt.name {
				t.Fatalf("expected torrent root name %q, got %q", tt.name, name)
			}
			expected := make([]string, 0, len(tt.files))
			for _, rel := range tt.files {
				expected = append(expected, filepath.ToSlash(rel))
			}
			assertStringSliceEqual(t, files, expected)
		})
	}
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

func TestCreateNoHashReusesExactCaseClientTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "Video.mkv")
	writeTestFile(t, source, "source-data")

	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createTestTorrentFromExisting(t, source, clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	reuseOnly := true
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		ClientTorrentPath: clientTorrentPath,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != clientTorrentPath {
		t.Fatalf("expected exact-case client torrent path %s, got %s", clientTorrentPath, result.Path)
	}
	if result.CrossSeed {
		t.Fatalf("expected client torrent reuse to not be marked cross-seed")
	}
}

func TestCreateNoHashRejectsCaseOnlySingleFileClientTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "Video.mkv")
	writeTestFile(t, source, "source-data")

	clientDir := filepath.Join(dir, "client")
	clientSource := filepath.Join(clientDir, "video.mkv")
	writeTestFile(t, clientSource, "source-data")
	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createTestTorrentFromExisting(t, clientSource, clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	reuseOnly := true
	_, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		ClientTorrentPath: clientTorrentPath,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected nohash to reject case-only single-file mismatch, got %v", err)
	}
}

func TestCreateNoHashRejectsCaseOnlyMultiFileClientTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "Show.S01.1080p.WEB-DL-GRP")
	episode1 := filepath.Join(sourceDir, "Show.S01E01.mkv")
	episode2 := filepath.Join(sourceDir, "Show.S01E02.mkv")
	writeTestFile(t, episode1, "episode 1")
	writeTestFile(t, episode2, "episode 2")
	if caseSensitiveFilesystem(t, dir) {
		writeTestFile(t, filepath.Join(sourceDir, "show.s01e01.mkv"), "episode 1")
		writeTestFile(t, filepath.Join(sourceDir, "show.s01e02.mkv"), "episode 2")
	}

	clientDir := filepath.Join(dir, "client", filepath.Base(sourceDir))
	writeTestFile(t, filepath.Join(clientDir, "show.s01e01.mkv"), "episode 1")
	writeTestFile(t, filepath.Join(clientDir, "show.s01e02.mkv"), "episode 2")
	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createTestTorrentFromExisting(t, clientDir, clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	reuseOnly := true
	_, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        sourceDir,
		VideoPath:         episode1,
		FileList:          []string{episode1, episode2},
		ClientTorrentPath: clientTorrentPath,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected nohash to reject case-only multi-file mismatch, got %v", err)
	}
}

func TestCreateNoHashReusesPureV2ClientTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	content := []byte("source-data")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createPureV2TestTorrent(t, source, content, clientTorrentPath)
	if err := validateTorrentContent(clientTorrentPath, api.PreparedMetadata{SourcePath: source}); err != nil {
		t.Fatalf("expected pure v2 torrent to validate, got %v", err)
	}

	service := NewService(api.NopLogger{}, t.TempDir())
	reuseOnly := true
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		ClientTorrentPath: clientTorrentPath,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != clientTorrentPath {
		t.Fatalf("expected pure v2 client torrent path %s, got %s", clientTorrentPath, result.Path)
	}
}

func TestCreateNoHashReusesPureV2ZeroLengthClientTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createPureV2TestTorrent(t, source, nil, clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	reuseOnly := true
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		ClientTorrentPath: clientTorrentPath,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != clientTorrentPath {
		t.Fatalf("expected zero-length pure v2 client torrent path %s, got %s", clientTorrentPath, result.Path)
	}
}

func TestCreateNoHashReusesPureV2SameNameSameSizeDifferentContent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	sourceContent := []byte("source-data")
	if err := os.WriteFile(source, sourceContent, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createPureV2TestTorrent(t, source, []byte("source-evil"), clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	reuseOnly := true
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		ClientTorrentPath: clientTorrentPath,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != clientTorrentPath {
		t.Fatalf("expected same-name same-size pure v2 client torrent path %s, got %s", clientTorrentPath, result.Path)
	}
}

func TestValidateTorrentContentAllowsPureV2NonEmptyFileWithoutPiecesRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	content := []byte("source-data")
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createPureV2TestTorrentWithPiecesRoot(t, source, content, clientTorrentPath, false)

	err := validateTorrentContent(clientTorrentPath, api.PreparedMetadata{SourcePath: source})
	if err != nil {
		t.Fatalf("expected metadata-only validation to pass, got %v", err)
	}
}

func TestCreateNoHashReusesSameNameSameSizeDifferentContentTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	writeTestFile(t, source, "source-data")

	clientDir := filepath.Join(dir, "client")
	clientSource := filepath.Join(clientDir, "video.mkv")
	writeTestFile(t, clientSource, "source-evil")
	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createTestTorrentFromExisting(t, clientSource, clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	reuseOnly := true
	result, err := service.Create(context.Background(), api.PreparedMetadata{
		SourcePath:        source,
		ClientTorrentPath: clientTorrentPath,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != clientTorrentPath {
		t.Fatalf("expected same-name same-size client torrent path %s, got %s", clientTorrentPath, result.Path)
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

func TestCreateRehashOverridesNoHashReusableTorrents(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, source string, tmpTorrentPath string) (api.PreparedMetadata, string){
		"client": func(t *testing.T, source string, _ string) (api.PreparedMetadata, string) {
			t.Helper()

			clientTorrentPath := filepath.Join(filepath.Dir(source), "client.torrent")
			createTestTorrentFromExisting(t, source, clientTorrentPath)
			return api.PreparedMetadata{SourcePath: source, ClientTorrentPath: clientTorrentPath}, clientTorrentPath
		},
		"adjacent": func(t *testing.T, source string, _ string) (api.PreparedMetadata, string) {
			t.Helper()

			adjacentTorrentPath := source + ".torrent"
			createTestTorrentFromExisting(t, source, adjacentTorrentPath)
			return api.PreparedMetadata{SourcePath: source}, adjacentTorrentPath
		},
		"temp": func(t *testing.T, source string, tmpTorrentPath string) (api.PreparedMetadata, string) {
			t.Helper()

			createTestTorrentFromExisting(t, source, tmpTorrentPath)
			past := mustStat(t, tmpTorrentPath).ModTime().Add(-2 * time.Hour)
			if err := os.Chtimes(tmpTorrentPath, past, past); err != nil {
				t.Fatalf("chtimes: %v", err)
			}
			return api.PreparedMetadata{SourcePath: source}, tmpTorrentPath
		},
	}

	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sourceDir := t.TempDir()
			source := filepath.Join(sourceDir, "video.mkv")
			if err := os.WriteFile(source, []byte("source-data"), 0o600); err != nil {
				t.Fatalf("write source: %v", err)
			}

			tmpRoot := t.TempDir()
			service := NewService(api.NopLogger{}, tmpRoot)
			tmpTorrentPath, err := TempTorrentPath(tmpRoot, api.PreparedMetadata{SourcePath: source}, source)
			if err != nil {
				t.Fatalf("temp torrent path: %v", err)
			}

			meta, reusablePath := setup(t, source, tmpTorrentPath)
			past := time.Time{}
			if name == "temp" {
				past = mustStat(t, tmpTorrentPath).ModTime()
			}
			reuseOnly := true
			rehash := true
			meta.TorrentOverrides = api.TorrentOverrides{
				NoHash: &reuseOnly,
				Rehash: &rehash,
			}

			result, err := service.Create(context.Background(), meta)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if result.Path != tmpTorrentPath {
				t.Fatalf("expected rehash to create temp torrent %s, got %s", tmpTorrentPath, result.Path)
			}
			if reusablePath != tmpTorrentPath && result.Path == reusablePath {
				t.Fatalf("expected rehash to ignore reusable torrent %s", reusablePath)
			}
			if !past.IsZero() {
				if got := mustStat(t, result.Path).ModTime(); !got.After(past) {
					t.Fatalf("expected rehash to recreate torrent, modtime %v was not after %v", got, past)
				}
			}
		})
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

func TestCreateReusesSameNameSameSizeDifferentContentClientTorrent(t *testing.T) {
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
	if result.Path != clientTorrentPath {
		t.Fatalf("expected same-name same-size client torrent path %s, got %s", clientTorrentPath, result.Path)
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

func createPureV2TestTorrent(t *testing.T, sourcePath string, content []byte, torrentPath string) {
	t.Helper()

	createPureV2TestTorrentWithPiecesRoot(t, sourcePath, content, torrentPath, len(content) > 0)
}

func createPureV2TestTorrentWithPiecesRoot(t *testing.T, sourcePath string, content []byte, torrentPath string, includePiecesRoot bool) {
	t.Helper()

	piecesRoot := ""
	if includePiecesRoot {
		hash := merkle.NewHash()
		if _, err := hash.Write(content); err != nil {
			t.Fatalf("hash v2 content: %v", err)
		}
		piecesRoot = string(hash.Sum(nil))
	}
	info := metainfo.Info{
		PieceLength: 1 << 14,
		Name:        filepath.Base(sourcePath),
		MetaVersion: 2,
		FileTree: metainfo.FileTree{
			File: metainfo.FileTreeFile{
				Length:     int64(len(content)),
				PiecesRoot: piecesRoot,
			},
		},
	}
	infoBytes, err := bencode.Marshal(&info)
	if err != nil {
		t.Fatalf("marshal v2 info: %v", err)
	}
	meta := metainfo.MetaInfo{InfoBytes: infoBytes}
	meta.SetDefaults()
	file, err := os.Create(torrentPath)
	if err != nil {
		t.Fatalf("create v2 torrent: %v", err)
	}
	if err := meta.Write(file); err != nil {
		_ = file.Close()
		t.Fatalf("write v2 torrent: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close v2 torrent: %v", err)
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

func caseSensitiveFilesystem(t *testing.T, dir string) bool {
	t.Helper()

	probe := filepath.Join(dir, "CaseProbe")
	if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
		t.Fatalf("write case probe: %v", err)
	}
	defer func() {
		if err := os.Remove(probe); err != nil && !os.IsNotExist(err) {
			t.Fatalf("remove case probe: %v", err)
		}
	}()

	_, err := os.Stat(filepath.Join(dir, "caseprobe"))
	if err == nil {
		return false
	}
	if os.IsNotExist(err) {
		return true
	}
	t.Fatalf("stat case probe: %v", err)
	return false
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
