// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestOrdinaryReuseDoesNotVerifyMediaBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	writeTestFile(t, source, "source-data")
	other := filepath.Join(dir, "client", "video.mkv")
	writeTestFile(t, other, "source-evil")
	artifact := filepath.Join(dir, "client.torrent")
	createTestTorrentFromExisting(t, other, artifact)
	hash, err := loadInfoHash(artifact)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewService(api.NopLogger{}, t.TempDir()).Create(context.Background(), api.TorrentSubject{
		SourcePath:            source,
		ClientTorrentPath:     artifact,
		ClientTorrentInfoHash: hash,
		TorrentOverrides:      api.TorrentOverrides{NoHash: new(true)},
	})
	if err != nil || result.Path != artifact || result.InfoHash != hash {
		t.Fatalf("expected compatible artifact without reading media: result=%+v err=%v", result, err)
	}
}

func TestPersonalizedTorrentReusesGeneratedPieces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	writeTestFile(t, source, "source-data")
	service := NewService(nil, t.TempDir())
	base, err := service.Create(t.Context(), api.TorrentSubject{SourcePath: source})
	if err != nil {
		t.Fatal(err)
	}
	personalizedPath := filepath.Join(dir, "personalized.torrent")
	if err := trackers.WritePersonalizedTorrent(base.Path, personalizedPath, "https://tracker.example.invalid/announce", "synthetic"); err != nil {
		t.Fatal(err)
	}
	baseMeta, err := metainfo.LoadFromFile(base.Path)
	if err != nil {
		t.Fatal(err)
	}
	personalized, err := metainfo.LoadFromFile(personalizedPath)
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, err := baseMeta.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	personalizedInfo, err := personalized.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	if base.InfoHash == personalized.HashInfoBytes().String() || !bytes.Equal(baseInfo.Pieces, personalizedInfo.Pieces) {
		t.Fatal("expected personalization to change infohash while preserving piece hashes")
	}
	var work atomic.Int64
	ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
		if update.TotalPieces > 0 {
			work.Add(1)
		}
	})
	result, err := service.Create(ctx, api.TorrentSubject{
		SourcePath:            source,
		ClientTorrentPath:     personalizedPath,
		ClientTorrentInfoHash: personalized.HashInfoBytes().String(),
		TorrentOverrides:      api.TorrentOverrides{NoHash: new(true)},
	})
	if err != nil || result.InfoHash != personalized.HashInfoBytes().String() || work.Load() != 0 {
		t.Fatalf("personalized artifact was not reused: result=%+v work=%d err=%v", result, work.Load(), err)
	}
}

func TestReuseRejectsOverwrittenCandidateWithSameInfoHash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	writeTestFile(t, source, "source-data")
	path := filepath.Join(dir, "snapshot.torrent")
	createTestTorrentFromExisting(t, source, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := metainfo.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := meta.HashInfoBytes().String()
	meta.Comment = "changed outer metadata"
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := meta.Write(file); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = NewService(api.NopLogger{}, t.TempDir()).Create(t.Context(), api.TorrentSubject{
		SourcePath: source,
		ClientTorrentCandidates: []api.ClientTorrentCandidate{{
			Path:          path,
			InfoHash:      hash,
			ContentSHA256: fmt.Sprintf("%x", sha256.Sum256(data)),
		}},
		TorrentOverrides: api.TorrentOverrides{NoHash: new(true)},
	})
	if err == nil {
		t.Fatal("expected modified captured artifact to be rejected")
	}
}

