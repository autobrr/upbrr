// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrentclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/pkg/api"
)

func writeQbitTestTorrentForSource(t *testing.T, torrentPath string, sourcePath string) {
	t.Helper()
	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatalf("stat torrent source fixture: %v", err)
	}
	if !info.IsDir() {
		writeQbitTestTorrent(t, torrentPath, filepath.Base(sourcePath), map[string]string{
			filepath.Base(sourcePath): sourcePath,
		}, false)
		return
	}
	files := make(map[string]string)
	if err := filepath.WalkDir(sourcePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return fmt.Errorf("torrent source fixture relative path: %w", err)
		}
		files[rel] = path
		return nil
	}); err != nil {
		t.Fatalf("walk torrent source fixture: %v", err)
	}
	writeQbitTestTorrent(t, torrentPath, filepath.Base(sourcePath), files, true)
}

func writeQbitTestTorrent(t *testing.T, torrentPath string, rootName string, files map[string]string, multi bool) {
	t.Helper()
	info := metainfo.Info{
		Name:        rootName,
		PieceLength: 16 * 1024,
		Private:     new(true),
	}
	if multi {
		for rel, source := range files {
			fileInfo, err := os.Stat(source)
			if err != nil {
				t.Fatalf("stat torrent source fixture: %v", err)
			}
			info.Files = append(info.Files, metainfo.FileInfo{
				Length: fileInfo.Size(),
				Path:   strings.Split(filepath.ToSlash(rel), "/"),
			})
		}
	} else {
		for _, source := range files {
			fileInfo, err := os.Stat(source)
			if err != nil {
				t.Fatalf("stat torrent source fixture: %v", err)
			}
			info.Length = fileInfo.Size()
			break
		}
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal torrent fixture info: %v", err)
	}
	file, err := os.OpenFile(torrentPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("create torrent fixture: %v", err)
	}
	if err := (&metainfo.MetaInfo{InfoBytes: infoBytes}).Write(file); err != nil {
		_ = file.Close()
		t.Fatalf("write torrent fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close torrent fixture: %v", err)
	}
}

func TestBuildTorrentLinkPlanUsesInjectedSingleFileName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "Original.Name.2026.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(root, "renamed.torrent")
	writeQbitTestTorrent(t, torrentPath, "Tracker.Name.2026.mkv", map[string]string{"source": source}, false)

	plan, err := buildTorrentLinkPlan(context.Background(), torrentPath, api.ClientSubject{SourcePath: source, FileList: []string{source}})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.root != "Tracker.Name.2026.mkv" || len(plan.files) != 1 || plan.files[0].destRel != "Tracker.Name.2026.mkv" {
		t.Fatalf("unexpected single-file torrent plan")
	}
}

func TestBuildTorrentLinkPlanUsesInjectedMultiFileLayout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "Original.Release.2026")
	source := filepath.Join(sourceRoot, "Original.Name.2026.mkv")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(root, "renamed-pack.torrent")
	writeQbitTestTorrent(t, torrentPath, "Tracker.Release.2026", map[string]string{
		filepath.Join("Feature", "Tracker.Name.2026.mkv"): source,
	}, true)

	plan, err := buildTorrentLinkPlan(context.Background(), torrentPath, api.ClientSubject{SourcePath: sourceRoot, FileList: []string{source}})
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	wantDest := filepath.Join("Tracker.Release.2026", "Feature", "Tracker.Name.2026.mkv")
	if len(plan.files) != 1 || plan.files[0].destRel != wantDest {
		t.Fatalf("unexpected multi-file torrent plan")
	}
	trackerDir := filepath.Join(root, "links", "TRACKER")
	if err := os.MkdirAll(trackerDir, 0o700); err != nil {
		t.Fatalf("mkdir tracker staging: %v", err)
	}
	if err := createTorrentLinkPlan(context.Background(), trackerDir, plan, "hardlink"); err != nil {
		t.Fatalf("create torrent link plan: %v", err)
	}
	stagedInfo, err := os.Stat(filepath.Join(trackerDir, wantDest))
	if err != nil {
		t.Fatalf("stat staged torrent file: %v", err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatalf("stat source: %v", err)
	}
	if !os.SameFile(sourceInfo, stagedInfo) {
		t.Fatalf("expected metainfo-shaped destination to hardlink source")
	}
}

