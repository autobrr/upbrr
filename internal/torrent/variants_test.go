// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ant"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ptp"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNoHashRetainsDifferentTrackerBasesAcrossRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "synthetic.mkv")
	const size int64 = 256 << 30
	file, err := os.OpenFile(source, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	makeCandidate := func(name string, piece int64) string {
		t.Helper()
		info := metainfo.Info{
			Name:        filepath.Base(source),
			Length:      size,
			PieceLength: piece,
			Pieces:      make([]byte, 20*(size/piece)),
		}
		data, err := bencode.Marshal(info)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, name+".torrent")
		output, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		mi := metainfo.MetaInfo{InfoBytes: data}
		if err := mi.Write(output); err != nil {
			t.Fatal(err)
		}
		if err := output.Close(); err != nil {
			t.Fatal(err)
		}
		return path
	}
	smaller := makeCandidate("small", 16<<20)
	larger := makeCandidate("large", 32<<20)
	largerHash, err := loadInfoHash(larger)
	if err != nil {
		t.Fatal(err)
	}
	registry := trackers.NewRegistry()
	for _, definition := range []trackers.Definition{ptp.New(), ant.New()} {
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	tmp := t.TempDir()
	subject := api.TorrentSubject{
		SourcePath:              source,
		SourceSize:              size,
		Trackers:                []string{"PTP", "ANT"},
		ClientTorrentPath:       smaller,
		ClientTorrentCandidates: []api.ClientTorrentCandidate{{Path: larger, InfoHash: largerHash}},
		TorrentOverrides:        api.TorrentOverrides{NoHash: new(true)},
	}
	work := 0
	ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
		if update.TotalPieces > 0 {
			work++
		}
	})
	first, err := NewServiceWithRegistry(nil, tmp, registry).Create(ctx, subject)
	if err != nil {
		t.Fatalf("individually compatible NoHash lanes failed: %v", err)
	}
	if len(first.SkippedTrackers) != 0 || len(first.RehashedTrackers) != 0 || work != 0 {
		t.Fatalf("unexpected hashing/skips: %+v work=%d", first, work)
	}
	for _, tracker := range subject.Trackers {
		artifact, ok := first.TrackerArtifacts[tracker]
		if !ok {
			t.Fatalf("missing base for %s", tracker)
		}
		one := subject
		one.Trackers = []string{tracker}
		if err := resolveTrackerPolicy(one, registry).validateTorrent(artifact.Path, one); err != nil {
			t.Fatalf("%s invalid base: %v", tracker, err)
		}
		if hash, err := loadInfoHash(artifact.Path); err != nil || hash != artifact.InfoHash {
			t.Fatalf("captured hash mismatch: %s %v", hash, err)
		}
	}
	if first.TrackerArtifacts["PTP"].Path == first.TrackerArtifacts["ANT"].Path {
		t.Fatal("incompatible policies shared one base")
	}
	if err := os.Remove(smaller); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(larger); err != nil {
		t.Fatal(err)
	}
	subject.ClientTorrentPath = ""
	subject.ClientTorrentCandidates = nil
	restarted, err := NewServiceWithRegistry(nil, tmp, registry).Create(ctx, subject)
	if err != nil || len(restarted.SkippedTrackers) != 0 || work != 0 {
		t.Fatalf("restart lost immutable reusable variants: %+v %v", restarted, err)
	}
	for tracker, expected := range first.TrackerArtifacts {
		if restarted.TrackerArtifacts[tracker] != expected {
			t.Fatalf("%s base identity changed across restart", tracker)
		}
	}
}