func TestReuseSearchesClientAlternativesForHardPolicy(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	writeTestFile(t, source, "source-data")
	paths := []string{filepath.Join(dir, "large.torrent"), filepath.Join(dir, "compatible.torrent")}
	for index, exp := range []uint{19, 18} {
		if _, err := mkbrr.Create(mkbrr.CreateOptions{
			Path:           source,
			OutputPath:     paths[index],
			PieceLengthExp: &exp,
			MaxPieceLength: &exp,
		}); err != nil {
			t.Fatal(err)
		}
	}
	firstHash, err := loadInfoHash(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := loadInfoHash(paths[1])
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewService(api.NopLogger{}, t.TempDir()).Create(t.Context(), api.TorrentSubject{
		SourcePath:              source,
		SourceSize:              11,
		Trackers:                []string{"PTP"},
		ClientTorrentPath:       paths[0],
		ClientTorrentInfoHash:   firstHash,
		ClientTorrentCandidates: []api.ClientTorrentCandidate{{Path: paths[1], InfoHash: secondHash}},
		TorrentOverrides:        api.TorrentOverrides{NoHash: new(true)},
	})
	if err != nil || result.InfoHash != secondHash {
		t.Fatalf("expected compatible alternative without generation: result=%+v err=%v", result, err)
	}
}

func TestSingleFileWrapperReusesPiecesWithoutMediaHashing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	folder := filepath.Join(dir, "wrapped")
	source := filepath.Join(folder, "video.mkv")
	writeTestFile(t, source, "source-data")
	path := filepath.Join(dir, "wrapped.torrent")
	createTestTorrentFromExisting(t, folder, path)
	wrapped, err := metainfo.LoadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := wrapped.UnmarshalInfo()
	if err != nil || len(before.Files) != 1 {
		t.Fatalf("expected one-file wrapper: info=%+v err=%v", before, err)
	}
	var callbacks atomic.Int64
	ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
		if update.TotalPieces > 0 {
			callbacks.Add(1)
		}
	})
	result, err := NewService(api.NopLogger{}, t.TempDir()).Create(ctx, api.TorrentSubject{
		SourcePath:            source,
		ClientTorrentPath:     path,
		ClientTorrentInfoHash: wrapped.HashInfoBytes().String(),
		TorrentOverrides:      api.TorrentOverrides{NoHash: new(true)},
	})
	if err != nil {
		t.Fatalf("expected wrapper-only conversion: %v", err)
	}
	converted, err := metainfo.LoadFromFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := converted.UnmarshalInfo()
	if err != nil || len(after.Files) != 0 || after.Name != "video.mkv" || !bytes.Equal(before.Pieces, after.Pieces) || callbacks.Load() != 0 {
		t.Fatalf("wrapper conversion changed content or hashed media: info=%+v callbacks=%d err=%v", after, callbacks.Load(), err)
	}
}

func TestKeepFolderPreservesOneFileLayout(t *testing.T) {
	t.Parallel()
	folder := filepath.Join(t.TempDir(), "release")
	source := filepath.Join(folder, "video.mkv")
	writeTestFile(t, source, "source-data")
	wrapper := filepath.Join(t.TempDir(), "wrapper.torrent")
	createTestTorrentFromExisting(t, folder, wrapper)
	wrappedMeta, err := metainfo.LoadFromFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := NewService(nil, t.TempDir()).Create(t.Context(), api.TorrentSubject{
		SourcePath:            folder,
		FileList:              []string{source},
		KeepFolder:            true,
		ClientTorrentPath:     wrapper,
		ClientTorrentInfoHash: wrappedMeta.HashInfoBytes().String(),
		TorrentOverrides:      api.TorrentOverrides{NoHash: new(true)},
	})
	if err != nil || reused.InfoHash != wrappedMeta.HashInfoBytes().String() {
		t.Fatalf("expected keep-folder wrapper to be reused: result=%+v err=%v", reused, err)
	}
	result, err := NewService(nil, t.TempDir()).Create(t.Context(), api.TorrentSubject{
		SourcePath: folder,
		FileList:   []string{source},
		KeepFolder: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	mi, err := metainfo.LoadFromFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil || info.Name != "release" || len(info.Files) != 1 || len(info.Files[0].Path) != 1 || info.Files[0].Path[0] != "video.mkv" {
		t.Fatalf("keep folder did not preserve wrapper: info=%+v err=%v", info, err)
	}
}

func TestKeepFolderRejectsSameBasenameSingleFileMetainfo(t *testing.T) {
	t.Parallel()
	folder := filepath.Join(t.TempDir(), "release")
	source := filepath.Join(folder, "release")
	writeTestFile(t, source, "source-data")
	artifact := filepath.Join(t.TempDir(), "single.torrent")
	createTestTorrentFromExisting(t, source, artifact)
	mi, err := metainfo.LoadFromFile(artifact)
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewService(nil, t.TempDir()).Create(t.Context(), api.TorrentSubject{
		SourcePath:            folder,
		FileList:              []string{source},
		KeepFolder:            true,
		ClientTorrentPath:     artifact,
		ClientTorrentInfoHash: mi.HashInfoBytes().String(),
		TorrentOverrides:      api.TorrentOverrides{NoHash: new(true)},
	})
	if err == nil {
		t.Fatal("single-file metainfo must not satisfy a keep-folder layout")
	}
}

func TestConcurrentSingleFileWrapperConversion(t *testing.T) {
	t.Parallel()
	for name, sidecars := range map[string]bool{"folder wrapper": false, "partial piece": true} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			folder := filepath.Join(dir, "release")
			source := filepath.Join(folder, "video.mkv")
			writeTestFile(t, source, "source-data")
			if sidecars {
				writeTestFile(t, filepath.Join(folder, "zz-readme.nfo"), "sidecar")
			}
			wrapper := filepath.Join(dir, "wrapper.torrent")
			createTestTorrentFromExisting(t, folder, wrapper)
			mi, err := metainfo.LoadFromFile(wrapper)
			if err != nil {
				t.Fatal(err)
			}
			tmpRoot := t.TempDir()
			subject := api.TorrentSubject{
				SourcePath:            source,
				ClientTorrentPath:     wrapper,
				ClientTorrentInfoHash: mi.HashInfoBytes().String(),
				TorrentOverrides:      api.TorrentOverrides{NoHash: new(!sidecars)},
			}
			const count = 8
			results := make(chan api.TorrentResult, count)
			errors := make(chan error, count)
			var workers sync.WaitGroup
			var ready sync.WaitGroup
			var partial, other atomic.Int64
			ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
				if update.TotalPieces > 0 {
					if update.Message == "Hashing final partial torrent piece" {
						partial.Add(1)
					} else {
						other.Add(1)
					}
				}
			})
			ready.Add(count)
			start := make(chan struct{})
			for range count {
				service := NewService(nil, tmpRoot)
				workers.Go(func() {
					ready.Done()
					<-start
					result, err := service.Create(ctx, subject)
					results <- result
					errors <- err
				})
			}
			ready.Wait()
			close(start)
			workers.Wait()
			close(results)
			close(errors)
			for err := range errors {
				if err != nil {
					t.Fatalf("concurrent wrapper conversion: %v", err)
				}
			}
			var hash string
			for result := range results {
				if hash != "" && result.InfoHash != hash {
					t.Fatalf("concurrent conversions produced different infohashes: %s and %s", hash, result.InfoHash)
				}
				hash = result.InfoHash
			}
			if hash == "" {
				t.Fatal("no converted artifact")
			}
			if other.Load() != 0 || sidecars && partial.Load() == 0 || !sidecars && partial.Load() != 0 {
				t.Fatalf("wrong hashing work: partial=%d other=%d", partial.Load(), other.Load())
			}
		})
	}
}

