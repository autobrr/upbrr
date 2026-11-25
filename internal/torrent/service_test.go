// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	slashpath "path" //nolint:depguard // Joins torrent-internal slash-delimited metainfo paths.
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"

	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/internal/filelock"
	"github.com/autobrr/upbrr/internal/filesystem"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ant"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/hdb"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ptp"
	"github.com/autobrr/upbrr/pkg/api"
)

const antMaxTorrentBytesForTest int64 = 250 << 10

func TestCreateReusesTorrent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	contentPath := filepath.Join(dir, "sample.bin")
	torrentPath := filepath.Join(dir, "sample.torrent")
	createTestTorrent(t, contentPath, torrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.TorrentSubject{SourcePath: torrentPath})
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
	result, err := service.Create(context.Background(), api.TorrentSubject{SourcePath: source})
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
	_, err := service.Create(context.Background(), api.TorrentSubject{SourcePath: "/missing/file.mkv"})
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
	_, err := service.Create(ctx, api.TorrentSubject{SourcePath: " \t "})
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
	result, err := service.Create(context.Background(), api.TorrentSubject{SourcePath: source})
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
	if torrentMeta.CreatedBy != "upbrr with mkbrr" {
		t.Fatalf("expected mkbrr created-by, got %q", torrentMeta.CreatedBy)
	}
	if torrentMeta.Comment != "uploaded with upbrr" {
		t.Fatalf("expected upbrr comment, got %q", torrentMeta.Comment)
	}
}

func TestCreateHonorsMaxPieceSizeOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	// Without the override, the 1,000-piece target chooses 2 MiB pieces.
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(source, 2<<30); err != nil {
		t.Fatal(err)
	}

	tmpRoot := t.TempDir()
	service := NewService(api.NopLogger{}, tmpRoot)
	maxPiece := 1
	result, err := service.Create(context.Background(), api.TorrentSubject{
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

func TestCreateTrustsPTPPieceProfile(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "video.mkv")
	writeTestFile(t, source, "data")
	service := NewService(api.NopLogger{}, t.TempDir())
	_, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: source,
		SourceSize: 494 << 20,
		Trackers:   []string{"PTP"},
	})
	if err != nil {
		t.Fatalf("create PTP torrent: %v", err)
	}
}

func TestCreateFolderWithSingleWantedVideoHashesFile(t *testing.T) {
	t.Parallel()
	for _, folderName := range []string{"Example.Movie.2026.1080p.WEB-DL-GRP", "Example.Movie.2026.1080p.WEB-DL-GRP.mkv"} {
		for _, keep := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s keep=%t", folderName, keep), func(t *testing.T) {
				sourceDir := filepath.Join(t.TempDir(), folderName)
				video := filepath.Join(sourceDir, "Example.Movie.2026.1080p.WEB-DL-GRP.mkv")
				writeTestFile(t, video, "video")
				for _, name := range []string{"sample.mkv", "release.nfo", "notes.txt", "proof.jpg"} {
					writeTestFile(t, filepath.Join(sourceDir, name), "sidecar")
				}
				service := NewService(api.NopLogger{}, t.TempDir())
				subject := api.TorrentSubject{
					SourcePath: sourceDir,
					FileList:   []string{video},
					KeepFolder: keep,
				}
				result, err := service.Create(t.Context(), subject)
				if err != nil {
					t.Fatal(err)
				}
				name, files := loadTorrentShape(t, result.Path)
				if keep {
					if name != filepath.Base(sourceDir) || len(files) != 1 || files[0] != filepath.Base(video) {
						t.Fatalf("kept folder layout = %q %v", name, files)
					}
				} else if name != filepath.Base(video) || len(files) != 0 {
					t.Fatalf("single-file layout = %q %v", name, files)
				}
				inventory, ok, err := ResolveSubmissionContentInventory(subject)
				if err != nil || !ok || len(inventory.Files) != 1 || inventory.Files[0].LocalPath != video {
					t.Fatalf("selected content inventory = %+v ok=%t err=%v", inventory, ok, err)
				}
				wantScope := api.SubmissionContentScopeSingleFile
				if keep {
					wantScope = api.SubmissionContentScopeFileSet
				}
				if inventory.Scope != wantScope {
					t.Fatalf("scope=%s want=%s", inventory.Scope, wantScope)
				}
				subject.KeepFolder = !keep
				if err := validateTorrentContent(result.Path, subject); err == nil {
					t.Fatal("opposite folder layout accepted for reuse")
				}
			})
		}
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
	result, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: sourceDir,
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
	_, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: sourceDir,
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
	_, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: sourceDir,
		FileList:   []string{episode1, missing},
	})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected partial missing wanted file to fail with not found, got %v", err)
	}
}

func TestCreateFolderPackEscapesWantedFilePatterns(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), wantedStagingLockName)
	episode1 := filepath.Join(sourceDir, "Show, [S01E01].mkv")
	episode2 := filepath.Join(sourceDir, "Show.{S01E02}.mkv")
	writeTestFile(t, episode1, "episode 1")
	writeTestFile(t, episode2, "episode 2")
	writeTestFile(t, filepath.Join(sourceDir, "Showx [S01E01].mkv"), "ambiguous extra")
	writeTestFile(t, filepath.Join(sourceDir, "Show.S01E03.mkv"), "extra")

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: sourceDir,
		FileList:   []string{episode1, episode2},
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	name, files := loadTorrentShape(t, result.Path)
	if name != wantedStagingLockName {
		t.Fatalf("staged torrent root = %q, want %q", name, wantedStagingLockName)
	}
	assertStringSliceEqual(t, files, []string{"Show, [S01E01].mkv", "Show.{S01E02}.mkv"})
}

