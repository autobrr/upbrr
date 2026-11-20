// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ant"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/hdb"
	"github.com/autobrr/upbrr/internal/trackers/impl/standalone/ptp"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestNoHashRetainsDifferentTrackerBasesAcrossRestart(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "synthetic.mkv")
	const size int64 = 32 << 20
	file, err := os.OpenFile(source, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
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
		return writeExtractionTorrent(t, filepath.Join(root, name+".torrent"), info).Path
	}
	smaller := makeCandidate("small", 256<<10)
	larger := makeCandidate("large", 512<<10)
	largerHash, err := loadInfoHash(larger)
	if err != nil {
		t.Fatal(err)
	}
	registry := trackers.NewRegistry()
	// Scale the opposing piece/file-size limits to a portable, bounded fixture.
	for _, descriptor := range []trackers.Descriptor{
		{
			Name:       "PTP",
			Definition: ptp.New(),
			Artifact:   &trackers.ArtifactPolicy{MaxPieceSizeMiB: 1},
		},
		{
			Name:       "ANT",
			Definition: ant.New(),
			Artifact:   &trackers.ArtifactPolicy{MaxTorrentBytes: 1500},
		},
	} {
		if err := registry.RegisterDescriptor(descriptor); err != nil {
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
	work, completed := 0, 0
	ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
		if update.TotalPieces > 0 {
			work++
		}
		if update.Status == "completed" {
			completed++
		}
	})
	first, err := NewServiceWithRegistry(nil, tmp, registry).Create(ctx, subject)
	if err != nil {
		t.Fatalf("individually compatible NoHash lanes failed: %v", err)
	}
	if completed != 1 {
		t.Fatalf("reuse completion updates = %d, want1", completed)
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

func TestExistingTorrentSelectionAvoidsOptionalGeneration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		pieces []int64
		want   int
		nohash bool
	}{
		{
			name:   "prefer target range",
			pieces: []int64{32 << 10, 128 << 10},
			want:   1,
		},
		{
			name:   "reuse closest outside range",
			pieces: []int64{32 << 10, 256 << 10},
			want:   1,
		},
		{name: "stable target ties", pieces: []int64{128 << 10, 64 << 10}},
		{name: "no optional compact base", pieces: []int64{32 << 20}},
		{
			name:   "nohash skips excessive pieces",
			pieces: []int64{8 << 10},
			want:   -1,
			nohash: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const size int64 = 125 << 20
			dir := t.TempDir()
			source := filepath.Join(dir, "synthetic.mkv")
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
			candidates := make([]api.ClientTorrentCandidate, 0, len(tc.pieces))
			for i, piece := range tc.pieces {
				candidates = append(candidates, writeExtractionTorrent(t, filepath.Join(dir, []string{"first.torrent", "second.torrent"}[i]), metainfo.Info{
					Name:        filepath.Base(source),
					Length:      size,
					PieceLength: piece,
					Pieces:      make([]byte, ((size+piece-1)/piece)*metainfo.HashSize),
				}))
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			hashing := false
			ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
				if update.TotalPieces > 0 {
					hashing = true
					cancel()
				}
			})
			result, err := NewService(nil, t.TempDir()).Create(ctx, api.TorrentSubject{
				SourcePath:              source,
				SourceSize:              size,
				Trackers:                []string{"EXAMPLE"},
				ClientTorrentCandidates: candidates,
				TorrentOverrides:        api.TorrentOverrides{NoHash: &tc.nohash},
			})
			if err != nil || hashing {
				t.Fatalf("compatible existing torrent should avoid hashing: %v hashing=%v", err, hashing)
			}
			if tc.want < 0 {
				if len(result.SkippedTrackers) != 1 || result.SkippedTrackers[0] != "EXAMPLE" || len(result.TrackerArtifacts) != 0 {
					t.Fatalf("incompatible nohash lane was not skipped: %+v", result)
				}
				return
			}
			artifact := result.TrackerArtifacts["EXAMPLE"]
			if artifact.InfoHash != candidates[tc.want].InfoHash {
				t.Fatalf("selected hash %s, want %s", artifact.InfoHash, candidates[tc.want].InfoHash)
			}
			if len(result.BaseArtifacts) != 1 || result.PendingPublication != nil || len(result.RehashedTrackers) != 0 {
				t.Fatalf("unexpected optional artifacts or generation: %+v", result)
			}
		})
	}
}

