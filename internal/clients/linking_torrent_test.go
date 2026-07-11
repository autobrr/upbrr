// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package clients

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

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
	info := metainfo.Info{Name: rootName, PieceLength: 16 * 1024, Private: new(true)}
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

	plan, err := buildTorrentLinkPlan(torrentPath, api.PreparedMetadata{SourcePath: source, FileList: []string{source}})
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

	plan, err := buildTorrentLinkPlan(torrentPath, api.PreparedMetadata{SourcePath: sourceRoot, FileList: []string{source}})
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

	_, err := buildTorrentLinkPlan(torrentPath, api.PreparedMetadata{SourcePath: sourceRoot})
	if err == nil || !strings.Contains(err.Error(), "no unique source match") {
		t.Fatalf("expected ambiguous source mapping error")
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

	_, err := service.prepareLinkStaging(context.Background(), "qbit", client, api.PreparedMetadata{
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