func TestCreateFolderPackStagesRelativeSymlink(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
	target := filepath.Join("originals", "one.mkv")
	episode1 := filepath.Join(source, "Episode, 1.mkv")
	episode2 := filepath.Join(source, "Episode 2.mkv")
	writeTestFile(t, filepath.Join(source, target), "episode 1")
	writeTestFile(t, episode2, "episode 2")
	if err := os.Symlink(target, episode1); err != nil {
		const privilegeNotHeld = syscall.Errno(1314) // Windows ERROR_PRIVILEGE_NOT_HELD.
		if runtime.GOOS == "windows" && errors.Is(err, privilegeNotHeld) {
			t.Skipf("symlink creation requires Developer Mode or symlink privilege: %v", err)
		}
		t.Fatal(err)
	}
	ctx := t.Context()
	_, wanted, err := filesystem.CollectVideoFiles(ctx, source, false)
	if err != nil {
		t.Fatal(err)
	}
	if !wantedMatchesFullDirectory(ctx, source, append(slices.Clone(wanted), filepath.Join(source, target))) {
		t.Fatal("expected a regular file symlink to count toward a full-directory match")
	}
	if wantedMatchesFullDirectory(ctx, source, wanted) {
		t.Fatal("expected unselected symlink target to prevent full-directory bypass")
	}
	subject := api.TorrentSubject{SourcePath: source, FileList: wanted}
	tmpRoot := t.TempDir()

	spec, err := stageWantedFilesWithOps(ctx, subject, tmpRoot, wanted, api.NopLogger{}, wantedStageOps{
		link: func(src, dst string) error {
			if src == episode1 {
				t.Fatal("staging tried to hardlink the relative symlink")
			}
			return os.Link(src, dst)
		},
		symlink: os.Symlink,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer spec.cleanup(ctx, api.NopLogger{})
	staged, err := os.Lstat(filepath.Join(spec.path, filepath.Base(episode2)))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(mustStat(t, episode2), staged) {
		t.Fatal("expected the regular episode to be staged as a hardlink")
	}

	result, err := NewService(api.NopLogger{}, tmpRoot).Create(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	name, files := loadTorrentShape(t, result.Path)
	if name != filepath.Base(source) {
		t.Fatalf("torrent root name = %q, want %q", name, filepath.Base(source))
	}
	assertStringSliceEqual(t, files, []string{filepath.Base(episode2), filepath.Base(episode1)})
	if err := verifyCandidateTorrentData(ctx, result.Path, subject); err != nil {
		t.Fatalf("verify generated torrent: %v", err)
	}
}

func TestCreateFolderPackSkipsStagingWhenWantedMatchesFullDirectory(t *testing.T) {
	t.Parallel()

	t.Run("comma-named full directory bypasses staging and ignores sidecars", func(t *testing.T) {
		t.Parallel()
		sourceDir := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
		episode1 := filepath.Join(sourceDir, "Show, Part 1 S01E01.mkv")
		episode2 := filepath.Join(sourceDir, "Show, Part 2 S01E02.mkv")
		writeTestFile(t, episode1, "episode 1")
		writeTestFile(t, episode2, "episode 2")
		// Ignored sidecars must not force staging when all non-ignored files are wanted.
		writeTestFile(t, filepath.Join(sourceDir, "ignored.torrent"), "ignored")

		tmpRoot := t.TempDir()
		subject := api.TorrentSubject{
			SourcePath: sourceDir,
			FileList:   []string{episode1, episode2},
		}
		spec, err := resolveCreateSpec(t.Context(), subject, tmpRoot, false, api.NopLogger{})
		if err != nil {
			t.Fatalf("resolveCreateSpec failed: %v", err)
		}
		defer spec.cleanup(t.Context(), api.NopLogger{})
		if spec.path != sourceDir || len(spec.includePatterns) != 0 || spec.cleanupLock != nil || !spec.fullDirBypass {
			t.Fatalf("expected direct full-directory spec without staging, got %+v", spec)
		}

		result, err := NewService(api.NopLogger{}, tmpRoot).Create(t.Context(), subject)
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		name, files := loadTorrentShape(t, result.Path)
		if name != filepath.Base(sourceDir) {
			t.Fatalf("torrent root name = %q, want %q", name, filepath.Base(sourceDir))
		}
		assertStringSliceEqual(t, files, []string{"Show, Part 1 S01E01.mkv", "Show, Part 2 S01E02.mkv"})
	})

	t.Run("ordinary full directory keeps explicit include patterns without directory walk", func(t *testing.T) {
		t.Parallel()
		sourceDir := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
		episode1 := filepath.Join(sourceDir, "Show.S01E01.mkv")
		episode2 := filepath.Join(sourceDir, "Show.S01E02.mkv")
		writeTestFile(t, episode1, "episode 1")
		writeTestFile(t, episode2, "episode 2")

		spec, err := resolveCreateSpec(t.Context(), api.TorrentSubject{
			SourcePath: sourceDir,
			FileList:   []string{episode1, episode2},
		}, t.TempDir(), false, api.NopLogger{})
		if err != nil {
			t.Fatalf("resolveCreateSpec failed: %v", err)
		}
		defer spec.cleanup(t.Context(), api.NopLogger{})
		if spec.fullDirBypass || len(spec.includePatterns) != 2 || spec.cleanupLock != nil {
			t.Fatalf("expected include-pattern spec for non-comma selection, got %+v", spec)
		}
	})

	t.Run("full-directory bypass falls back to staging when an extra file appears before hashing", func(t *testing.T) {
		t.Parallel()
		sourceDir := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
		episode1 := filepath.Join(sourceDir, "Show, Part 1 S01E01.mkv")
		episode2 := filepath.Join(sourceDir, "Show, Part 2 S01E02.mkv")
		writeTestFile(t, episode1, "episode 1")
		writeTestFile(t, episode2, "episode 2")

		var (
			addedExtra sync.Once
			mu         sync.Mutex
			statuses   []string
		)
		ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
			mu.Lock()
			statuses = append(statuses, update.Status)
			mu.Unlock()
			if strings.HasPrefix(update.Message, "Hashing torrent:") {
				addedExtra.Do(func() {
					writeTestFile(t, filepath.Join(sourceDir, "unexpected-extra.mkv"), "extra")
				})
			}
		})
		result, err := NewService(api.NopLogger{}, t.TempDir()).Create(ctx, api.TorrentSubject{
			SourcePath: sourceDir,
			FileList:   []string{episode1, episode2},
		})
		if err != nil {
			t.Fatalf("Create failed on TOCTOU retry: %v", err)
		}
		_, files := loadTorrentShape(t, result.Path)
		assertStringSliceEqual(t, files, []string{"Show, Part 1 S01E01.mkv", "Show, Part 2 S01E02.mkv"})
		mu.Lock()
		defer mu.Unlock()
		if slices.Contains(statuses, "failed") {
			t.Fatalf("bypass retry emitted premature failed status before succeeding: %v", statuses)
		}
		if len(statuses) == 0 || statuses[len(statuses)-1] != "completed" {
			t.Fatalf("expected final completed status after bypass retry, got %v", statuses)
		}
	})

	t.Run("wantedMatchesFullDirectory honors canceled context", func(t *testing.T) {
		t.Parallel()
		sourceDir := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
		episode1 := filepath.Join(sourceDir, "Show, Part 1 S01E01.mkv")
		writeTestFile(t, episode1, "episode 1")
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if wantedMatchesFullDirectory(ctx, sourceDir, []string{episode1}) {
			t.Fatalf("expected canceled context to disable full-directory bypass")
		}
	})
}

func TestStageWantedFilesFallbackOrderCancellationAndStaleCleanup(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Show.S01.1080p.WEB-DL-GRP")
	episode1 := filepath.Join(sourceDir, "Show, Ep 1.mkv")
	episode2 := filepath.Join(sourceDir, "Show, Ep 2.mkv")
	writeTestFile(t, episode1, "episode 1 bytes")
	writeTestFile(t, episode2, "episode 2 bytes")

	t.Run("prefers symlink over copy when link fails", func(t *testing.T) {
		t.Parallel()
		tmpRoot := t.TempDir()
		var symlinks int
		spec, err := stageWantedFilesWithOps(
			t.Context(),
			api.TorrentSubject{SourcePath: sourceDir},
			tmpRoot,
			[]string{episode1, episode2},
			api.NopLogger{},
			wantedStageOps{
				link: func(string, string) error { return errors.New("cross-device link") },
				symlink: func(oldname, newname string) error {
					symlinks++
					return os.WriteFile(newname, []byte("symlink:"+filepath.Base(oldname)), 0o600)
				},
			},
		)
		if err != nil {
			t.Fatalf("stageWantedFilesWithOps failed: %v", err)
		}
		defer spec.cleanup(t.Context(), api.NopLogger{})
		if symlinks != 2 {
			t.Fatalf("expected symlink fallback for both files, got symlinks=%d", symlinks)
		}
		for _, sourceFile := range []string{episode1, episode2} {
			stagedBytes, readErr := os.ReadFile(filepath.Join(spec.path, filepath.Base(sourceFile)))
			if readErr != nil {
				t.Fatalf("read staged file: %v", readErr)
			}
			want := "symlink:" + filepath.Base(sourceFile)
			if string(stagedBytes) != want {
				t.Fatalf("staged file bytes = %q, want %q (copy fallback should not have run)", string(stagedBytes), want)
			}
		}
	})

	t.Run("copy fallback handles read-only source, logs warning, honors cancellation, and cleans only marked stale dirs", func(t *testing.T) {
		t.Parallel()
		roSourceDir := filepath.Join(t.TempDir(), "Show.RO.1080p.WEB-DL-GRP")
		roEpisode1 := filepath.Join(roSourceDir, "Show, Ep 1.mkv")
		roEpisode2 := filepath.Join(roSourceDir, "Show, Ep 2.mkv")
		writeTestFile(t, roEpisode1, "episode 1 bytes")
		writeTestFile(t, roEpisode2, "episode 2 bytes")
		if err := os.Chmod(roEpisode1, 0o444); err != nil {
			t.Fatalf("chmod read-only source: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(roEpisode1, 0o600) })

		tmpRoot := t.TempDir()
		stageByCopy := func(ctx context.Context, logger api.Logger) (createSpec, error) {
			return stageWantedFilesWithOps(
				ctx,
				api.TorrentSubject{SourcePath: roSourceDir, Trackers: []string{"TL"}},
				tmpRoot,
				[]string{roEpisode1, roEpisode2},
				logger,
				wantedStageOps{
					link:    func(string, string) error { return errors.New("cross-device link") },
					symlink: func(string, string) error { return errors.New("symlink unsupported") },
				},
			)
		}
		logger := &recordingDebugLogger{}
		spec, err := stageByCopy(t.Context(), logger)
		if err != nil {
			t.Fatalf("copy fallback with read-only source failed: %v", err)
		}
		cleanupPath := filepath.Dir(spec.cleanupLock.Name())
		spec.cleanup(t.Context(), logger)
		if _, statErr := os.Stat(cleanupPath); !os.IsNotExist(statErr) {
			t.Fatalf("expected cleanup to remove staged read-only copy dir %s, stat err=%v", cleanupPath, statErr)
		}
		if !slices.ContainsFunc(logger.snapshot(), func(line string) bool {
			return strings.Contains(line, "staging wanted files via copy fallback")
		}) {
			t.Fatalf("expected copy fallback warning log, got %v", logger.snapshot())
		}

		releaseRoot, err := TempTorrentPath(tmpRoot, roSourceDir)
		if err != nil {
			t.Fatal(err)
		}
		releaseDir := filepath.Dir(releaseRoot)
		staleDir := filepath.Join(releaseDir, "wanted-files-stale")
		unmarkedDir := filepath.Join(releaseDir, "wanted-files-archive")
		activeDir := filepath.Join(releaseDir, "wanted-files-active")
		writeTestFile(t, filepath.Join(staleDir, "leftover.mkv"), "stale")
		writeTestFile(t, filepath.Join(staleDir, ".staging.lock"), "")
		writeTestFile(t, filepath.Join(unmarkedDir, "user.mkv"), "keep")
		if err := os.MkdirAll(activeDir, 0o700); err != nil {
			t.Fatal(err)
		}
		activeLock, err := filelock.Acquire(filepath.Join(activeDir, ".staging.lock"))
		if err != nil {
			t.Fatal(err)
		}
		defer activeLock.Close()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		var progressMessages []string
		ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
			progressMessages = append(progressMessages, update.Message)
			if update.Message == "Staging torrent files (2/2)" {
				cancel()
			}
		})

		canceledSpec, err := stageByCopy(ctx, api.NopLogger{})
		if !errors.Is(err, context.Canceled) {
			canceledSpec.cleanup(t.Context(), api.NopLogger{})
			t.Fatalf("expected context.Canceled on canceled copy fallback, got %v", err)
		}
		if _, statErr := os.Stat(staleDir); !os.IsNotExist(statErr) {
			t.Fatalf("expected marked stale staging dir to be removed, stat err = %v", statErr)
		}
		if _, statErr := os.Stat(unmarkedDir); statErr != nil {
			t.Fatalf("expected unmarked wanted-files-* dir to survive cleanup, stat err = %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(unmarkedDir, ".staging.lock")); !os.IsNotExist(statErr) {
			t.Fatalf("stale cleanup must not create .staging.lock inside unmarked dir, stat err = %v", statErr)
		}
		if _, statErr := os.Stat(activeDir); statErr != nil {
			t.Fatalf("expected locked active staging dir to survive cleanup, stat err = %v", statErr)
		}
		if !slices.Contains(progressMessages, "Staging torrent files (1/2)") || !slices.Contains(progressMessages, "Staging torrent files (2/2)") {
			t.Fatalf("expected per-file staging progress messages, got %v", progressMessages)
		}
		remaining, globErr := filepath.Glob(filepath.Join(releaseDir, "wanted-files-*"))
		slices.Sort(remaining)
		wantRemaining := []string{activeDir, unmarkedDir}
		slices.Sort(wantRemaining)
		if globErr != nil || !slices.Equal(remaining, wantRemaining) {
			t.Fatalf("expected canceled staging dir to be removed leaving activeDir and unmarkedDir, got %v (err=%v)", remaining, globErr)
		}
	})

	t.Run("preserves unmarked and marked wanted-files-* folders when source overlaps release root", func(t *testing.T) {
		t.Parallel()
		tmpRoot := t.TempDir()
		overlappingSource := filepath.Join(tmpRoot, "Release.Overlap.1080p-GRP")
		ep1 := filepath.Join(overlappingSource, "Show, Ep 1.mkv")
		ep2 := filepath.Join(overlappingSource, "Show, Ep 2.mkv")
		userArchive := filepath.Join(overlappingSource, "wanted-files-archive", "extras.mkv")
		markedSubtree := filepath.Join(overlappingSource, "wanted-files-marked", "bonus.mkv")
		writeTestFile(t, ep1, "ep1")
		writeTestFile(t, ep2, "ep2")
		writeTestFile(t, userArchive, "user extras")
		writeTestFile(t, markedSubtree, "marked bonus")
		writeTestFile(t, filepath.Join(filepath.Dir(markedSubtree), wantedStagingLockName), "")

		spec, err := stageWantedFiles(
			t.Context(),
			api.TorrentSubject{SourcePath: overlappingSource},
			tmpRoot,
			[]string{ep1, ep2},
			api.NopLogger{},
		)
		if err != nil {
			t.Fatalf("stageWantedFiles failed: %v", err)
		}
		defer spec.cleanup(t.Context(), api.NopLogger{})
		if _, statErr := os.Stat(userArchive); statErr != nil {
			t.Fatalf("expected pre-existing user folder inside overlapping source to survive, stat err = %v", statErr)
		}
		if _, statErr := os.Stat(filepath.Join(filepath.Dir(userArchive), wantedStagingLockName)); !os.IsNotExist(statErr) {
			t.Fatalf("must not create .staging.lock inside user folder, stat err = %v", statErr)
		}
		if _, statErr := os.Stat(markedSubtree); statErr != nil {
			t.Fatalf("expected marked wanted-files-* folder inside overlapping source to survive, stat err = %v", statErr)
		}
	})

	t.Run("collision with existing symlink fails without modifying symlink target", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		src := filepath.Join(dir, "second.mkv")
		target := filepath.Join(dir, "original.mkv")
		writeTestFile(t, src, "new-data")
		writeTestFile(t, target, "keep-original")

		dst := filepath.Join(dir, "collision.mkv")
		if err := os.Symlink(target, dst); err != nil {
			// Fall back to regular file collision when host disallows symlinks,
			// while still verifying O_EXCL and os.ErrExist fail-fast behavior.
			writeTestFile(t, dst, "keep-original")
		}
		if err := copyFileContext(t.Context(), src, dst); !errors.Is(err, os.ErrExist) {
			t.Fatalf("expected copyFileContext to fail with os.ErrExist on collision, got %v", err)
		}
		got, err := os.ReadFile(target)
		if err != nil || string(got) != "keep-original" {
			t.Fatalf("collision target was modified: data=%q err=%v", string(got), err)
		}

		// stageWantedFilesWithOps must fail immediately on os.ErrExist without calling symlink/copy.
		symlinkCalled := false
		_, err = stageWantedFilesWithOps(
			t.Context(),
			api.TorrentSubject{SourcePath: sourceDir},
			t.TempDir(),
			[]string{episode1},
			api.NopLogger{},
			wantedStageOps{
				link: func(string, string) error { return os.ErrExist },
				symlink: func(string, string) error {
					symlinkCalled = true
					return nil
				},
			},
		)
		if !errors.Is(err, os.ErrExist) {
			t.Fatalf("expected stageWantedFilesWithOps to fail on link os.ErrExist, got %v", err)
		}
		if symlinkCalled {
			t.Fatal("symlink fallback must not run after link os.ErrExist collision")
		}
	})

	t.Run("read-only source retains read-only mode after default staging and cleanup", func(t *testing.T) {
		t.Parallel()
		roDir := filepath.Join(t.TempDir(), "Show.RODefault.1080p-GRP")
		roFile := filepath.Join(roDir, "Show, Ep 1.mkv")
		writeTestFile(t, roFile, "read-only media")
		if err := os.Chmod(roFile, 0o444); err != nil {
			t.Fatalf("chmod read-only source: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(roFile, 0o600) })

		defaults := defaultWantedStageOps()
		linkCalls := 0
		spec, err := stageWantedFilesWithOps(
			t.Context(),
			api.TorrentSubject{SourcePath: roDir},
			t.TempDir(),
			[]string{roFile},
			api.NopLogger{},
			wantedStageOps{
				link: func(oldname, newname string) error {
					linkCalls++
					return defaults.link(oldname, newname)
				},
				symlink: defaults.symlink,
			},
		)
		if err != nil {
			t.Fatalf("staging read-only source failed: %v", err)
		}
		cleanupPath := filepath.Dir(spec.cleanupLock.Name())
		spec.cleanup(t.Context(), api.NopLogger{})
		if _, statErr := os.Stat(cleanupPath); !os.IsNotExist(statErr) {
			t.Fatalf("expected staging dir to be removed, stat err = %v", statErr)
		}
		info, statErr := os.Stat(roFile)
		if statErr != nil {
			t.Fatalf("stat read-only source: %v", statErr)
		}
		if info.Mode()&0o200 != 0 {
			t.Fatalf("read-only source became writable after staging cleanup: mode=%v", info.Mode())
		}
		if runtime.GOOS == "windows" && linkCalls != 0 {
			t.Fatalf("expected Windows staging to skip hardlink for read-only source, linkCalls=%d", linkCalls)
		}
	})

	t.Run("removeWantedStagingDir preserves lock marker when payload removal fails", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS != "windows" && os.Geteuid() == 0 {
			t.Skip("root can remove files inside read-only directories on Unix")
		}
		stageDir := filepath.Join(t.TempDir(), "wanted-files-partial")
		payloadDir := filepath.Join(stageDir, "Show")
		payloadFile := filepath.Join(payloadDir, "ep1.mkv")
		lockPath := filepath.Join(stageDir, wantedStagingLockName)
		writeTestFile(t, payloadFile, "locked payload")
		writeTestFile(t, lockPath, "")

		var unlock func()
		if runtime.GOOS == "windows" {
			handle, openErr := os.Open(payloadFile)
			if openErr != nil {
				t.Fatalf("open payload file: %v", openErr)
			}
			unlock = func() { _ = handle.Close() }
		} else {
			if chmodErr := os.Chmod(payloadDir, 0o500); chmodErr != nil {
				t.Fatalf("chmod payload dir: %v", chmodErr)
			}
			unlock = func() { _ = os.Chmod(payloadDir, 0o700) }
		}
		t.Cleanup(unlock)

		if err := removeWantedStagingDir(t.Context(), stageDir); err == nil {
			t.Fatal("expected removeWantedStagingDir to fail while payload cannot be removed")
		}
		if _, statErr := os.Stat(lockPath); statErr != nil {
			t.Fatalf("expected .staging.lock to remain after partial cleanup failure, stat err = %v", statErr)
		}

		unlock()
		if err := removeWantedStagingDir(t.Context(), stageDir); err != nil {
			t.Fatalf("expected second removeWantedStagingDir to succeed after unlock: %v", err)
		}
		if _, statErr := os.Stat(stageDir); !os.IsNotExist(statErr) {
			t.Fatalf("expected staging dir to be removed after retry, stat err = %v", statErr)
		}
	})

	t.Run("stale cleanup protects directory reached through symlinked source", func(t *testing.T) {
		t.Parallel()
		base := t.TempDir()
		releaseRoot := filepath.Join(base, "release-tmp")
		staleDir := filepath.Join(releaseRoot, "wanted-files-aliased")
		realMedia := filepath.Join(staleDir, "real-source", "ep1.mkv")
		writeTestFile(t, realMedia, "important media")
		writeTestFile(t, filepath.Join(staleDir, wantedStagingLockName), "")

		aliasSource := filepath.Join(base, "source-alias")
		if err := os.Symlink(filepath.Dir(realMedia), aliasSource); err != nil {
			if runtime.GOOS != "windows" {
				t.Skipf("symlink unavailable on this host: %v", err)
			}
			output, mkErr := exec.CommandContext(t.Context(), "cmd.exe", "/d", "/c", "mklink", "/J", aliasSource, filepath.Dir(realMedia)).CombinedOutput()
			if mkErr != nil {
				t.Skipf("junction unavailable on this host: %v: %s", mkErr, output)
			}
			t.Cleanup(func() { _ = os.Remove(aliasSource) })
		}
		cleanupStaleWantedStagingDirs(t.Context(), releaseRoot, aliasSource, []string{filepath.Join(aliasSource, "ep1.mkv")}, api.NopLogger{})
		if _, statErr := os.Stat(realMedia); statErr != nil {
			t.Fatalf("stale cleanup deleted media reachable through symlinked source: %v", statErr)
		}
	})
}

func TestStagingPreservesReadOnlyAttributeChangedAfterHardlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("Windows hardlink attributes")
	}
	for _, cleanup := range []string{"normal cleanup", "stale sweep"} {
		t.Run(cleanup, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "Show")
			wanted := filepath.Join(source, "Show, Ep 1.mkv")
			const content = "synthetic media"
			writeTestFile(t, wanted, content)
			spec, err := stageWantedFiles(t.Context(), api.TorrentSubject{SourcePath: source}, t.TempDir(), []string{wanted}, api.NopLogger{})
			if err != nil {
				t.Fatal(err)
			}
			defer spec.cleanup(t.Context(), api.NopLogger{})
			stageDir := filepath.Dir(spec.cleanupLock.Name())
			sourceInfo, err := os.Stat(wanted)
			if err != nil {
				t.Fatal(err)
			}
			stagedInfo, err := os.Lstat(filepath.Join(spec.path, filepath.Base(wanted)))
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(sourceInfo, stagedInfo) {
				t.Fatal("expected writable source to be staged as a hardlink")
			}
			if err := os.Chmod(wanted, 0o444); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Chmod(wanted, 0o600) }()

			if cleanup == "stale sweep" {
				if err := spec.cleanupLock.Close(); err != nil {
					t.Fatal(err)
				}
				spec.cleanupLock = nil
				cleanupStaleWantedStagingDirs(t.Context(), filepath.Dir(stageDir), source, []string{wanted}, api.NopLogger{})
			} else {
				spec.cleanup(t.Context(), api.NopLogger{})
			}
			info, err := os.Stat(wanted)
			if err != nil || info.Mode()&0o200 != 0 {
				t.Fatalf("source lost read-only attribute: info=%v err=%v", info, err)
			}
			if data, err := os.ReadFile(wanted); err != nil || string(data) != content {
				t.Fatalf("source changed: data=%q err=%v", data, err)
			}
			if _, err := os.Stat(stageDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("staging dir was not removed: %v", err)
			}
		})
	}
}

