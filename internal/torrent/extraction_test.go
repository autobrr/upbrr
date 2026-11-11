// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	internalerrors "github.com/autobrr/upbrr/internal/errors"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestExtractSingleFileReusesAlignedPieces(t *testing.T) {
	t.Parallel()
	const piece = 16
	for _, tc := range []struct {
		name                          string
		prefix, size, suffix          int
		nohash, wantHash, wantMissing bool
	}{
		{
			name:   "aligned extra files on both sides",
			prefix: piece,
			size:   2 * piece,
			suffix: 7,
			nohash: true,
		},
		{
			name:   "partial video at torrent EOF",
			prefix: piece,
			size:   2*piece + 5,
			nohash: true,
		},
		{
			name:     "only final partial piece needs hashing",
			prefix:   piece,
			size:     2*piece + 5,
			suffix:   7,
			wantHash: true,
		},
		{
			name:        "nohash forbids a new final hash",
			size:        2*piece + 5,
			suffix:      7,
			nohash:      true,
			wantMissing: true,
		},
		{
			name:        "misaligned video start is not extractable",
			prefix:      3,
			size:        2*piece + 5,
			suffix:      7,
			nohash:      true,
			wantMissing: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject, video, _ := extractionFixture(t, tc.prefix, tc.size, tc.suffix)
			subject.TorrentOverrides.NoHash = new(tc.nohash)
			// An unchanged length with a deliberately different first byte proves that
			// extraction preserves trusted complete hashes instead of rehashing the file.
			if tc.wantHash {
				changed := bytes.Clone(video)
				changed[0] ^= 0xff
				if err := os.WriteFile(subject.SourcePath, changed, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			work := 0
			ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
				if update.TotalPieces > 0 {
					if update.TotalPieces != 1 || update.Message != "Hashing final partial torrent piece" {
						t.Errorf("unexpected piece work: %+v", update)
					}
					if update.CompletedPieces == 1 {
						work++
					}
				}
			})
			root := t.TempDir()
			result, err := NewService(nil, root).Create(ctx, subject)
			if tc.wantMissing {
				if !errors.Is(err, internalerrors.ErrNotFound) || work != 0 {
					t.Fatalf("unsafe extraction: result=%+v work=%d err=%v", result, work, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			mi, err := metainfo.LoadFromFile(result.Path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := mi.UnmarshalInfo()
			if err != nil {
				t.Fatal(err)
			}
			if len(info.Files) != 0 || info.Name != filepath.Base(subject.SourcePath) || info.Length != int64(len(video)) || !bytes.Equal(info.Pieces, extractionPieces(video, piece)) {
				t.Fatalf("extracted torrent has wrong layout or pieces: name=%q length=%d files=%d", info.Name, info.Length, len(info.Files))
			}
			if (work == 1) != tc.wantHash {
				t.Fatalf("partial hashes=%d wantHash=%t", work, tc.wantHash)
			}
		})
	}
}

func TestExtractionPrefersCompatibleSingleFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		suffix   int
		limitMiB int
	}{
		{name: "folder wrapper"},
		{name: "sidecars", suffix: 7},
		{
			name:     "exact file fails requested piece policy",
			suffix:   7,
			limitMiB: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject, video, _ := extractionFixture(t, 0, 37, tc.suffix)
			exactPiece := 16
			if tc.limitMiB != 0 {
				exactPiece = 2 << 20
				subject.TorrentOverrides.MaxPieceSizeMiB = &tc.limitMiB
			}
			exact := writeExtractionTorrent(t, filepath.Join(t.TempDir(), "exact.torrent"), metainfo.Info{
				Name:        filepath.Base(subject.SourcePath),
				Length:      int64(len(video)),
				PieceLength: int64(exactPiece),
				Pieces:      extractionPieces(video, exactPiece),
			})
			subject.ClientTorrentCandidates = []api.ClientTorrentCandidate{exact}
			root := t.TempDir()
			work := 0
			ctx := api.WithUploadProgressReporter(t.Context(), func(u api.UploadProgressUpdate) {
				if u.TotalPieces > 0 {
					work++
				}
			})
			result, err := NewService(nil, root).Create(ctx, subject)
			if err != nil || (tc.limitMiB == 0 && (result.InfoHash != exact.InfoHash || work != 0)) || (tc.limitMiB != 0 && work != 2) {
				t.Fatalf("exact candidate not preferred: result=%+v work=%d err=%v", result, work, err)
			}
			mi, err := metainfo.LoadFromFile(result.Path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := mi.UnmarshalInfo()
			if err != nil || info.PieceLength != 16 || !bytes.Equal(info.Pieces, extractionPieces(video, 16)) {
				t.Fatalf("fallback produced wrong pieces: %v", err)
			}
			files, err := filepath.Glob(filepath.Join(root, "*", "converted-*.torrent"))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 0 {
				t.Fatalf("unnecessary conversions=%d", len(files))
			}
		})
	}
}

