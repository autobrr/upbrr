// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package torrent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"
	mkbrr "github.com/autobrr/mkbrr/torrent"
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
		var updates []api.UploadProgressUpdate
		ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
			updates = append(updates, update)
		})
		result, err := NewService(nil, t.TempDir()).Create(ctx, api.TorrentSubject{
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
		if len(tc.selected) == 1 {
			if len(updates) == 0 || updates[len(updates)-1].Status != "skipped" || updates[len(updates)-1].Tracker != "PTP" {
				t.Fatalf("expected sole skipped tracker with retained general base to close as skipped, got %+v", updates)
			}
			if slices.ContainsFunc(updates, func(u api.UploadProgressUpdate) bool { return u.Status == "completed" }) {
				t.Fatalf("sole skipped tracker must not emit completed status, got %+v", updates)
			}
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

func TestVariantPublicationCanceledWhileRetentionLockHeld(t *testing.T) {
	dir := t.TempDir()
	stage := filepath.Join(dir, "staged.torrent")
	destination := filepath.Join(dir, "retained.torrent")
	writeTestFile(t, stage, "synthetic torrent")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	file := &generatedTorrentPublication{
		stage:       stage,
		destination: destination,
		cancelErr:   ctx.Err,
	}
	publication := &variantPublication{files: []*generatedTorrentPublication{file}, path: destination}

	started := make(chan struct{})
	done := make(chan error, 1)
	retainedVariantMu.Lock()
	go func() {
		close(started)
		_, err := publication.Publish()
		done <- err
	}()
	<-started
	cancel()
	retainedVariantMu.Unlock()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Publish error = %v, want context.Canceled", err)
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected destination to remain absent after cancellation, stat err = %v", err)
	}
	if _, err := os.Stat(stage); err != nil {
		t.Fatalf("expected staging file to remain until Discard: %v", err)
	}
	if err := publication.Discard(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected staging file to be removed by Discard, stat err = %v", err)
	}
}

func TestMixedTrackerRecheckReportsSkippedAndReady(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := filepath.Join(dir, "video.mkv")
	const size = 1 << 20
	writeSizedTorrentSource(t, source, size)
	exact := filepath.Join(t.TempDir(), "exact.torrent")
	if _, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:           source,
		OutputPath:     exact,
		PieceLengthExp: new(uint(25)),
		MaxPieceLength: new(uint(25)),
		Quiet:          true,
	}); err != nil {
		t.Fatal(err)
	}
	stale := writeExtractionTorrent(t, filepath.Join(t.TempDir(), "stale.torrent"), metainfo.Info{
		Name:        "wrapped",
		PieceLength: 32 << 10,
		Pieces:      make([]byte, (size/(32<<10))*metainfo.HashSize),
		Files:       []metainfo.FileInfo{{Length: size, Path: []string{filepath.Base(source)}}},
	})
	wrapper, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:           dir,
		OutputPath:     filepath.Join(t.TempDir(), "good-wrapper.torrent"),
		PieceLengthExp: new(uint(19)),
		Quiet:          true,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, mode := range []string{"nohash reuse", "deferred generation"} {
		t.Run(mode, func(t *testing.T) {
			registry := trackers.NewRegistry()
			if err := registry.Register(hdb.New()); err != nil {
				t.Fatal(err)
			}
			subject := api.TorrentSubject{
				SourcePath:              source,
				ClientTorrentPath:       exact,
				ClientTorrentCandidates: []api.ClientTorrentCandidate{stale},
				Trackers:                []string{" ptp ", "tl"},
				ForceRecheck:            true,
			}
			switch mode {
			case "nohash reuse":
				subject.TorrentOverrides.NoHash = new(true)
				subject.ClientTorrentCandidates = append(subject.ClientTorrentCandidates, api.ClientTorrentCandidate{Path: wrapper.Path, InfoHash: wrapper.InfoHash})
			case "deferred generation":
				subject.Trackers = append(subject.Trackers, "HDB")
				subject.SkipIfRehashTrackers = []string{"PTP"}
				subject.DeferGenerationPublication = true
			}
			var mu sync.Mutex
			var updates []api.UploadProgressUpdate
			ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
				mu.Lock()
				defer mu.Unlock()
				updates = append(updates, update)
			})
			result, err := NewServiceWithRegistry(nil, t.TempDir(), registry).Create(ctx, subject)
			if err != nil {
				t.Fatal(err)
			}
			if result.PendingPublication != nil {
				defer func() { _ = result.PendingPublication.Discard() }()
			}
			if !slices.Equal(result.SkippedTrackers, []string{"PTP"}) || result.TrackerArtifacts["TL"].Path == "" {
				t.Fatalf("expected a skipped PTP lane and a usable TL torrent: %+v", result)
			}
			last := make(map[string]api.UploadProgressUpdate)
			recheckedPTP := false
			for _, update := range updates {
				last[update.Tracker] = update
				recheckedPTP = recheckedPTP || update.Tracker == "PTP" && update.Message == "Verifying torrent pieces"
			}
			if !recheckedPTP || last["PTP"].Status != "skipped" || last["PTP"].Message != "Torrent skipped: rehash required" {
				t.Fatalf("expected skipped terminal progress after stale PTP wrapper recheck: %+v", updates)
			}
			if mode == "nohash reuse" && last["TL"].Status != "completed" {
				t.Fatalf("expected the verified TL scope to complete: %+v", last)
			}
			if mode == "deferred generation" && (result.PendingPublication == nil || last["HDB"].Status != "running") {
				t.Fatalf("expected HDB scope to stay open for deferred publication: %+v", last)
			}
		})
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
			writeSizedTorrentSource(t, source, size)
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
		var updates []api.UploadProgressUpdate
		ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
			updates = append(updates, update)
		})
		result, err := NewService(nil, t.TempDir()).Create(ctx, api.TorrentSubject{
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
		if slices.ContainsFunc(updates, func(u api.UploadProgressUpdate) bool { return u.Status == "failed" }) {
			t.Fatalf("candidate recheck failure emitted premature failed status before valid candidate succeeded: %+v", updates)
		}
		if len(updates) == 0 || updates[len(updates)-1].Status != "completed" {
			t.Fatalf("expected final completed status after valid candidate succeeded, got %+v", updates)
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

type recordingDebugLogger struct {
	api.NopLogger
	mu    sync.Mutex
	lines []string
}

func (l *recordingDebugLogger) Debugf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *recordingDebugLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *recordingDebugLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}

func TestMultiTrackerAlignedWrapperRecheckClosesTorrentScopes(t *testing.T) {
	t.Parallel()
	folder := filepath.Join(t.TempDir(), "Example")
	source := filepath.Join(folder, "Example.2026.mkv")
	const pieceExp = 15
	writeTestFile(t, source, strings.Repeat("x", 1<<pieceExp))
	path := filepath.Join(t.TempDir(), "wrapper.torrent")
	if _, err := mkbrr.Create(mkbrr.CreateOptions{
		Path:           folder,
		OutputPath:     path,
		PieceLengthExp: new(uint(pieceExp)),
		IsPrivate:      true,
		Quiet:          true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		wantStatus string
		wantErr    error
	}{
		{name: "completed", wantStatus: "completed"},
		{
			name:       "canceled during verification",
			wantStatus: "canceled",
			wantErr:    context.Canceled,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var mu sync.Mutex
			var verified bool
			open := make(map[string]bool)
			terminals := make(map[string][]string)
			ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
				if update.Task != "torrent" {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if update.Status == "running" {
					open[update.Tracker] = true
					if update.Tracker == "TL" && update.Message == "Verifying torrent pieces" {
						verified = true
						if tc.wantErr != nil {
							cancel()
						}
					}
					return
				}
				terminals[update.Tracker] = append(terminals[update.Tracker], update.Status)
				delete(open, update.Tracker)
			})
			out, err := NewService(nil, t.TempDir()).Create(ctx, api.TorrentSubject{
				SourcePath:        source,
				ClientTorrentPath: path,
				Trackers:          []string{"TL", "BHD"},
				ForceRecheck:      true,
				TorrentOverrides:  api.TorrentOverrides{NoHash: new(true)},
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Create error = %v, want %v", err, tc.wantErr)
			}
			if !verified || len(open) != 0 || !slices.Equal(terminals["TL"], []string{tc.wantStatus}) || !slices.Equal(terminals[""], []string{tc.wantStatus}) {
				t.Fatalf("aligned wrapper progress not closed: verified=%t open=%v terminals=%v", verified, open, terminals)
			}
			if tc.wantErr == nil && len(out.TrackerArtifacts) != 2 {
				t.Fatalf("expected both trackers to reuse the wrapper conversion: %+v", out)
			}
		})
	}
}

func TestCreateProgressAndCandidateRejectionDiagnostics(t *testing.T) {
	t.Parallel()

	t.Run("generation without candidates skips reuse check progress and nonexistent candidate noise", func(t *testing.T) {
		t.Parallel()
		source := filepath.Join(t.TempDir(), "Example.2026.mkv")
		writeTestFile(t, source, "synthetic media bytes")

		logger := &recordingDebugLogger{}
		var messages []string
		ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
			messages = append(messages, update.Message)
		})
		_, err := NewService(logger, t.TempDir()).Create(ctx, api.TorrentSubject{
			SourcePath: source,
			Trackers:   []string{"TL"},
		})
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		if slices.Contains(messages, "Checking reusable torrent") {
			t.Fatalf("unexpected reuse-check progress when no candidates exist: %v", messages)
		}
		if !slices.Contains(messages, "Preparing torrent") {
			t.Fatalf("expected Preparing torrent progress, got %v", messages)
		}
		if !slices.Contains(messages, "Hashing torrent: no compatible reusable artifact") {
			t.Fatalf("expected non-rehash generation reason, got %v", messages)
		}
		for _, line := range logger.snapshot() {
			if strings.Contains(line, "reusable candidate rejected") && strings.Contains(line, "stage=content_layout") {
				t.Fatalf("unexpected nonexistent candidate rejection log: %s", line)
			}
		}
	})

	t.Run("piece-count rejection logs debug reason and terminates skipped nohash scope", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		source := filepath.Join(dir, "Pack.2026.mkv")
		const (
			piece int64 = 16 << 10
			count int64 = 10001
			size        = piece * count
		)
		writeSizedTorrentSource(t, source, size)
		candidate := writeExtractionTorrent(t, filepath.Join(dir, "existing.torrent"), metainfo.Info{
			Name:        filepath.Base(source),
			Length:      size,
			PieceLength: piece,
			Pieces:      make([]byte, count*metainfo.HashSize),
		})

		logger := &recordingDebugLogger{}
		var updates []api.UploadProgressUpdate
		ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
			updates = append(updates, update)
		})
		result, err := NewService(logger, t.TempDir()).Create(ctx, api.TorrentSubject{
			SourcePath:              source,
			SourceSize:              size,
			Trackers:                []string{"TL"},
			ClientTorrentCandidates: []api.ClientTorrentCandidate{candidate},
			TorrentOverrides:        api.TorrentOverrides{NoHash: new(true)},
		})
		if err != nil {
			t.Fatalf("expected skipped nohash result, got err=%v", err)
		}
		if !slices.Equal(result.SkippedTrackers, []string{"TL"}) {
			t.Fatalf("expected TL skipped under nohash, got %+v", result)
		}
		if !slices.ContainsFunc(updates, func(u api.UploadProgressUpdate) bool {
			return u.Tracker == "TL" && u.Status == "running" && u.Message == "Checking reusable torrent"
		}) {
			t.Fatalf("expected Checking reusable torrent progress when candidates exist, got %+v", updates)
		}
		last := updates[len(updates)-1]
		if last.Tracker != "TL" || last.Status != "skipped" {
			t.Fatalf("expected terminal skipped progress for TL, got %+v", last)
		}
		loggedPieceCount := slices.ContainsFunc(logger.snapshot(), func(line string) bool {
			return strings.Contains(line, "reusable candidate rejected") && strings.Contains(line, "stage=piece_count")
		})
		if !loggedPieceCount {
			t.Fatalf("expected stage=piece_count rejection debug log, got %v", logger.snapshot())
		}
	})

	t.Run("defer wrapper conversion reuse hit emits terminal completed progress", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		source := filepath.Join(dir, "Reusable.2026.mkv")
		writeTestFile(t, source, "reusable bytes")
		candidate := filepath.Join(dir, "reusable.torrent")
		createTestTorrentFromExisting(t, source, candidate)

		var updates []api.UploadProgressUpdate
		ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
			updates = append(updates, update)
		})
		result, err := NewService(api.NopLogger{}, t.TempDir()).Create(ctx, api.TorrentSubject{
			SourcePath:             source,
			Trackers:               []string{"TL"},
			ClientTorrentPath:      candidate,
			DeferWrapperConversion: true,
			TorrentOverrides:       api.TorrentOverrides{NoHash: new(true)},
		})
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
		if result.PendingPublication != nil {
			t.Fatalf("expected no pending publication for reuse hit")
		}
		if len(updates) < 2 || updates[len(updates)-1].Status != "completed" || updates[len(updates)-1].Tracker != "TL" {
			t.Fatalf("expected completed terminal progress for DeferWrapperConversion reuse hit, got %+v", updates)
		}
	})

	t.Run("two-tracker generation closes shared check and per-tracker scopes", func(t *testing.T) {
		t.Parallel()
		for _, deferred := range []bool{false, true} {
			t.Run(fmt.Sprintf("deferred=%t", deferred), func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				source := filepath.Join(dir, "Multi.2026.mkv")
				writeTestFile(t, source, "synthetic media bytes")
				other := filepath.Join(dir, "Other.2026.mkv")
				writeTestFile(t, other, "other bytes")
				incompatible := filepath.Join(dir, "incompatible.torrent")
				createTestTorrentFromExisting(t, other, incompatible)

				lastStatus := make(map[string]string)
				ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
					lastStatus[update.Tracker] = update.Status
				})
				result, err := NewService(api.NopLogger{}, t.TempDir()).Create(ctx, api.TorrentSubject{
					SourcePath:                 source,
					Trackers:                   []string{"TL", "BHD"},
					ClientTorrentPath:          incompatible,
					DeferGenerationPublication: deferred,
				})
				if err != nil {
					t.Fatalf("Create failed: %v", err)
				}
				if result.PendingPublication != nil {
					defer func() { _ = result.PendingPublication.Discard() }()
				}
				if lastStatus[""] != "completed" {
					t.Fatalf("expected shared torrent check scope to complete before/after generation, got %+v", lastStatus)
				}
				if deferred {
					if result.PendingPublication == nil {
						t.Fatalf("expected pending publication when deferred=true")
					}
					if lastStatus["TL"] != "running" {
						t.Fatalf("expected deferred TL scope to remain running until caller publishes, got %+v", lastStatus)
					}
					if !slices.Contains(result.RehashedTrackers, "TL") {
						t.Fatalf("expected TL in RehashedTrackers for caller handoff, got %v", result.RehashedTrackers)
					}
				} else {
					for tracker, status := range lastStatus {
						if status != "completed" {
							t.Fatalf("scope %q stuck in %q after immediate publication: %+v", tracker, status, lastStatus)
						}
					}
				}
			})
		}
	})

	t.Run("direct provided torrent failure and cancellation emit terminal status", func(t *testing.T) {
		t.Parallel()
		t.Run("validation failure", func(t *testing.T) {
			t.Parallel()
			missingTorrent := filepath.Join(t.TempDir(), "missing.torrent")
			var updates []api.UploadProgressUpdate
			ctx := api.WithUploadProgressReporter(t.Context(), func(update api.UploadProgressUpdate) {
				updates = append(updates, update)
			})
			if _, err := NewService(api.NopLogger{}, t.TempDir()).Create(ctx, api.TorrentSubject{
				SourcePath: missingTorrent,
				Trackers:   []string{"TL"},
			}); err == nil {
				t.Fatal("expected missing .torrent to fail")
			}
			if len(updates) < 2 || updates[0].Status != "running" || updates[len(updates)-1].Status != "failed" || updates[len(updates)-1].Tracker != "TL" {
				t.Fatalf("expected running then failed progress for missing .torrent, got %+v", updates)
			}
		})

		t.Run("cancellation during validation", func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			content := filepath.Join(dir, "video.mkv")
			provided := filepath.Join(dir, "provided.torrent")
			createTestTorrent(t, content, provided)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var updates []api.UploadProgressUpdate
			ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
				updates = append(updates, update)
				if update.Status == "running" {
					cancel()
				}
			})
			if _, err := NewService(api.NopLogger{}, t.TempDir()).Create(ctx, api.TorrentSubject{
				SourcePath: provided,
				Trackers:   []string{"TL"},
			}); err == nil {
				t.Fatal("expected canceled .torrent validation to fail")
			}
			if len(updates) < 2 || updates[len(updates)-1].Status != "canceled" || updates[len(updates)-1].Tracker != "TL" {
				t.Fatalf("expected terminal canceled status for direct .torrent, got %+v", updates)
			}
		})
	})

	for _, tc := range []struct {
		tracker      string
		piece, count int64
		stage        string
	}{
		{
			tracker: "TL",
			piece:   16 << 10,
			count:   10001,
			stage:   "piece_count",
		},
		{
			tracker: "HDB",
			piece:   2 << 20,
			count:   4001,
			stage:   "tracker_policy",
		},
	} {
		t.Run("wrapper extraction logs rejection at "+tc.stage, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			source := filepath.Join(dir, "Wrapper.2026.mkv")
			size := tc.piece * tc.count
			writeSizedTorrentSource(t, source, size)
			wrapper := writeExtractionTorrent(t, filepath.Join(dir, "wrapper.torrent"), metainfo.Info{
				Name:        "Wrapper.2026.Folder",
				PieceLength: tc.piece,
				Pieces:      make([]byte, tc.count*metainfo.HashSize),
				Files:       []metainfo.FileInfo{{Length: size, Path: []string{filepath.Base(source)}}},
			})
			registry := trackers.NewRegistry()
			if err := registry.Register(hdb.New()); err != nil {
				t.Fatal(err)
			}
			logger := &recordingDebugLogger{}
			_, err := NewServiceWithRegistry(logger, t.TempDir(), registry).Create(t.Context(), api.TorrentSubject{
				SourcePath:              source,
				SourceSize:              size,
				Trackers:                []string{tc.tracker},
				ClientTorrentCandidates: []api.ClientTorrentCandidate{wrapper},
				TorrentOverrides:        api.TorrentOverrides{NoHash: new(true)},
			})
			if err != nil {
				t.Fatalf("expected skipped nohash result, got err=%v", err)
			}
			if !slices.ContainsFunc(logger.snapshot(), func(line string) bool {
				return strings.Contains(line, "reusable candidate rejected") && strings.Contains(line, "stage="+tc.stage)
			}) {
				t.Fatalf("expected wrapper rejection at stage %s, got %v", tc.stage, logger.snapshot())
			}
		})
	}
}

func writeSizedTorrentSource(t *testing.T, path string, size int64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	truncateErr := file.Truncate(size)
	if err := errors.Join(truncateErr, file.Close()); err != nil {
		t.Fatal(err)
	}
}