func TestWantedStagingCoordinationIsReleaseScopedAndCancelable(t *testing.T) {
	t.Parallel()
	tmpRoot := t.TempDir()
	sourceA := filepath.Join(t.TempDir(), "Release-A")
	sourceB := filepath.Join(t.TempDir(), "Release-B")
	fileA := filepath.Join(sourceA, "Show, Ep 1.mkv")
	fileB := filepath.Join(sourceB, "Show, Ep 1.mkv")
	writeTestFile(t, fileA, "release A")
	writeTestFile(t, fileB, "release B")
	stage := func(ctx context.Context, source, file string) (createSpec, error) {
		return stageWantedFiles(ctx, api.TorrentSubject{SourcePath: source}, tmpRoot, []string{file}, api.NopLogger{})
	}
	specA, err := stage(t.Context(), sourceA, fileA)
	if err != nil {
		t.Fatal(err)
	}
	defer specA.cleanup(t.Context(), api.NopLogger{})
	dirA := filepath.Dir(specA.cleanupLock.Name())
	lock, err := filelock.Acquire(filepath.Join(filepath.Dir(dirA), wantedStagingCoordLockName))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	ctxA, cancelA := context.WithCancel(t.Context())
	defer cancelA()
	waiting := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(waiting)
		spec, err := stage(ctxA, sourceA, fileA)
		spec.cleanup(ctxA, api.NopLogger{})
		done <- err
	}()
	<-waiting
	ctxB, cancelB := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelB()
	otherDone := make(chan error, 1)
	go func() {
		spec, err := stage(ctxB, sourceB, fileB)
		if err == nil {
			spec.cleanup(ctxB, api.NopLogger{})
			if _, statErr := os.Stat(spec.path); !errors.Is(statErr, os.ErrNotExist) {
				err = errors.Join(errors.New("release B staging dir still present"), statErr)
			}
		}
		otherDone <- err
	}()
	select {
	case err := <-otherDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctxB.Done():
		t.Fatal("release B staging or cleanup was blocked by release A's lock")
	}
	select {
	case err := <-done:
		t.Fatalf("release A did not wait for its own lock: %v", err)
	default:
	}
	cancelA()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("release A did not return cancellation: %v", err)
		}
	case <-ctxB.Done():
		t.Fatal("release A did not stop waiting while its lock was held")
	}

	specA.cleanup(ctxA, api.NopLogger{})
	if _, err := os.Stat(filepath.Join(dirA, wantedStagingLockName)); err != nil {
		t.Fatalf("canceled cleanup must leave a marker while coordination is held: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := stage(t.Context(), sourceA, fileA)
	if err != nil {
		t.Fatal(err)
	}
	defer again.cleanup(t.Context(), api.NopLogger{})
	if _, err := os.Stat(dirA); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale staging dir was not recovered after cancellation: %v", err)
	}
}