func TestReuseRejectsMalformedPieceMetadata(t *testing.T) {
	t.Parallel()
	valid := metainfo.Info{
		Name:        "video.mkv",
		Length:      11,
		PieceLength: 32768,
		Pieces:      make([]byte, metainfo.HashSize),
	}
	if err := ValidatePieceMetadata(valid); err != nil {
		t.Fatalf("valid metadata: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*metainfo.Info)
	}{
		{name: "truncated pieces", edit: func(i *metainfo.Info) { i.Pieces = i.Pieces[:len(i.Pieces)-1] }},
		{name: "missing piece", edit: func(i *metainfo.Info) { i.Length = i.PieceLength + 1 }},
		{name: "unsafe path", edit: func(i *metainfo.Info) {
			i.Files = []metainfo.FileInfo{{Length: 11, Path: []string{"..", "video.mkv"}}}
		}},
		{name: "ambiguous name", edit: func(i *metainfo.Info) { i.NameUtf8 = "other.mkv" }},
		{name: "hybrid v2 tree", edit: func(i *metainfo.Info) {
			i.MetaVersion = 2
			i.FileTree.Dir = map[string]metainfo.FileTree{
				"other.mkv": {File: metainfo.FileTreeFile{Length: i.Length}},
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := valid
			test.edit(&info)
			if err := ValidatePieceMetadata(info); err == nil {
				t.Fatal("expected malformed metainfo to be rejected")
			}
		})
	}
}

func TestExplicitRecheckVerifiesMedia(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	writeTestFile(t, source, "source-data")
	other := filepath.Join(dir, "client", "video.mkv")
	writeTestFile(t, other, "source-evil")
	artifact := filepath.Join(dir, "client.torrent")
	createTestTorrentFromExisting(t, other, artifact)
	var calls atomic.Int64
	ctx := api.WithUploadProgressReporter(context.Background(), func(update api.UploadProgressUpdate) {
		if update.TotalPieces > 0 {
			calls.Add(1)
		}
	})
	_, err := NewService(api.NopLogger{}, t.TempDir()).Create(ctx, api.TorrentSubject{
		SourcePath:        source,
		ClientTorrentPath: artifact,
		ForceRecheck:      true,
		TorrentOverrides:  api.TorrentOverrides{NoHash: new(true)},
	})
	if err == nil || calls.Load() == 0 {
		t.Fatalf("explicit recheck must read and reject mismatched media: callbacks=%d err=%v", calls.Load(), err)
	}
}

func TestExplicitRecheckRequiresSourceData(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	writeTestFile(t, source, "source-data")
	artifact := filepath.Join(dir, "provided.torrent")
	createTestTorrentFromExisting(t, source, artifact)
	_, err := NewService(nil, t.TempDir()).Create(t.Context(), api.TorrentSubject{SourcePath: artifact, ForceRecheck: true})
	if err == nil {
		t.Fatal("explicit recheck must not succeed without local source data")
	}
}
