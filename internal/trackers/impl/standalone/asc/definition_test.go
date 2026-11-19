// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/autobrr/go-torrent/bencode"
	"github.com/autobrr/go-torrent/metainfo"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/trackers"
	"github.com/autobrr/upbrr/pkg/api"
)

func prepareDryRun(ctx context.Context, input trackers.PreparationInput) (api.TrackerDryRunEntry, error) {
	input.Intent = trackers.PreparationIntentDryRun
	plan, failure := New().Prepare(ctx, input)
	if failure != nil {
		return api.TrackerDryRunEntry{}, failure
	}
	return plan.DryRun(), nil
}

const testCookieFileName = "ASC.txt"

func TestASCPreflightRequiresImportedSession(t *testing.T) {
	t.Parallel()
	resolver := Profile().AuthResolver
	if resolver == nil {
		t.Fatal("ASC upload preflight has no session resolver")
	}
	err := resolver(t.Context(), config.TrackerConfig{}, filepath.Join(t.TempDir(), "upbrr.db"), api.TrackerAuthLoginRequest{})
	failure, ok := errors.AsType[*trackers.AuthResolutionError](err)
	if !ok || !failure.AuthRequired || failure.ConfirmedInvalid {
		t.Fatalf("missing ASC cookies returned %v", err)
	}
}

func TestASCMigratedUploadDryRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cookieDir := filepath.Join(dir, "cookies")
	if err := os.Mkdir(cookieDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cookieDir, testCookieFileName), "amigos-share.club\tFALSE\t/\tTRUE\t0\tasc-session\tsynthetic\n")
	const stem = "Synthetic.Release.2026.1080p.WEB-DL.DDP5.1.H.264-GRP"
	content, err := bencode.Marshal(metainfo.Info{
		Name:        stem,
		PieceLength: 16384,
		Pieces:      make([]byte, 20),
		Files: []metainfo.FileInfo{
			{Path: []string{stem + ".mkv"}, Length: 1},
			{Path: []string{stem + ".pt-BR.srt"}, Length: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	torrent := filepath.Join(dir, "synthetic.torrent")
	f, err := os.Create(torrent)
	if err != nil {
		t.Fatal(err)
	}
	writeErr := (&metainfo.MetaInfo{InfoBytes: content}).Write(f)
	if err := errors.Join(writeErr, f.Close()); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(dir, "mediainfo.txt")
	reportText := "General\nComplete name : " + filepath.Join(dir, stem+".mkv")
	write(report, reportText)
	if cleaned := ascUploadMediaInfo(reportText); cleaned != "General\nComplete name : "+stem+".mkv" {
		t.Fatal("MediaInfo retained a local path")
	}
	images := make([]api.ScreenshotImage, 0, 2)
	for _, name := range []string{"a.png", "b.png"} {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		writeErr := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 1, 1)))
		if err := errors.Join(writeErr, f.Close()); err != nil {
			t.Fatal(err)
		}
		images = append(images, api.ScreenshotImage{Path: path})
	}
	req := trackers.PreparationInput{
		Intent:  trackers.PreparationIntentDryRun,
		Tracker: "ASC",
		Runtime: trackers.PreparationRuntime{DBPath: filepath.Join(dir, "upbrr.db")},
		Logger:  api.NopLogger{},
		Assets:  &trackers.DescriptionAssets{Screenshots: images},
		Meta: api.UploadSubject{
			SourcePath:        filepath.Join(dir, stem),
			TorrentPath:       torrent,
			MediaInfoTextPath: report,
			Identity:          api.ExternalIdentity{Category: api.CanonicalCategoryMovie, IMDBID: 1234567},
			Release: api.ReleaseInfo{
				Title:      "Synthetic Release",
				Year:       2026,
				Resolution: "1080p",
			},
			Type:       "WEBDL",
			Container:  "mkv",
			Audio:      "DD+ 5.1",
			VideoCodec: "AVC",
			ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
				Poster:           "https://image.tmdb.org/t/p/original/synthetic.jpg",
				Overview:         "Synthetic overview",
				Genres:           "Drama",
				OriginalLanguage: "en",
			}},
		},
	}
	plan, failure := New().Prepare(t.Context(), req)
	if failure != nil {
		t.Fatal(failure)
	}
	preview := plan.DryRun()
	if preview.Status != "ready" || preview.Endpoint != "https://amigos-share.club/torrents" ||
		preview.Payload["category_id"] != "4" || len(preview.Files) != 4 || hasForeignImages(preview.Description) {
		t.Fatalf("unexpected preview status=%s message=%s files=%d", preview.Status, preview.Message, len(preview.Files))
	}
	if _, err := plan.Submit(t.Context()); !errors.Is(err, trackers.ErrPlanNotSubmittable) {
		t.Fatalf("dry-run plan was submittable: %v", err)
	}
	preserved, err := metainfo.LoadFromFile(torrent)
	if err != nil || !bytes.Equal(preserved.InfoBytes, content) {
		t.Fatal("dry run changed the torrent payload containing the external subtitle")
	}
	req.Intent, req.ExecutionMode = trackers.PreparationIntentUpload, api.WorkflowExecutionModeDebug
	debug, failure := New().Prepare(t.Context(), req)
	if failure != nil {
		t.Fatal(failure)
	}
	if _, err := debug.Submit(t.Context()); !errors.Is(err, trackers.ErrPlanNotSubmittable) {
		t.Fatalf("debug plan was submittable: %v", err)
	}
	req.ExecutionMode = ""
	req.Assets = &trackers.DescriptionAssets{Screenshots: images[:1]}
	blocked, err := prepareDryRun(t.Context(), req)
	if err != nil || blocked.Status != "blocked" || !strings.Contains(blocked.Message, "at least 2 local screenshots") {
		t.Fatalf("missing screenshot status=%s message=%s err=%v", blocked.Status, blocked.Message, err)
	}
}