func TestExtractionPrefersTargetRangeBeforeHashing(t *testing.T) {
	t.Parallel()
	const size int64 = 128 << 20
	dir := t.TempDir()
	source := filepath.Join(dir, "Example.Video.2026.mkv")
	file, err := os.OpenFile(source, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(size); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	candidates := make([]api.ClientTorrentCandidate, 0, 3)
	candidates = append(candidates, writeExtractionTorrent(t, filepath.Join(dir, "direct.torrent"), metainfo.Info{
		Name:        filepath.Base(source),
		Length:      size,
		PieceLength: 32 << 10,
		Pieces:      make([]byte, size/(32<<10)*metainfo.HashSize),
	}))
	for i, piece := range []int64{64 << 10, 128 << 10} {
		candidates = append(candidates, writeExtractionTorrent(t, filepath.Join(dir, []string{"compact.torrent", "general.torrent"}[i]), metainfo.Info{
			Name:        "Example.Release.2026",
			PieceLength: piece,
			Pieces:      make([]byte, size/piece*metainfo.HashSize),
			Files:       []metainfo.FileInfo{{Length: size, Path: []string{filepath.Base(source)}}},
		}))
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	work := 0
	ctx = api.WithUploadProgressReporter(ctx, func(u api.UploadProgressUpdate) {
		if u.TotalPieces > 0 {
			work++
			cancel()
		}
	})
	result, err := NewService(nil, t.TempDir()).Create(ctx, api.TorrentSubject{
		SourcePath:              source,
		SourceSize:              size,
		ClientTorrentCandidates: candidates,
	})
	if err != nil || work != 0 {
		t.Fatalf("available extraction should avoid hashing: work=%d err=%v", work, err)
	}
	mi, err := metainfo.LoadFromFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.PieceLength != 128<<10 || info.NumPieces() != 1024 || len(info.Files) != 0 {
		t.Fatalf("wrong general extraction: piece=%d count=%d files=%d", info.PieceLength, info.NumPieces(), len(info.Files))
	}
}

func TestExtractionPrefersExistingHashesOverPartialHashing(t *testing.T) {
	t.Parallel()
	subject, video, _ := extractionFixture(t, 0, 37, 7)
	alternative, _, _ := extractionFixture(t, 0, 37, 0)
	subject.ClientTorrentCandidates = []api.ClientTorrentCandidate{{Path: alternative.ClientTorrentPath, InfoHash: alternative.ClientTorrentInfoHash}}
	work := 0
	ctx := api.WithUploadProgressReporter(t.Context(), func(u api.UploadProgressUpdate) {
		if u.TotalPieces > 0 {
			work++
		}
	})
	result, err := NewService(nil, t.TempDir()).Create(ctx, subject)
	if err != nil || work != 0 {
		t.Fatalf("partial hashing ran despite a metadata-only alternative: work=%d err=%v", work, err)
	}
	mi, err := metainfo.LoadFromFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil || !bytes.Equal(info.Pieces, extractionPieces(video, 16)) {
		t.Fatalf("wrong extracted pieces: %v", err)
	}
}

func TestExtractionRejectsAmbiguousFile(t *testing.T) {
	t.Parallel()
	subject, video, info := extractionFixture(t, 0, 32, 0)
	info.Files = append(info.Files, metainfo.FileInfo{Length: int64(len(video)), Path: []string{"other", filepath.Base(subject.SourcePath)}})
	info.Pieces = extractionPieces(append(bytes.Clone(video), video...), 16)
	candidate := writeExtractionTorrent(t, subject.ClientTorrentPath, info)
	subject.ClientTorrentInfoHash = candidate.InfoHash
	subject.TorrentOverrides.NoHash = new(true)
	if _, err := NewService(nil, t.TempDir()).Create(t.Context(), subject); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("ambiguous source accepted: %v", err)
	}
}

func TestPartialExtractionHonorsTrackerSkipAndPublication(t *testing.T) {
	t.Parallel()
	subject, _, _ := extractionFixture(t, 0, 37, 7)
	subject.Trackers = []string{"ALLOW", "SKIP"}
	subject.SkipIfRehashTrackers = []string{"SKIP"}
	subject.DeferGenerationPublication = true
	root := t.TempDir()
	result, err := NewService(nil, root).Create(t.Context(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if result.PendingPublication == nil || !slices.Equal(result.RehashedTrackers, []string{"ALLOW"}) || !slices.Equal(result.SkippedTrackers, []string{"SKIP"}) || len(result.TrackerArtifacts) != 1 {
		t.Fatalf("partial piece bypassed generation rules: %+v", result)
	}
	for _, artifact := range result.BaseArtifacts {
		if _, err := os.Stat(artifact.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("new partial hash published early: %v", err)
		}
	}
	if err := result.PendingPublication.Discard(); err != nil {
		t.Fatal(err)
	}
	// Once discarded, no automatically discovered artifact may retain the new hash.
	subject.ClientTorrentPath = ""
	subject.ClientTorrentInfoHash = ""
	subject.Trackers = nil
	subject.TorrentOverrides.NoHash = new(true)
	if _, err := NewService(nil, root).Create(t.Context(), subject); !errors.Is(err, internalerrors.ErrNotFound) {
		t.Fatalf("discarded partial extraction was reused: %v", err)
	}
}

func TestPartialExtractionCancellationPreventsPublication(t *testing.T) {
	t.Parallel()
	subject, _, _ := extractionFixture(t, 0, 37, 7)
	root := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = api.WithUploadProgressReporter(ctx, func(u api.UploadProgressUpdate) {
		if u.TotalPieces > 0 {
			cancel()
		}
	})
	if _, err := NewService(nil, root).Create(ctx, subject); !errors.Is(err, context.Canceled) {
		t.Fatalf("partial extraction ignored cancellation: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(root, "*", "converted-*.torrent"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("canceled extraction published %d conversions", len(files))
	}
}

func extractionFixture(t *testing.T, prefix, size, suffix int) (api.TorrentSubject, []byte, metainfo.Info) {
	t.Helper()
	const piece = 16
	dir := t.TempDir()
	source := filepath.Join(dir, "Example.Video.2026.mkv")
	video := make([]byte, size)
	for i := range video {
		video[i] = byte(i % 251)
	}
	if err := os.WriteFile(source, video, 0o600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{Name: "Example.Release.2026", PieceLength: int64(piece)}
	stream := bytes.Repeat([]byte("n"), prefix)
	if prefix > 0 {
		info.Files = append(info.Files, metainfo.FileInfo{Length: int64(prefix), Path: []string{"readme.nfo"}})
	}
	info.Files = append(info.Files, metainfo.FileInfo{Length: int64(size), Path: []string{filepath.Base(source)}})
	stream = append(stream, video...)
	if suffix > 0 {
		info.Files = append(info.Files, metainfo.FileInfo{Length: int64(suffix), Path: []string{"details.txt"}})
		stream = append(stream, bytes.Repeat([]byte("s"), suffix)...)
	}
	info.Pieces = extractionPieces(stream, piece)
	candidate := writeExtractionTorrent(t, filepath.Join(dir, "client.torrent"), info)
	return api.TorrentSubject{
		SourcePath:            source,
		SourceSize:            int64(size),
		ClientTorrentPath:     candidate.Path,
		ClientTorrentInfoHash: candidate.InfoHash,
	}, video, info
}

func extractionPieces(data []byte, piece int) []byte {
	var pieces []byte
	for offset := 0; offset < len(data); offset += piece {
		hash := metainfo.HashBytes(data[offset:min(offset+piece, len(data))])
		pieces = append(pieces, hash[:]...)
	}
	return pieces
}

func writeExtractionTorrent(t *testing.T, path string, info metainfo.Info) api.ClientTorrentCandidate {
	t.Helper()
	data, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: data}
	var encoded bytes.Buffer
	if err := mi.Write(&encoded); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return api.ClientTorrentCandidate{Path: path, InfoHash: mi.HashInfoBytes().String()}
}