func TestCreateTorrentLinkPlanRollsBackCreatedLinksUnderExistingRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	trackerDir := filepath.Join(root, "links", "EXAMPLE")
	planRoot := filepath.Join(trackerDir, "Example.Release.2026")
	if err := os.MkdirAll(planRoot, 0o700); err != nil {
		t.Fatalf("mkdir existing plan root: %v", err)
	}
	firstSource := filepath.Join(root, "first.mkv")
	secondSource := filepath.Join(root, "second.mkv")
	for _, source := range []string{firstSource, secondSource} {
		if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}
	}
	staleDest := filepath.Join(planRoot, "second.mkv")
	if err := os.WriteFile(staleDest, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write stale destination: %v", err)
	}

	plan := torrentLinkPlan{
		root: "Example.Release.2026",
		files: []torrentLinkFile{
			{
				sourcePath: firstSource,
				destRel:    filepath.Join("Example.Release.2026", "first.mkv"),
				length:     5,
			},
			{
				sourcePath: secondSource,
				destRel:    filepath.Join("Example.Release.2026", "second.mkv"),
				length:     5,
			},
		},
		torrentIsMulti: true,
	}
	if err := createTorrentLinkPlan(context.Background(), trackerDir, plan, "hardlink"); err == nil {
		t.Fatal("expected stale destination to fail link plan")
	}
	if _, err := os.Stat(filepath.Join(planRoot, "first.mkv")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected failed attempt to remove its created link, stat err=%v", err)
	}
	stale, err := os.ReadFile(staleDest)
	if err != nil {
		t.Fatalf("read stale destination: %v", err)
	}
	if string(stale) != "stale" {
		t.Fatal("expected rollback to preserve pre-existing destination")
	}
}

func TestBuildTorrentLinkPlanRejectsAmbiguousSizeOnlyMatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourceRoot := filepath.Join(root, "Original.Release.2026")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	first := filepath.Join(sourceRoot, "First.mkv")
	second := filepath.Join(sourceRoot, "Second.mkv")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("same-size"), 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}
	}
	torrentPath := filepath.Join(root, "ambiguous.torrent")
	writeQbitTestTorrent(t, torrentPath, "Renamed.mkv", map[string]string{"source": first}, false)

	_, err := buildTorrentLinkPlan(context.Background(), torrentPath, api.ClientSubject{SourcePath: sourceRoot})
	if err == nil || !strings.Contains(err.Error(), "no unique source match") {
		t.Fatalf("expected ambiguous source mapping error")
	}
}

func TestMatchSourceLinkCandidateUsesHostCaseSemantics(t *testing.T) {
	t.Parallel()

	t.Run("exact case", func(t *testing.T) {
		candidates := []sourceLinkCandidate{{
			path: "exact",
			rel:  filepath.Join("Feature", "Example.Release.2026.mkv"),
			name: "Example.Release.2026.mkv",
			size: 5,
		}}
		candidate, match, err := matchSourceLinkCandidateWithCaseFold(
			context.Background(),
			candidates,
			filepath.Join("Feature", "Example.Release.2026.mkv"),
			5,
			false,
		)
		if err != nil {
			t.Fatalf("match exact-case candidate: %v", err)
		}
		if candidate.path != "exact" || match != "path_size" {
			t.Fatal("expected exact-case path match")
		}
	})

	t.Run("case-only mismatch", func(t *testing.T) {
		candidates := []sourceLinkCandidate{{
			path: "case-only",
			rel:  filepath.Join("Feature", "Example.Release.2026.mkv"),
			name: "Example.Release.2026.mkv",
			size: 5,
		}}
		candidate, match, err := matchSourceLinkCandidateWithCaseFold(
			context.Background(),
			candidates,
			filepath.Join("feature", "example.release.2026.mkv"),
			5,
			false,
		)
		if err == nil {
			t.Fatal("expected case-only mismatch rejection on case-sensitive host")
		}
		if candidate != nil || match != "" {
			t.Fatal("expected rejected case-only candidate without a match")
		}
	})

	t.Run("Windows case folding", func(t *testing.T) {
		candidates := []sourceLinkCandidate{{
			path: "case-only",
			rel:  filepath.Join("Feature", "Example.Release.2026.mkv"),
			name: "Example.Release.2026.mkv",
			size: 5,
		}}
		candidate, match, err := matchSourceLinkCandidateWithCaseFold(
			context.Background(),
			candidates,
			filepath.Join("feature", "example.release.2026.mkv"),
			5,
			true,
		)
		if err != nil {
			t.Fatalf("match Windows case-folded candidate: %v", err)
		}
		if candidate.path != "case-only" || match != "path_size" {
			t.Fatal("expected Windows case-folded path match")
		}
	})

	t.Run("true rename keeps unique-size fallback", func(t *testing.T) {
		candidates := []sourceLinkCandidate{{
			path: "renamed",
			rel:  "Original.Name.2026.mkv",
			name: "Original.Name.2026.mkv",
			size: 5,
		}}
		candidate, match, err := matchSourceLinkCandidateWithCaseFold(context.Background(), candidates, "Tracker.Name.2026.mkv", 5, false)
		if err != nil {
			t.Fatalf("match renamed candidate: %v", err)
		}
		if candidate.path != "renamed" || match != "unique_size" {
			t.Fatal("expected renamed unique-size match")
		}
	})
}