func TestDefinitionBuildUploadDryRunBlockedWithoutCookies(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	torrentPath := filepath.Join(tmp, "release.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	entry, err := prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "ASC",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "movie.mkv"),
			TorrentPath: torrentPath,
			Release: api.ReleaseInfo{
				Title:      "Movie",
				Year:       2024,
				Resolution: "1080p",
			},
			Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 1234567},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{
					Poster:   "https://img/poster.jpg",
					Overview: "Overview",
					Genres:   "Drama",
				},
				IMDB: &api.IMDBMetadata{IMDbIDText: "tt1234567"},
			},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")},
		}),
		Logger: api.NopLogger{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", entry.Status)
	}
}

func TestDefinitionBuildUploadDryRunRejectsMissingQuestionnaireMetadata(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cookieDir := filepath.Join(tmp, "cookies")
	if err := os.MkdirAll(cookieDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := "# Netscape HTTP Cookie File\n.cliente.amigos-share.club\tTRUE\t/\tTRUE\t0\tsession\tcookievalue\n"
	if err := os.WriteFile(filepath.Join(cookieDir, testCookieFileName), []byte(content), 0o600); err != nil {
		t.Fatalf("write cookie: %v", err)
	}
	torrentPath := filepath.Join(tmp, "release.torrent")
	if err := os.WriteFile(torrentPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	_, err := prepareDryRun(context.Background(), trackers.PreparationInput{
		Tracker: "ASC",
		Meta: api.UploadSubject{
			SourcePath:  filepath.Join(tmp, "movie.mkv"),
			TorrentPath: torrentPath,
			Release: api.ReleaseInfo{
				Title:      "Movie",
				Year:       2024,
				Resolution: "1080p",
			},
			Identity: api.ExternalIdentity{Category: "MOVIE", IMDBID: 1234567},
			ProviderMetadata: api.SourceScopedMetadata{
				TMDB: &api.TMDBMetadata{Poster: "https://img/poster.jpg"},
				IMDB: &api.IMDBMetadata{IMDbIDText: "tt1234567"},
			},
		},
		Runtime: trackers.PreparationRuntimeFromConfig(config.Config{
			MainSettings: config.MainSettingsConfig{DBPath: filepath.Join(tmp, "ua.db")},
		}),
		Logger: api.NopLogger{},
	})
	if err == nil || !strings.Contains(err.Error(), "required_genre") {
		t.Fatalf("expected direct dry-run constructibility error, got %v", err)
	}
}

func TestBuildQuestionnaireForMissingMetadata(t *testing.T) {
	t.Parallel()

	questionnaire := buildQuestionnaire(api.UploadSubject{})
	if questionnaire == nil {
		t.Fatal("expected questionnaire")
	}
	if got := len(questionnaire.Fields); got != 2 {
		t.Fatalf("expected 2 questionnaire fields, got %d", got)
	}
}