func TestProvidedTorrentPieceCountLimits(t *testing.T) {
	t.Parallel()
	registry := trackers.NewRegistry()
	for _, descriptor := range []trackers.Descriptor{
		{
			Name:       "HDB",
			Definition: hdb.New(),
			Artifact:   &trackers.ArtifactPolicy{MaxPieceSizeMiB: 32, PieceSizeProfileURL: "https://hdbits.org"},
		},
		{
			Name:       "ANT",
			Definition: ant.New(),
			Artifact:   &trackers.ArtifactPolicy{MaxPieceSizeMiB: 128, MaxTorrentBytes: 250 << 10},
		},
	} {
		if err := registry.RegisterDescriptor(descriptor); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		tracker string
		piece   int64
		count   int
		maximum *int
		allowed bool
	}{
		{
			name:    "cap boundary",
			piece:   64 << 10,
			count:   10000,
			allowed: true,
		},
		{
			name:  "above cap",
			piece: 64 << 10,
			count: 10001,
		},
		{
			name:    "user maximum wins",
			piece:   1 << 20,
			count:   10001,
			maximum: new(1),
			allowed: true,
		},
		{
			name:    "smaller than user maximum",
			piece:   512 << 10,
			count:   20002,
			maximum: new(1),
		},
		{
			name:    "PTP maximum wins",
			tracker: "PTP",
			piece:   16 << 20,
			count:   10001,
			allowed: true,
		},
		{
			name:    "PTP smaller allowed size still exceeds cap",
			tracker: "PTP",
			piece:   8 << 20,
			count:   20002,
		},
		{
			name:    "HDB large source maximum",
			tracker: "HDB",
			piece:   32 << 20,
			count:   32769,
			allowed: true,
		},
		{
			name:    "HDB current generation ceiling",
			tracker: "HDB",
			piece:   16 << 20,
			count:   65538,
			allowed: true,
		},
		{
			name:    "generator ceiling",
			piece:   128 << 20,
			count:   10001,
			allowed: true,
		},
		{
			name:    "HDB below generation ceiling",
			tracker: "HDB",
			piece:   8 << 20,
			count:   131076,
		},
		{
			name:    "ANT byte cap still applies",
			tracker: "ANT",
			piece:   1 << 20,
			count:   13000,
			maximum: new(1),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := metainfo.Info{
				Name:        "synthetic.mkv",
				Length:      tc.piece * int64(tc.count),
				PieceLength: tc.piece,
				Pieces:      make([]byte, tc.count*metainfo.HashSize),
			}
			candidate := writeExtractionTorrent(t, filepath.Join(t.TempDir(), "source.torrent"), info)
			subject := api.TorrentSubject{
				SourcePath:       candidate.Path,
				SourceSize:       info.Length,
				TorrentOverrides: api.TorrentOverrides{MaxPieceSizeMiB: tc.maximum},
			}
			if tc.tracker != "" {
				subject.Trackers = []string{tc.tracker}
			}
			result, err := NewServiceWithRegistry(nil, t.TempDir(), registry).Create(t.Context(), subject)
			if (err == nil) != tc.allowed {
				t.Fatalf("accepted=%v, want %v: %v", err == nil, tc.allowed, err)
			}
			if tc.allowed && result.Path != candidate.Path {
				t.Fatalf("provided artifact changed: %+v", result)
			}
		})
	}
}

func TestExplicitRecheckRejectsStaleAlternativesWithoutLosingValidBase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	writeTestFile(t, source, "source-data")
	other := filepath.Join(dir, "old", "video.mkv")
	writeTestFile(t, other, "source-evil")
	good, bad := filepath.Join(dir, "good.torrent"), filepath.Join(dir, "bad.torrent")
	createTestTorrentFromExisting(t, source, good)
	createTestTorrentFromExisting(t, other, bad)
	for _, first := range []string{good, bad} {
		second := good
		if first == good {
			second = bad
		}
		hash, err := loadInfoHash(second)
		if err != nil {
			t.Fatal(err)
		}
		result, err := NewService(nil, t.TempDir()).Create(t.Context(), api.TorrentSubject{
			SourcePath:              source,
			ClientTorrentPath:       first,
			ClientTorrentCandidates: []api.ClientTorrentCandidate{{Path: second, InfoHash: hash}},
			ForceRecheck:            true,
			TorrentOverrides:        api.TorrentOverrides{NoHash: new(true)},
		})
		if err != nil {
			t.Fatalf("verified candidate lost to stale alternative: %v", err)
		}
		if result.Path != good || len(result.BaseArtifacts) != 1 {
			t.Fatalf("unexpected reuse: %+v", result)
		}
	}
}

func TestEffectivePolicySharesGenerationAndValidationMaximum(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		trackers []string
		maximum  *int
		wantMiB  int64
	}{
		{name: "unmapped without override", trackers: []string{"EXAMPLE"}},
		{
			name:    "explicit maximum without tracker",
			maximum: new(16),
			wantMiB: 16,
		},
		{
			name:     "unmapped with override",
			trackers: []string{"EXAMPLE"},
			maximum:  new(16),
			wantMiB:  16,
		},
		{
			name:     "stricter tracker",
			trackers: []string{"PTP"},
			maximum:  new(128),
			wantMiB:  16,
		},
		{
			name:     "stricter override",
			trackers: []string{"PTP"},
			maximum:  new(8),
			wantMiB:  8,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			subject := api.TorrentSubject{
				Trackers:         tc.trackers,
				SourceSize:       64 << 30,
				TorrentOverrides: api.TorrentOverrides{MaxPieceSizeMiB: tc.maximum},
			}
			policy := resolveTrackerPolicy(subject, nil)
			if tc.wantMiB == 0 {
				if policy != nil {
					t.Fatal("unmapped tracker gained a policy without an override")
				}
				return
			}
			if policy == nil {
				t.Fatal("explicit maximum has no validation policy")
			}
			limit := tc.wantMiB << 20
			if options := policy.createOptions(subject); int64(1)<<options.maxPieceExp != limit {
				t.Fatalf("generation maximum = %d, want %d", int64(1)<<options.maxPieceExp, limit)
			}
			info := metainfo.Info{Length: subject.SourceSize, PieceLength: limit}
			if err := policy.validateTorrentInfo(&info, subject); err != nil {
				t.Fatalf("valid generated piece size rejected: %v", err)
			}
			info.PieceLength *= 2
			if err := policy.validateTorrentInfo(&info, subject); err == nil {
				t.Fatal("oversized generated pieces accepted")
			}
		})
	}
}