func TestPrepareLinkStagingReturnsPlannerCancellation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	service := NewService(config.Config{}, nil)
	_, err := service.prepareLinkStaging(ctx, "qbit", config.TorrentClientConfig{
		Linking:      "hardlink",
		LinkedFolder: config.StringList{filepath.Join(root, "links")},
	}, api.ClientSubject{
		SourcePath: source,
		FileList:   []string{source},
	}, api.TorrentResult{Path: filepath.Join(root, "injected.torrent"), Tracker: "EXAMPLE"})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("expected planner cancellation")
	}
}

func TestPrepareLinkStagingRejectsURLOnlyWhenFallbackDisabled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "Example.Release.2026.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	linkRoot := filepath.Join(root, "links")
	client := config.TorrentClientConfig{
		Linking:       "hardlink",
		LinkedFolder:  config.StringList{linkRoot},
		AllowFallback: new(false),
	}
	service := NewService(config.Config{}, nil)

	_, err := service.prepareLinkStaging(context.Background(), "qbit", client, api.ClientSubject{
		SourcePath: source,
		FileList:   []string{source},
	}, api.TorrentResult{URL: "https://tracker.example/torrent/1", Tracker: "EXAMPLE"})
	if err == nil {
		t.Fatal("expected URL-only layout validation error")
	}
	for _, expected := range []string{"hardlink staging", "URL-only torrent", "provide a torrent file", "enable allow_fallback"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("expected URL-only error to contain %q", expected)
		}
	}
}

func TestPrepareLinkStagingHardlinksRenamedTrackerTorrent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "Example.Show.S01E05.1080p.WEB-DL.H.264-GRP.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	torrentPath := filepath.Join(root, "renamed.torrent")
	writeQbitTestTorrent(t, torrentPath, "Example.Show.S01E05.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv", map[string]string{"source": source}, false)
	linkRoot := filepath.Join(root, "links")

	service := NewService(config.Config{}, nil)
	staging, err := service.prepareLinkStaging(context.Background(), "qbit", config.TorrentClientConfig{
		Linking:      "hardlink",
		LinkedFolder: config.StringList{linkRoot},
	}, api.ClientSubject{SourcePath: source, FileList: []string{source}}, api.TorrentResult{Path: torrentPath, Tracker: "ASC"})
	if err != nil {
		t.Fatalf("prepare link staging: %v", err)
	}
	if !staging.Linked {
		t.Fatal("expected linked staging")
	}
	linked := filepath.Join(linkRoot, "ASC", "Example.Show.S01E05.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv")
	linkedInfo, err := os.Stat(linked)
	if err != nil {
		t.Fatalf("renamed hardlink missing: %v", err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatalf("stat source: %v", err)
	}
	if !os.SameFile(linkedInfo, sourceInfo) {
		t.Fatal("renamed file must be a hardlink to the untouched source")
	}
	if _, err := os.Stat(source); err != nil {
		t.Fatalf("source must remain: %v", err)
	}
}

func TestRequireLinkStagingForRenamedContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "Example.Show.S01E05.1080p.WEB-DL.H.264-GRP.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	renamed := filepath.Join(root, "renamed.torrent")
	writeQbitTestTorrent(t, renamed, "Example.Show.S01E05.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv", map[string]string{"source": source}, false)
	same := filepath.Join(root, "same.torrent")
	writeQbitTestTorrent(t, same, filepath.Base(source), map[string]string{"source": source}, false)
	meta := api.ClientSubject{SourcePath: source, FileList: []string{source}}

	service := NewService(config.Config{}, nil)
	service.renamingTrackers = map[string]struct{}{"asc": {}}

	if err := service.requireLinkStagingForRenamedContent(context.Background(), "qbit", config.TorrentClientConfig{}, meta, api.TorrentResult{Path: renamed, Tracker: "ASC"}); err == nil ||
		!strings.Contains(err.Error(), "link staging is not active") {
		t.Fatalf("expected link staging requirement error, got %v", err)
	}
	if err := service.requireLinkStagingForRenamedContent(context.Background(), "qbit", config.TorrentClientConfig{}, meta, api.TorrentResult{Path: same, Tracker: "ASC"}); err != nil {
		t.Fatalf("unchanged names must not require staging: %v", err)
	}
	if err := service.requireLinkStagingForRenamedContent(context.Background(), "qbit", config.TorrentClientConfig{}, meta, api.TorrentResult{Path: renamed, Tracker: "OTHER"}); err != nil {
		t.Fatalf("trackers without a rename policy must be unaffected: %v", err)
	}
}

// crossDeviceLinkRoot returns a writable directory on a different filesystem
// than source, or skips when no such location exists on this host.
func crossDeviceLinkRoot(t *testing.T, source string) string {
	t.Helper()
	other, err := os.MkdirTemp("/dev/shm", "upbrr-link-*") //nolint:usetesting // t.TempDir cannot target a second filesystem
	if err != nil {
		t.Skipf("no secondary filesystem available: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(other) })
	probe := filepath.Join(other, "probe")
	if err := os.Link(source, probe); err == nil {
		t.Skip("source and /dev/shm share a filesystem")
	}
	return other
}

func TestPrepareLinkStagingCrossFilesystemHardlinkDoesNotCopy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "Example.Show.S01E05.1080p.WEB-DL.H.264-GRP.mkv")
	if err := os.WriteFile(source, []byte("media"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	renamedName := "Example.Show.S01E05.1080p.WEB-DL.DDP5.1.H.264-GRP.mkv"
	torrentPath := filepath.Join(root, "renamed.torrent")
	writeQbitTestTorrent(t, torrentPath, renamedName, map[string]string{"source": source}, false)
	linkRoot := crossDeviceLinkRoot(t, source)
	meta := api.ClientSubject{SourcePath: source, FileList: []string{source}}
	torrent := api.TorrentResult{Path: torrentPath, Tracker: "ASC"}

	service := NewService(config.Config{}, nil)
	service.renamingTrackers = map[string]struct{}{"asc": {}}

	// Fallback disabled: staging fails outright and leaves nothing behind.
	_, err := service.prepareLinkStaging(context.Background(), "qbit", config.TorrentClientConfig{
		Linking:       "hardlink",
		LinkedFolder:  config.StringList{linkRoot},
		AllowFallback: new(false),
	}, meta, torrent)
	if err == nil || !strings.Contains(err.Error(), "hardlink") {
		t.Fatalf("expected hardlink failure across filesystems, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(linkRoot, "ASC", renamedName)); !os.IsNotExist(statErr) {
		t.Fatalf("no copy or partial link may remain, stat err=%v", statErr)
	}

	// Fallback allowed: staging reports unlinked, and the renamed torrent is then
	// rejected rather than injected against files that do not exist.
	staging, err := service.prepareLinkStaging(context.Background(), "qbit", config.TorrentClientConfig{
		Linking:       "hardlink",
		LinkedFolder:  config.StringList{linkRoot},
		AllowFallback: new(true),
	}, meta, torrent)
	if err != nil {
		t.Fatalf("prepare link staging with fallback: %v", err)
	}
	if staging.Linked {
		t.Fatal("cross-filesystem hardlink must not report linked staging")
	}
	if err := service.requireLinkStagingForRenamedContent(context.Background(), "qbit", config.TorrentClientConfig{}, meta, torrent); err == nil ||
		!strings.Contains(err.Error(), "link staging is not active") {
		t.Fatalf("expected renamed torrent to be rejected after fallback, got %v", err)
	}
}