func TestNoHashSkipsOnlyMissingPolicyVariant(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "synthetic.mkv")
	writeTestFile(t, source, "data")
	candidate := filepath.Join(dir, "large.torrent")
	createTestTorrentFromExisting(t, source, candidate)
	mi, err := metainfo.LoadFromFile(candidate)
	if err != nil {
		t.Fatal(err)
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	info.PieceLength = 32 << 20
	mi.InfoBytes, err = bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(candidate, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := mi.Write(file); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		selected []string
		nohash   bool
		skip     []string
	}{
		{selected: []string{"PTP", "EXAMPLE"}, nohash: true},
		{selected: []string{"PTP"}, nohash: true},
		{selected: []string{"PTP"}, skip: []string{"PTP"}},
	} {
		result, err := NewService(nil, t.TempDir()).Create(t.Context(), api.TorrentSubject{
			SourcePath:           source,
			Trackers:             tc.selected,
			ClientTorrentPath:    candidate,
			TorrentOverrides:     api.TorrentOverrides{NoHash: &tc.nohash},
			SkipIfRehashTrackers: tc.skip,
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.SkippedTrackers) != 1 || result.SkippedTrackers[0] != "PTP" || len(result.RehashedTrackers) != 0 {
			t.Fatalf("unexpected partial reuse: %+v", result)
		}
		if len(tc.selected) == 2 && result.TrackerArtifacts["EXAMPLE"].Path == "" {
			t.Fatal("unrestricted tracker lost its reusable artifact")
		}
		if len(result.BaseArtifacts) != 1 {
			t.Fatalf("available general base not retained: %+v", result)
		}
		if _, err := os.Stat(result.BaseArtifacts[0].Path); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGeneratedBasesRemainStagedUntilPublication(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "synthetic.mkv")
	writeTestFile(t, source, "source-data")
	root := t.TempDir()
	service := NewService(nil, root)
	subject := api.TorrentSubject{
		SourcePath:                 source,
		Trackers:                   []string{"EXAMPLE"},
		DeferGenerationPublication: true,
	}
	result, err := service.Create(t.Context(), subject)
	if err != nil {
		t.Fatal(err)
	}
	if result.PendingPublication == nil || len(result.BaseArtifacts) != 1 {
		t.Fatalf("expected one shared staged base, got %+v", result)
	}
	for _, artifact := range result.BaseArtifacts {
		if _, err := os.Stat(artifact.Path); !os.IsNotExist(err) {
			t.Fatalf("base published before source fence: %v", err)
		}
	}
	if err := result.PendingPublication.Discard(); err != nil {
		t.Fatal(err)
	}
	base, err := TempTorrentPath(root, source)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := filepath.Glob(filepath.Join(filepath.Dir(base), ".generating-*.torrent"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("staged artifacts leaked: %v %v", staged, err)
	}
	result, err = service.Create(t.Context(), subject)
	if err != nil {
		t.Fatal(err)
	}
	path, err := result.PendingPublication.Publish()
	if err != nil {
		t.Fatal(err)
	}
	if path != result.Path {
		t.Fatal("publication changed assigned path")
	}
	for tracker, artifact := range result.TrackerArtifacts {
		hash, err := loadInfoHash(artifact.Path)
		if err != nil || hash != artifact.InfoHash {
			t.Fatalf("%s published wrong artifact: %s %v", tracker, hash, err)
		}
	}
}

func TestGeneralPieceGoalHonorsAttainableBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		piece      int64
		count      int
		maximum    *int
		acceptable bool
	}{
		{
			name:       "general target",
			piece:      32 << 20,
			count:      1500,
			acceptable: true,
		},
		{
			name:  "needs larger pieces",
			piece: 16 << 20,
			count: 4000,
		},
		{
			name:       "explicit maximum",
			piece:      16 << 20,
			count:      4000,
			maximum:    new(16),
			acceptable: true,
		},
		{
			name:       "generator maximum",
			piece:      128 << 20,
			count:      4000,
			acceptable: true,
		},
		{
			name:  "needs smaller pieces",
			piece: 16 << 20,
			count: 500,
		},
		{
			name:       "minimum pieces",
			piece:      64 << 10,
			count:      500,
			acceptable: true,
		},
		{
			name:       "tiny source",
			piece:      1 << 20,
			count:      8,
			acceptable: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := metainfo.Info{
				Name:        "synthetic.mkv",
				Length:      tc.piece * int64(tc.count),
				PieceLength: tc.piece,
				Pieces:      make([]byte, tc.count*20),
			}
			data, err := bencode.Marshal(info)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "base.torrent")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			mi := metainfo.MetaInfo{InfoBytes: data}
			if err := mi.Write(file); err != nil {
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			subject := api.TorrentSubject{}
			subject.TorrentOverrides.MaxPieceSizeMiB = tc.maximum
			if got := generalPieceCount(path, subject); got != tc.acceptable {
				t.Fatalf("acceptable piece goal = %v, want %v", got, tc.acceptable)
			}
		})
	}
}