func TestCreateDiscImagePreservesSingleFileLayout(t *testing.T) {
	t.Parallel()
	for _, reuse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reuse=%t", reuse), func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "Example.Movie.2026.iso")
			writeTestFile(t, source, "disc image")
			if reuse {
				createTestTorrentFromExisting(t, source, source+".torrent")
			}
			service := NewService(api.NopLogger{}, t.TempDir())
			result, err := service.Create(t.Context(), api.TorrentSubject{SourcePath: source, DiscType: "BDMV"})
			if err != nil {
				t.Fatal(err)
			}
			name, files := loadTorrentShape(t, result.Path)
			if name != filepath.Base(source) || len(files) != 0 {
				t.Fatalf("disc image layout = %q %v", name, files)
			}
			if reuse && result.Path != source+".torrent" {
				t.Fatalf("disc image candidate was not reused: %s", result.Path)
			}
		})
	}
}

func TestCreateDiscFolderIgnoresWantedFileList(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Movie.2026.COMPLETE.BLURAY-GRP")
	stream := filepath.Join(sourceDir, "BDMV", "STREAM", "00001.m2ts")
	writeTestFile(t, stream, "stream")
	writeTestFile(t, filepath.Join(sourceDir, "BDMV", "PLAYLIST", "00001.mpls"), "playlist")
	writeTestFile(t, filepath.Join(sourceDir, "CERTIFICATE", "id.bdmv"), "certificate")

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: sourceDir,
		DiscType:   "BDMV",
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

func TestCreateMultiDiscCollectionIncludesEveryTree(t *testing.T) {
	t.Parallel()

	sourceDir := filepath.Join(t.TempDir(), "Example.Release.2026.COMPLETE.BLURAY-GRP")
	firstStream := filepath.Join(sourceDir, "Disc 1", "BDMV", "STREAM", "00001.m2ts")
	secondStream := filepath.Join(sourceDir, "Disc 2", "BDMV", "STREAM", "00001.m2ts")
	writeTestFile(t, firstStream, "first stream")
	writeTestFile(t, filepath.Join(sourceDir, "Disc 1", "BDMV", "PLAYLIST", "00001.mpls"), "first playlist")
	writeTestFile(t, secondStream, "second stream")
	writeTestFile(t, filepath.Join(sourceDir, "Disc 2", "BDMV", "PLAYLIST", "00001.mpls"), "second playlist")

	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: sourceDir,
		DiscType:   "BDMV",
		FileList:   []string{firstStream},
	})
	if err != nil {
		t.Fatalf("create collection torrent: %v", err)
	}

	name, files := loadTorrentShape(t, result.Path)
	if name != filepath.Base(sourceDir) {
		t.Fatalf("torrent name = %q, want %q", name, filepath.Base(sourceDir))
	}
	assertStringSliceEqual(t, files, []string{
		"Disc 1/BDMV/PLAYLIST/00001.mpls",
		"Disc 1/BDMV/STREAM/00001.m2ts",
		"Disc 2/BDMV/PLAYLIST/00001.mpls",
		"Disc 2/BDMV/STREAM/00001.m2ts",
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
			result, err := service.Create(context.Background(), api.TorrentSubject{
				SourcePath: sourceDir,
				DiscType:   tt.discType,
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
	result, err := service.Create(context.Background(), api.TorrentSubject{
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
	_, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: source,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected nohash to fail without reusable torrent, got %v", err)
	}
}

func TestCreateNoHashReportsInvalidDiscInventory(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "film.mkv")
	writeTestFile(t, source, "synthetic media")
	candidate := filepath.Join(t.TempDir(), "existing.torrent")
	createTestTorrentFromExisting(t, source, candidate)
	hash, err := loadInfoHash(candidate)
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewService(nil, t.TempDir()).Create(t.Context(), api.TorrentSubject{
		SourcePath:            t.TempDir(),
		DiscType:              "BDMV",
		Trackers:              []string{"TL"},
		ClientTorrentPath:     candidate,
		ClientTorrentInfoHash: hash,
		TorrentOverrides:      api.TorrentOverrides{NoHash: new(true)},
	})
	if err == nil || !strings.Contains(err.Error(), "submitted content inventory is empty") {
		t.Fatalf("expected the unavailable disc inventory to be reported, got %v", err)
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
	result, err := service.Create(context.Background(), api.TorrentSubject{
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
	_, err := service.Create(context.Background(), api.TorrentSubject{
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
	_, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath:                sourceDir,
		FileList:                  []string{episode1, episode2},
		ClientTorrentPath:         clientTorrentPath,
		ClientTorrentDataVerified: true,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected nohash to reject case-only multi-file mismatch, got %v", err)
	}
}

func TestCreateNoHashReusesCompatibleClientDataWithoutVerification(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		verified    bool
		missingHash bool
	}{
		{name: "verified", verified: true},
		{name: "unverified"},
		{
			name:        "missing infohash",
			verified:    true,
			missingHash: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			source := filepath.Join(dir, "video.mkv")
			writeTestFile(t, source, "source-data")

			clientSource := filepath.Join(dir, "client", "video.mkv")
			writeTestFile(t, clientSource, "source-evil")
			clientTorrentPath := filepath.Join(dir, "client.torrent")
			createTestTorrentFromExisting(t, clientSource, clientTorrentPath)
			clientInfoHash, err := loadInfoHash(clientTorrentPath)
			if err != nil {
				t.Fatalf("load client torrent infohash: %v", err)
			}
			if test.missingHash {
				clientInfoHash = ""
			}

			service := NewService(api.NopLogger{}, t.TempDir())
			reuseOnly := true
			result, err := service.Create(context.Background(), api.TorrentSubject{
				SourcePath:                source,
				ClientTorrentPath:         clientTorrentPath,
				ClientTorrentInfoHash:     clientInfoHash,
				ClientTorrentDataVerified: test.verified,
				TorrentOverrides: api.TorrentOverrides{
					NoHash: &reuseOnly,
				},
			})
			if err != nil || result.Path != clientTorrentPath {
				t.Fatalf("expected compatible client torrent to be reused, result=%+v err=%v", result, err)
			}
			if clientInfoHash != "" && result.InfoHash != clientInfoHash {
				t.Fatalf("reused torrent infohash = %q, want %q", result.InfoHash, clientInfoHash)
			}
		})
	}
}

func TestCreateNoHashRejectsVerifiedClientTorrentOverwrittenAfterDiscovery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	writeTestFile(t, source, "source-data")

	clientTorrentPath := filepath.Join(dir, "client.torrent")
	createTestTorrentFromExisting(t, source, clientTorrentPath)
	verifiedInfoHash, err := loadInfoHash(clientTorrentPath)
	if err != nil {
		t.Fatalf("load verified client torrent infohash: %v", err)
	}

	replacementSource := filepath.Join(dir, "replacement", "video.mkv")
	writeTestFile(t, replacementSource, "source-evil")
	createTestTorrentFromExisting(t, replacementSource, clientTorrentPath)

	service := NewService(api.NopLogger{}, t.TempDir())
	reuseOnly := true
	_, err = service.Create(context.Background(), api.TorrentSubject{
		SourcePath:                source,
		ClientTorrentPath:         clientTorrentPath,
		ClientTorrentInfoHash:     verifiedInfoHash,
		ClientTorrentDataVerified: true,
		TorrentOverrides: api.TorrentOverrides{
			NoHash: &reuseOnly,
		},
	})
	if !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("expected overwritten verified torrent to require byte verification, got %v", err)
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
	meta := api.TorrentSubject{SourcePath: source}

	tmpTorrentPath, err := TempTorrentPath(tmpRoot, source)
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
	if filepath.Dir(result.Path) != filepath.Join(filepath.Dir(tmpTorrentPath), "base-torrents") {
		t.Fatalf("expected immutable base beneath release owner, got %s", result.Path)
	}
	if got := mustStat(t, result.Path).ModTime(); !got.After(past) {
		t.Fatalf("expected rehash to recreate torrent, modtime %v was not after %v", got, past)
	}
}

func TestCreateRehashOverridesNoHashReusableTorrents(t *testing.T) {
	t.Parallel()

	cases := map[string]func(t *testing.T, source string, tmpTorrentPath string) (api.TorrentSubject, string){
		"client": func(t *testing.T, source string, _ string) (api.TorrentSubject, string) {
			t.Helper()

			clientTorrentPath := filepath.Join(filepath.Dir(source), "client.torrent")
			createTestTorrentFromExisting(t, source, clientTorrentPath)
			return api.TorrentSubject{SourcePath: source, ClientTorrentPath: clientTorrentPath}, clientTorrentPath
		},
		"adjacent": func(t *testing.T, source string, _ string) (api.TorrentSubject, string) {
			t.Helper()

			adjacentTorrentPath := source + ".torrent"
			createTestTorrentFromExisting(t, source, adjacentTorrentPath)
			return api.TorrentSubject{SourcePath: source}, adjacentTorrentPath
		},
		"temp": func(t *testing.T, source string, tmpTorrentPath string) (api.TorrentSubject, string) {
			t.Helper()

			createTestTorrentFromExisting(t, source, tmpTorrentPath)
			past := mustStat(t, tmpTorrentPath).ModTime().Add(-2 * time.Hour)
			if err := os.Chtimes(tmpTorrentPath, past, past); err != nil {
				t.Fatalf("chtimes: %v", err)
			}
			return api.TorrentSubject{SourcePath: source}, tmpTorrentPath
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
			tmpTorrentPath, err := TempTorrentPath(tmpRoot, source)
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
			if filepath.Dir(result.Path) != filepath.Join(filepath.Dir(tmpTorrentPath), "base-torrents") {
				t.Fatalf("expected immutable generated base beneath release owner, got %s", result.Path)
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

func TestApplyTorrentOverridePieceOptionsCapsRequiredPieceSizeToUserMax(t *testing.T) {
	t.Parallel()

	maxPiece := 16
	requiredExp := uint(26)

	options := applyTorrentOverridePieceOptions(api.TorrentSubject{
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
	if options.pieceExp == nil || *options.pieceExp != 24 {
		t.Fatalf("expected required piece exponent to be capped at 24, got %#v", options.pieceExp)
	}
}

func TestApplyTorrentOverridePieceOptionsCapsToTrackerMax(t *testing.T) {
	t.Parallel()

	maxPiece := 128

	options := applyTorrentOverridePieceOptions(api.TorrentSubject{
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

	meta := api.TorrentSubject{SourcePath: source}
	tmpTorrentPath, err := TempTorrentPath(tmpRoot, source)
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
	meta := api.TorrentSubject{SourcePath: source}

	tmpTorrentPath, err := TempTorrentPath(tmpRoot, source)
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
	meta := api.TorrentSubject{SourcePath: source}

	tmpTorrentPath, err := TempTorrentPath(tmpRoot, source)
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
	result, err := service.Create(context.Background(), api.TorrentSubject{
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

func TestCreateReusesSameLayoutAdjacentTorrentWithoutContentCheck(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "video.mkv")
	writeTestFile(t, source, "source-data")

	clientDir := filepath.Join(sourceDir, "client")
	clientSource := filepath.Join(clientDir, "video.mkv")
	writeTestFile(t, clientSource, "source-evil")
	adjacentTorrentPath := source + ".torrent"
	createTestTorrentFromExisting(t, clientSource, adjacentTorrentPath)

	tmpRoot := t.TempDir()
	service := NewService(api.NopLogger{}, tmpRoot)
	result, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: source,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != adjacentTorrentPath {
		t.Fatalf("expected compatible adjacent torrent to be reused at %s, got %s", adjacentTorrentPath, result.Path)
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
	result, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath:        sourceDir,
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
	_, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath: sourceDir,
		FileList:   []string{outside},
	})
	if err == nil {
		t.Fatalf("expected outside wanted file to be rejected")
	}
}

func TestCreateReusesPTPYellowPieceSizeForPTPAndHDB(t *testing.T) {
	t.Parallel()

	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "movie.mkv")
	content := make([]byte, 70<<20)
	if err := os.WriteFile(source, content, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	clientTorrentPath := filepath.Join(sourceDir, "client.torrent")
	pieceExp := uint(18)
	_, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:           source,
		OutputPath:     clientTorrentPath,
		IsPrivate:      true,
		PieceLengthExp: &pieceExp,
	})
	if err != nil {
		t.Fatalf("create client torrent: %v", err)
	}

	registry := trackers.NewRegistry()
	for _, definition := range []trackers.Definition{hdb.New(), ptp.New()} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register tracker: %v", err)
		}
	}
	service := NewServiceWithRegistry(api.NopLogger{}, t.TempDir(), registry)
	result, err := service.Create(context.Background(), api.TorrentSubject{
		SourcePath:        source,
		SourceSize:        int64(len(content)),
		Trackers:          []string{"PTP", "HDB"},
		ClientTorrentPath: clientTorrentPath,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.Path != clientTorrentPath {
		t.Fatalf("expected accepted PTP client torrent to be reused")
	}
	if len(result.RehashedTrackers) != 0 || len(result.SkippedTrackers) != 0 {
		t.Fatalf("unexpected tracker exclusions: rehashed=%v skipped=%v", result.RehashedTrackers, result.SkippedTrackers)
	}
}

func TestResolveTrackerPieceProfiles(t *testing.T) {
	t.Parallel()

	registry := trackers.NewRegistry()
	for _, definition := range []trackers.Definition{hdb.New(), ptp.New()} {
		if err := registry.Register(definition); err != nil {
			t.Fatalf("register tracker: %v", err)
		}
	}
	tests := []struct {
		name        string
		trackers    []string
		size        int64
		maxExp      uint
		pieceExp    uint
		profileHost string
	}{
		{
			name:        "PTP",
			trackers:    []string{"PTP"},
			size:        70 << 20,
			maxExp:      24,
			pieceExp:    17,
			profileHost: "passthepopcorn.me",
		},
		{
			name:        "HDB",
			trackers:    []string{"HDB"},
			size:        40 << 30,
			maxExp:      25,
			pieceExp:    24,
			profileHost: "hdbits.org",
		},
		{
			name:        "HDB and PTP",
			trackers:    []string{"HDB", "PTP"},
			size:        10 << 30,
			maxExp:      24,
			pieceExp:    23,
			profileHost: "passthepopcorn.me",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := resolveTrackerPolicy(api.TorrentSubject{Trackers: test.trackers, SourceSize: test.size}, registry)
			pieceExp, ok := policy.requiredPieceExp(api.TorrentSubject{SourceSize: test.size})
			if !ok || policy.maxPieceExp != test.maxExp || pieceExp != test.pieceExp || !strings.Contains(policy.pieceSizeProfileURL, test.profileHost) {
				t.Fatalf("policy=%#v piece_exp=%d resolved=%t", policy, pieceExp, ok)
			}
		})
	}
}

func TestHDBPieceRecommendationBoundaries(t *testing.T) {
	t.Parallel()

	registry := trackers.NewRegistry()
	if err := registry.Register(hdb.New()); err != nil {
		t.Fatalf("register HDB: %v", err)
	}
	cases := []struct {
		size int64
		exp  uint
	}{
		{size: 8 << 30, exp: 22},
		{size: 8<<30 + 1, exp: 24},
		{size: 1<<40 + 1, exp: 24},
	}
	for _, test := range cases {
		meta := api.TorrentSubject{Trackers: []string{"HDB"}, SourceSize: test.size}
		policy := resolveTrackerPolicy(meta, registry)
		options := policy.createOptions(meta)
		if options.maxPieceExp != 24 || options.pieceExp == nil || *options.pieceExp != test.exp {
			t.Fatalf("size %d: create options = %#v; want max 24 and piece exponent %d", test.size, options, test.exp)
		}
	}
}

func TestHDBAcceptedPieceLayouts(t *testing.T) {
	t.Parallel()

	policy := &trackerTorrentPolicy{
		name:        "HDB",
		maxPieceExp: 25,
		hdb:         true,
	}
	tests := []struct {
		name        string
		pieceLength int64
		pieces      int
		totalLength int64
		wantErr     bool
	}{
		{
			name:        "2 MiB at piece limit",
			pieceLength: 2 << 20,
			pieces:      4000,
		},
		{
			name:        "2 MiB over piece limit",
			pieceLength: 2 << 20,
			pieces:      4001,
			wantErr:     true,
		},
		{
			name:        "4 MiB at piece limit",
			pieceLength: 4 << 20,
			pieces:      30000,
		},
		{
			name:        "4 MiB over piece limit",
			pieceLength: 4 << 20,
			pieces:      30001,
			wantErr:     true,
		},
		{
			name:        "8 MiB at piece limit",
			pieceLength: 8 << 20,
			pieces:      30000,
		},
		{
			name:        "8 MiB over piece limit",
			pieceLength: 8 << 20,
			pieces:      30001,
			wantErr:     true,
		},
		{
			name:        "16 MiB has no stated piece limit",
			pieceLength: 16 << 20,
			pieces:      30001,
		},
		{
			name:        "32 MiB at 1 TiB",
			pieceLength: 32 << 20,
			pieces:      1,
			totalLength: 1 << 40,
			wantErr:     true,
		},
		{
			name:        "32 MiB over 1 TiB",
			pieceLength: 32 << 20,
			pieces:      1,
			totalLength: 1<<40 + 1,
		},
		{
			name:        "64 MiB unsupported",
			pieceLength: 64 << 20,
			pieces:      1,
			totalLength: 1<<40 + 1,
			wantErr:     true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			totalLength := test.totalLength
			if totalLength == 0 {
				totalLength = test.pieceLength * int64(test.pieces)
			}
			info := metainfo.Info{
				PieceLength: test.pieceLength,
				Pieces:      make([]byte, test.pieces*metainfo.HashSize),
				Length:      totalLength,
			}
			err := policy.validateTorrentInfo(&info, api.TorrentSubject{})
			if (err != nil) != test.wantErr {
				t.Fatalf("validateTorrentInfo() error = %v, wantErr %t", err, test.wantErr)
			}
		})
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
		meta := api.TorrentSubject{Trackers: []string{"PTP"}, SourceSize: int64(tc.size)}
		policy := resolveTrackerPolicy(meta, nil)
		got, ok := policy.requiredPieceExp(meta)
		if !ok {
			t.Fatalf("expected piece exponent for size %d", tc.size)
		}
		if got != tc.exp {
			t.Fatalf("size %d: expected exp %d, got %d", tc.size, tc.exp, got)
		}
	}
}

func TestPTPAcceptedPieceSizeRanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		size   int64
		minExp uint
		maxExp uint
	}{
		{
			size:   10 << 20,
			minExp: 15,
			maxExp: 18,
		},
		{
			size:   248 << 20,
			minExp: 16,
			maxExp: 19,
		},
		{
			size:   494 << 20,
			minExp: 17,
			maxExp: 20,
		},
		{
			size:   17080 << 20,
			minExp: 22,
			maxExp: 24,
		},
		{
			size:   129 << 30,
			minExp: 23,
			maxExp: 24,
		},
		{
			size:   257 << 30,
			minExp: 24,
			maxExp: 24,
		},
	}
	for _, test := range tests {
		minExp, maxExp := ptpPieceExpRange(test.size)
		if minExp != test.minExp || maxExp != test.maxExp {
			t.Fatalf("size %d: range %d-%d, want %d-%d", test.size, minExp, maxExp, test.minExp, test.maxExp)
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
	if info.Size() <= antMaxTorrentBytesForTest {
		t.Fatalf("expected oversized ANT torrent fixture, got %d bytes", info.Size())
	}

	registry := trackers.NewRegistry()
	if err := registry.Register(ant.New()); err != nil {
		t.Fatalf("register ANT: %v", err)
	}
	service := NewServiceWithRegistry(api.NopLogger{}, t.TempDir(), registry)
	result, err := service.Create(context.Background(), api.TorrentSubject{
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
	if regenerated.Size() > antMaxTorrentBytesForTest {
		t.Fatalf("expected regenerated torrent <= %d bytes, got %d", antMaxTorrentBytesForTest, regenerated.Size())
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

func TestCreateISOFullDiscKeepsOnlyOriginalPayload(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Example.3D.iso")
	payload := []byte("synthetic original image payload")
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	// Neither virtual stream evidence nor nearby generated artifacts are payloads.
	artifact := filepath.Join(filepath.Dir(source), "summary.txt")
	if err := os.WriteFile(artifact, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	subject := api.TorrentSubject{
		SourcePath: source,
		DiscType:   "BDMV",
		FileList:   []string{filepath.Join(filepath.Dir(source), "00001.M2TS"), artifact},
	}
	inventory, ok, err := ResolveSubmissionContentInventory(subject)
	if err != nil || !ok {
		t.Fatalf("inventory=%+v ok=%v err=%v", inventory, ok, err)
	}
	if inventory.Scope != api.SubmissionContentScopeFullDisc || len(inventory.Files) != 1 || inventory.Files[0].LocalPath != source || inventory.Files[0].Size != int64(len(payload)) {
		t.Fatalf("physical ISO inventory=%+v", inventory)
	}
	service := NewService(api.NopLogger{}, t.TempDir())
	result, err := service.Create(t.Context(), subject)
	if err != nil {
		t.Fatal(err)
	}
	torrentMeta, err := metainfo.LoadFromFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := torrentMeta.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Files) != 0 || info.Name != filepath.Base(source) || info.Length != int64(len(payload)) {
		t.Fatalf("ISO torrent is not the original single payload: %+v", info)
	}
	identity, err := SubmissionContentIdentity(inventory, verifySubmissionSource(t, source))
	if err != nil || len(identity.Files) != 1 {
		t.Fatalf("ISO content identity=%+v err=%v", identity, err)
	}
	reused, err := service.Create(t.Context(), subject)
	if err != nil || reused.InfoHash != result.InfoHash {
		t.Fatalf("ISO reuse changed content: %+v err=%v", reused, err)
	}
}

func TestReusableTorrentCandidatesUseLiteralRetainedDirectory(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"[", "[Group]"} {
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "synthetic.mkv")
			root := filepath.Join(t.TempDir(), name)
			meta := api.TorrentSubject{SourcePath: source}
			base, err := TempTorrentPath(root, source)
			if err != nil {
				t.Fatal(err)
			}
			retained := filepath.Join(filepath.Dir(base), "base-torrents")
			want := []string{base, source + ".torrent"}
			if got, err := reusableTorrentCandidates(root, meta); err != nil || !slices.Equal(got, want) {
				t.Fatalf("absent retained directory: got %v, err %v, want %v", got, err, want)
			}
			if err := os.Mkdir(retained, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, filename := range []string{"b.torrent", "a.torrent", "other.TORRENT", "note.txt"} {
				if err := os.WriteFile(filepath.Join(retained, filename), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join(retained, "directory.torrent"), 0o700); err != nil {
				t.Fatal(err)
			}
			want = []string{base, filepath.Join(retained, "a.torrent"), filepath.Join(retained, "b.torrent"), source + ".torrent"}
			if got, err := reusableTorrentCandidates(root, meta); err != nil || !slices.Equal(got, want) {
				t.Fatalf("retained candidate filtering/order: got %v, err %v, want %v", got, err, want)
			}
			if err := os.RemoveAll(retained); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(retained, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := reusableTorrentCandidates(root, meta); err == nil {
				t.Fatal("non-directory retained path error suppressed")
			}
		})
	}
}

// retainedDirectoryReadFS forces an entry to appear after the directory read
// has observed absence, before the caller's stat. No scheduler timing is needed.
type retainedDirectoryReadFS struct {
	fstest.MapFS
	appear  *fstest.MapFile
	readErr error
}

func (f retainedDirectoryReadFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := f.MapFS.ReadDir(name)
	if f.appear != nil {
		f.MapFS[name] = f.appear
	}
	if f.readErr != nil {
		err = f.readErr
	}
	return entries, err
}

func TestReadRetainedTorrentEntries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		appear  *fstest.MapFile
		readErr error
		wantErr error
	}{
		{name: "directory appears after missing read", appear: &fstest.MapFile{Mode: fs.ModeDir | 0o700}},
		{
			name:    "file appears after missing read",
			appear:  &fstest.MapFile{Mode: 0o600},
			wantErr: fs.ErrNotExist,
		},
		{
			name:    "permission error",
			readErr: fs.ErrPermission,
			wantErr: fs.ErrPermission,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := retainedDirectoryReadFS{
				MapFS:   fstest.MapFS{},
				appear:  tc.appear,
				readErr: tc.readErr,
			}
			entries, err := readRetainedTorrentEntries(root)
			if len(entries) != 0 || !errors.Is(err, tc.wantErr) {
				t.Fatalf("retained entries=%v error=%v, want error %v", entries, err, tc.wantErr)
			}
		})
	}
}
