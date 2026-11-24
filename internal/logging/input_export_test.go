// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package logging

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestInputLogExportAcrossRotation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	root := t.TempDir()
	database := filepath.Join(root, "upbrr.db")
	repo, err := db.OpenContext(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err := repo.MigrateContext(ctx); err != nil {
		t.Fatal(err)
	}
	logger, err := New(config.LoggingConfig{
		Level:          "warn",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       8,
	}, database)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	logger.SetConsoleOutput(io.Discard, io.Discard)
	writer, ok := logger.closer.(*rotatingWriter)
	if !ok {
		t.Fatal("expected the rotating file sink")
	}
	writer.maxBytes = 235
	source := filepath.Join(root, "original", "Example.mkv")
	other := filepath.Join(root, "other", "Example.mkv")
	first, id, finish := logger.BeginInputLog(ctx, repo, source, "open input")
	canonical := filepath.Join(root, "verified", "Example.mkv")
	_, nestedID, bindVerified := logger.BeginInputLog(first, repo, source, "verify")
	if nestedID != id {
		t.Fatal("nested verification opened an independent attempt")
	}
	bindVerified(canonical, "completed")
	second, otherID, otherFinish := logger.BeginInputLog(ctx, repo, other, "open input")
	FromContext(first, logger).Tracef("first detail\ncontinued api_key=example-secret")
	FromContext(second, logger).Tracef("not this input")
	// Application level overrides must not discard ownership or disk TRACE.
	view, err := NewOperationLogger(logger, "error")
	if err != nil {
		t.Fatal(err)
	}
	FromContext(WithOperationLogger(first, view), logger).Tracef("last detail")
	otherFinish("", "completed")
	finish("", "completed")
	if id == "" || id == otherID {
		t.Fatal("processing attempts did not get distinct IDs")
	}
	if len(logger.Recent(0)) != 0 {
		t.Fatal("file TRACE raised the application threshold")
	}

	attempts, err := db.ReadOnlyInputLogs(ctx, database, source)
	if err != nil || len(attempts) != 1 || attempts[0].ID != id || attempts[0].SourcePath != canonical || attempts[0].Build != InputLogBuild() {
		t.Fatalf("attempt lookup: %v, %v", attempts, err)
	}
	restarted, err := ReadInputLog(ctx, nil, database, attempts[0])
	if err != nil || restarted.Coverage != "complete" {
		t.Fatalf("restart export coverage: %s, %v", restarted.Coverage, err)
	}
	start, end := strings.Index(restarted.Text, "first detail\ncontinued"), strings.Index(restarted.Text, "last detail")
	if start < 0 || end <= start || strings.Contains(restarted.Text, "not this input") ||
		strings.Contains(restarted.Text, "example-secret") || !strings.Contains(restarted.Text, "[REDACTED]") ||
		!strings.Contains(restarted.Text, "Build: "+attempts[0].Build) || !strings.Contains(restarted.Text, "Input: Example.mkv\n") {
		t.Fatal("export did not preserve order, isolation and redaction")
	}
	rotated, err := filepath.Glob(writer.path + ".[0-9]*")
	if err != nil || len(rotated) < 2 {
		t.Fatalf("fixtures did not rotate: %v, %v", rotated, err)
	}
	if err := os.Remove(rotated[len(rotated)-1]); err != nil {
		t.Fatal(err)
	}
	partial, err := ReadInputLog(ctx, nil, database, attempts[0])
	if err != nil || partial.Coverage != "partial" {
		t.Fatalf("rotation gap was not reported: %s, %v", partial.Coverage, err)
	}
}

func TestInputLogExportSanitizesFreshAndLegacyDiagnostics(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"memory", "disk", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			database := filepath.Join(t.TempDir(), "private-config", "upbrr.db")
			artifact := filepath.Join(filepath.Dir(database), "tmp", "Example.Release")
			// These are diagnostic strings from both platforms, not local filesystem paths.
			source := `D:\Private Media, Family (Archive)\Example Release.mkv`
			details := []string{
				fmt.Sprintf("preparing %s (phase=metadata)", source),
				fmt.Sprintf("metadata: external ids start path=%q category=%q", source, "movies"),
				`metadata: external ids start path="/media/Private, Family/Collection/Example.mkv" category="movies"`,
				`preparing /media/Private, Family/Collection/Example.mkv (phase=metadata)`,
				fmt.Sprintf("mediainfo: checking cache at %s (text=false json=false)", artifact),
				`clients: match seeders=847 tracker_ids="EXAMPLE:791223" save_path="D:\\Private Client\\Destination" content_path="D:\\Private Client\\Example Release.mkv"`,
				`metadata: torrent Example trackers=[https://tracker.invalid/announce] extracted_ids=[{example 791223}] comment="https://tracker.invalid/view/791223"`,
				fmt.Sprintf("metadata: pathed tracker ids for %s: EXAMPLE=791223", source),
				`screenshots: capture setup ffmpeg=C:\Tools Private\bin\ffmpeg.exe selected_path=/mnt/private library/Example Release.mkv`,
				`trackers: EXAMPLE torrent URL: https://tracker.invalid/view/791223`,
				`trackers: EXAMPLE upload response body: {"id":791223,"group_id":"791223","status":"accepted"}`,
				"trackers: EXAMPLE response body: {\n  \"id\": 791223,\n  \"status\": \"incomplete\"",
				`metadata: tracker lookup empty tracker=EXAMPLE id="791223"`,
				`unit3d: EXAMPLE response contained no attributes (id="791223" file="Example.mkv")`,
				`ASC filename request failed for ID 791223: synthetic failure`,
				`dupechecking: candidate tracker=EXAMPLE candidate_id="791223" relation=same_slot`,
				`dupechecking: set tracker=EXAMPLE candidates="791223 | 791224" facts="candidate[id=791223,size=123,kind=encode]" missing="candidate=791224:hdr"`,
				`tmdb: metadata loaded id=172233 title="Example"`,
				`tracker response location=https://tracker.invalid/view/791223`,
				`qbit: options save_path=/remote/Private Client path content_path=/remote/library/File.mkv`,
				`qbit: options {SavePath:D:\Private Client data=Folder\Hidden Destination}`,
				`clients: linked example save_path=C:/Incoming/{Archive}/Private Destination/`,
				`clients: injection input source=D:\Media Library Collection=Archive\Private Group\File.mkv tracker=EXAMPLE has_file=true has_url=false configured_clients=1`,
				`metadata: normalized path D:\Library Collection=Archive\Private Group\File.mkv`,
				`mediainfo: analyzing /media/Bracketed Library [2024]/Private Group/Example.mkv`,
				`preparing D:\Bracketed Library [2024]\Private Group\Example.mkv (phase=metadata)`,
				`preparing \Root Relative Library\Rooted.mkv (phase=metadata)`,
				`literal escapes: \n\t and \r`,
				`mediainfo: analyzing /media/Show {tvdb-123}/Season 01/File.mkv`,
				"mediainfo: analyzing " + filepath.Join(filepath.Dir(database), "private collection", "season", "File.mkv"),
				`credentials api_key=synthetic-api passkey=synthetic-passkey hash=0123456789abcdef0123456789abcdef01234567`,
				"request failed\nAuthorization: Bearer synthetic-bearer\nCookie: session=synthetic-cookie\nhttps://synthetic-user:synthetic-password@tracker.invalid/0123456789abcdef/announce",
				`dupechecking: comparison target={720p} candidate={1080p} result=different`,
				`SPD details https://tracker.invalid/browse/791223`,
				`analyzing /media/private library/File.mkv (1080p/24)`,
				`notice: copying \\private-server\private-share\Folder\File.mkv`,
				`workflow: dry-run tracker payload tracker=CZT payload={"fields":{"mediainfo":"General\nComplete name : /mnt/private library/Example Release.mkv\nFormat : Matroska","windows":"path=C:\\Private Media\\File.mkv\nother evidence","keywords":"comedy,mystery","user_descr":"Example description password=synthetic-description-secret","description":"Title / Subtitle [url=https://tracker.invalid/view/791223]Link[/url]\n[url=https://tracker.invalid/torrents.php?id=791223]Details[/url]\n[url=https://tracker.invalid/torrents-details.php?id=791223]Alternate[/url]\n[url=https://tracker.invalid/browse/t/[791223]]Bracketed[/url]\n[img=350]https://image.tmdb.org/t/p/w300/Ⱥ.png[/img]\n[url]https://thetvdb.com/?tab=series&id=172233[/url]"},"files":{"torrent":true}}`,
			}
			logger, err := New(config.LoggingConfig{
				Level:          "warn",
				FileEnabled:    mode != "memory",
				MaxTotalSizeMB: 1,
				MaxFiles:       2,
			}, database)
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			stored := &recordingAttempts{}
			if mode == "legacy" {
				details = append([]string{"trackers: EXAMPLE response body: <html>\n<meta name=\"csrf-token\" content=\"synthetic-hidden-form-value\">\n</html>"}, details...)
				at := time.Now()
				const id = "legacy"
				stored.attempts = []db.InputLogAttempt{{
					ID:         id,
					SourcePath: source,
					StartedAt:  at,
					Outcome:    "completed",
				}}
				logger.file.Printf("INFO: [input-log %s 1 begin %d] begin", id, at.UnixNano())
				for i, detail := range details {
					logger.file.Printf("TRACE: [input-log %s %d record %d] %s", id, i+2, at.UnixNano()+int64(i), diskLogMessage(detail))
				}
				logger.file.Printf("INFO: [input-log %s %d end %d] end", id, len(details)+2, at.UnixNano())
			} else {
				ctx, _, finish := logger.BeginInputLog(t.Context(), stored, source, "prepare")
				for _, detail := range details {
					FromContext(ctx, nil).Tracef("%s", detail)
				}
				finish("", "completed")
			}
			if mode != "memory" {
				logger = nil
			}
			result, err := ReadInputLog(t.Context(), logger, database, stored.attempts[0])
			if err != nil {
				t.Fatal(err)
			}
			for i, private := range []string{"Private Media", "Private, Family", "Family/Collection", "Private Client", "private library", "Tools Private",
				"private-config", "private-server", "private-share", "Hidden Destination", "Private Destination", "Private Group", "Collection=Archive",
				"private collection", "Root Relative Library", "Bracketed Library", "Library Collection", "Season 01", "tvdb-123", "synthetic-api", "synthetic-passkey", "synthetic-bearer",
				"synthetic-cookie", "synthetic-hidden-form-value", "synthetic-description-secret", "synthetic-user", "synthetic-password", "0123456789abcdef",
				"791223", "791224", "847"} {
				if strings.Contains(result.Text, private) {
					t.Errorf("retained private fixture value %d", i)
				}
			}
			for _, detail := range []string{"preparing Example Release.mkv (phase=metadata)", "{db}/tmp/Example.Release (text=false json=false)",
				"content_path=\"Example Release.mkv\"", "ffmpeg=ffmpeg.exe selected_path=Example Release.mkv", "copying File.mkv",
				"analyzing File.mkv (1080p/24)", "save_path=[REDACTED] content_path=File.mkv", `path="Example.mkv" category="movies"`,
				"preparing Rooted.mkv (phase=metadata)", `literal escapes: \n\t and \r`, "tmdb: metadata loaded id=172233", "candidate[id=[REDACTED],size=123,kind=encode]",
				"candidate={1080p}", "injection input source=File.mkv tracker=EXAMPLE", "mediainfo: analyzing File.mkv", "linked example save_path=[REDACTED]",
				"metadata: normalized path File.mkv", "mediainfo: analyzing Example.mkv", "preparing Example.mkv (phase=metadata)"} {
				if !strings.Contains(result.Text, detail) {
					t.Errorf("export missing expected diagnostic %q", detail)
				}
			}
			_, payload, _ := strings.Cut(result.Text, "payload=")
			var preview struct {
				Fields map[string]string `json:"fields"`
				Files  map[string]bool   `json:"files"`
			}
			if err := json.Unmarshal([]byte(payload), &preview); err != nil {
				t.Fatalf("invalid retained payload: %v\n%s", err, payload)
			}
			if preview.Fields["mediainfo"] != "General\nComplete name : Example Release.mkv\nFormat : Matroska" ||
				preview.Fields["windows"] != "path=File.mkv\nother evidence" ||
				preview.Fields["keywords"] != "comedy,mystery" ||
				preview.Fields["user_descr"] != "Example description password=[REDACTED]" ||
				preview.Fields["description"] != "Title / Subtitle [url=https://tracker.invalid/[REDACTED]]Link[/url]\n"+
					"[url=https://tracker.invalid/[REDACTED]]Details[/url]\n[url=https://tracker.invalid/[REDACTED]]Alternate[/url]\n"+
					"[url=https://tracker.invalid/[REDACTED]]Bracketed[/url]\n[img=350]https://image.tmdb.org/t/p/w300/%C8%BA.png[/img]\n"+
					"[url]https://thetvdb.com/?id=172233&tab=series[/url]" ||
				!preview.Files["torrent"] {
				t.Fatalf("retained payload changed valid diagnostics: %#v", preview)
			}
		})
	}
}

func TestInputLogPRBuildLabelRetainsRevision(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"abcdef123456", "fedcba654321"} {
		info := api.ApplicationInfo{Version: "pr123", BuildIdentifier: id}
		if got := inputLogBuildLabel(info); got != "pr123 ("+id+")" {
			t.Errorf("input log build = %q, missing PR revision %q", got, id)
		}
	}
}

func TestInputLogPOSIXDBPathWithNoRequestedSource(t *testing.T) {
	t.Parallel()

	const root = "/config/upbrr"
	const message = "mediainfo: checking cache at /config/upbrr/tmp/Example (text=false json=false)"
	got := sanitizeInputLog(message, root, "/media/Example.mkv", "")
	if got != "mediainfo: checking cache at {db}/tmp/Example (text=false json=false)" {
		t.Fatal("expected database-relative diagnostic with an empty requested path")
	}
}

func TestInputLogCaptureUsesDefaultDatabaseRoot(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	database, err := db.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	logger, err := New(config.LoggingConfig{Level: "warn"}, "")
	if err != nil {
		t.Fatal(err)
	}
	stored := &recordingAttempts{}
	ctx, _, finish := logger.BeginInputLog(t.Context(), stored, "Example.mkv", "prepare")
	FromContext(ctx, nil).Tracef("cache file=%q", filepath.Join(filepath.Dir(database), "tmp", "Example", "mediainfo.txt"))
	finish("", "completed")
	exported, err := ReadInputLog(t.Context(), logger, "", stored.attempts[0])
	if err != nil || !strings.Contains(exported.Text, `file="{db}/tmp/Example/mediainfo.txt"`) {
		t.Fatalf("default database root was lost: %v\n%s", err, exported.Text)
	}
}

func TestInputLogRedactsRelativeDatabaseDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	database := filepath.Join("PrivateState", "upbrr.db")
	logger, err := New(config.LoggingConfig{
		Level:          "warn",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       2,
	}, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	store := &recordingAttempts{}
	ctx, _, finish := logger.BeginInputLog(t.Context(), store, "Example.mkv", "prepare")
	messages := []string{
		fmt.Sprintf("mediainfo: checking cache at %s (text=false json=false)", filepath.Join("PrivateState", "tmp", "Example")),
		"metadata: tracker description saved tracker=EXAMPLE path=" + filepath.Join("PrivateState", "tmp", "Example", "description.txt"),
		"metadata: tracker artifacts tracker=EXAMPLE dir=" + filepath.Join("PrivateState", "tmp", "Example") + " desc=true images=1 keepImages=true",
	}
	for _, message := range messages {
		FromContext(ctx, logger).Tracef("%s", message)
	}
	finish("", "completed")
	logger.file.Print("INFO: [input-log legacy 1 begin] begin")
	for i, message := range messages {
		logger.file.Printf("TRACE: [input-log legacy %d record] %s", i+2, message)
	}
	logger.file.Printf("INFO: [input-log legacy %d end] end", len(messages)+2)
	store.attempts[0].Outcome = "completed"
	legacy := store.attempts[0]
	legacy.ID = "legacy"
	result, err := ReadInputLogs(t.Context(), nil, database, append(store.attempts, legacy))
	if err != nil || result.Coverage != "complete" || strings.Contains(result.Text, "PrivateState") {
		t.Fatalf("relative database export coverage = %q, error = %v, private directory is present = %t", result.Coverage, err, strings.Contains(result.Text, "PrivateState"))
	}
	for _, want := range []string{"{db}/tmp/Example (text=false json=false)", "path={db}/tmp/Example/description.txt", "dir={db}/tmp/Example desc=true"} {
		if strings.Count(result.Text, want) != 2 {
			t.Errorf("expected one fresh and one legacy occurrence of %q", want)
		}
	}
}

func TestInputLogWriterRecoversAfterAnotherWriterRotates(t *testing.T) {
	t.Parallel()
	database := filepath.Join(t.TempDir(), "upbrr.db")
	cfg := config.LoggingConfig{
		Level:          "warn",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       2,
	}
	first, err := New(cfg, database)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := New(cfg, database)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var logPath string
	for _, logger := range []*Logger{first, second} {
		writer, ok := logger.closer.(*rotatingWriter)
		if !ok {
			t.Fatal("expected rotating file sink")
		}
		writer.maxBytes = 1536
		logPath = writer.path
	}
	for range 5 {
		second.Infof("force shared rotation %s", strings.Repeat("x", 950))
	}
	if _, err := os.Stat(logPath + ".2"); err != nil {
		t.Fatalf("second writer did not rotate the shared file: %v", err)
	}
	store := &recordingAttempts{}
	ctx, _, finish := first.BeginInputLog(t.Context(), store, "Example.mkv", "prepare")
	FromContext(ctx, first).Tracef("first writer detail after rotation")
	finish("", "completed")
	attempt := store.attempts[0]
	attempt.Outcome = "completed"
	result, err := ReadInputLog(t.Context(), nil, database, attempt)
	if err != nil || result.Coverage != "complete" || !strings.Contains(result.Text, "first writer detail after rotation") {
		t.Fatalf("shared rotation export: %#v, %v", result, err)
	}
}

func TestInputLogPreviewBudgetDoesNotChangeRetainedCoverage(t *testing.T) {
	t.Parallel()
	database := filepath.Join(t.TempDir(), "upbrr.db")
	logger, err := New(config.LoggingConfig{
		Level:          "warn",
		FileEnabled:    true,
		MaxTotalSizeMB: 2,
		MaxFiles:       2,
	}, database)
	if err != nil {
		t.Fatal(err)
	}
	defer logger.Close()
	store := &recordingAttempts{}
	ctx, _, finish := logger.BeginInputLog(t.Context(), store, "Example.mkv", "prepare")
	log := FromContext(ctx, logger)
	log.Tracef("old detail")
	for range 4 {
		log.Tracef("%s", strings.Repeat("x", inputPreviewLimit/3))
	}
	log.Tracef("new detail")
	finish("", "completed")
	store.attempts[0].Outcome = "completed"
	preview, err := PreviewInputLogs(t.Context(), nil, database, store.attempts)
	if err != nil || preview.Coverage != "complete" || !strings.Contains(preview.Text, "Showing the end of the log") ||
		strings.Contains(preview.Text, "old detail") || !strings.Contains(preview.Text, "new detail") {
		t.Fatalf("preview coverage=%s, bytes=%d, error=%v", preview.Coverage, len(preview.Text), err)
	}
	full, err := ReadInputLogs(t.Context(), nil, database, store.attempts)
	if err != nil || full.Coverage != "complete" || len(full.Text) <= len(preview.Text) || !strings.Contains(full.Text, "old detail") {
		t.Fatalf("full export coverage=%s, bytes=%d, error=%v", full.Coverage, len(full.Text), err)
	}
}

func TestInputLogExportRecoversRotatedRecordsFromMemory(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database := filepath.Join(t.TempDir(), "upbrr.db")
	logger, err := New(config.LoggingConfig{
		Level:          "warn",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       1,
	}, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	writer, ok := logger.closer.(*rotatingWriter)
	if !ok {
		t.Fatal("expected the rotating file sink")
	}
	store := &recordingAttempts{}
	captureCtx, _, finish := logger.BeginInputLog(ctx, store, filepath.Join(filepath.Dir(database), "Example.mkv"), "prepare")
	scoped := FromContext(captureCtx, logger)
	scoped.Tracef("first detail")
	if err := writer.rotateLocked(); err != nil {
		t.Fatal(err)
	}
	scoped.Tracef("second detail")
	finish("", "completed")
	attempt := store.attempts[0]
	attempt.Outcome = "completed"
	disk, err := ReadInputLog(ctx, nil, database, attempt)
	if err != nil || strings.Contains(disk.Text, "first detail") || !strings.Contains(disk.Text, "second detail") {
		t.Fatalf("unexpected disk-only export: %#v, %v", disk, err)
	}
	merged, err := ReadInputLog(ctx, logger, database, attempt)
	first, second := strings.Index(merged.Text, "first detail"), strings.Index(merged.Text, "second detail")
	if err != nil || merged.Coverage != "partial" || first < 0 || second <= first ||
		strings.Count(merged.Text, "first detail") != 1 || strings.Count(merged.Text, "second detail") != 1 {
		t.Fatalf("unexpected merged export: %#v, %v", merged, err)
	}
}

func TestInputLogExportLimitKeepsNewestRecords(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"disk", "memory"} {
		t.Run("older attempt on "+source, func(t *testing.T) {
			fromMemory := source == "memory"
			ctx := t.Context()
			database := filepath.Join(t.TempDir(), "upbrr.db")
			logger, err := New(config.LoggingConfig{
				Level:          "warn",
				FileEnabled:    true,
				MaxTotalSizeMB: 1,
				MaxFiles:       1,
			}, database)
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			source := filepath.Join(filepath.Dir(database), "Example.mkv")
			store := &recordingAttempts{}
			old, _, finishOld := logger.BeginInputLog(ctx, store, source, "preparation")
			FromContext(old, logger).Tracef("old detail %s", strings.Repeat("o", 80))
			finishOld("", "completed")
			if fromMemory {
				writer, ok := logger.closer.(*rotatingWriter)
				if !ok {
					t.Fatal("expected rotating log writer")
				}
				if err := writer.rotateLocked(); err != nil {
					t.Fatal(err)
				}
				// Put the earlier captured entry in a different timestamp second
				// without depending on a sleep or the time the test starts.
				logger.inputLogs.entries[0].Time = time.Now().Add(-time.Hour)
			}
			current, _, finishCurrent := logger.BeginInputLog(ctx, store, source, "upload")
			FromContext(current, logger).Tracef("latest failure detail")
			finishCurrent("", "failed")
			store.attempts[1].Outcome = "failed"
			attempts := []db.InputLogAttempt{store.attempts[1], store.attempts[0]}
			if fromMemory {
				full, err := ReadInputLogs(ctx, logger, database, attempts)
				if err != nil || !strings.Contains(full.Text, "Coverage: memory") || !strings.Contains(full.Text, "old detail") {
					t.Fatalf("earlier attempt did not use the memory fallback: %#v, %v", full, err)
				}
			}
			data, err := readInputLogs(ctx, logger, database, attempts, 120, false)
			if err != nil || data.Coverage != "partial" || !strings.Contains(data.Text, "latest failure detail") ||
				strings.Contains(data.Text, "old detail") {
				t.Fatalf("bounded export = %#v, %v", data, err)
			}
		})
	}
}

func TestInputLogExportOrdersMemoryAfterOlderDiskInSameSecond(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name                  string
		diskOffset            time.Duration
		legacy, prefersMemory bool
		trimDisk              bool
	}{
		{
			name:          "newer memory",
			diskOffset:    100 * time.Millisecond,
			prefersMemory: true,
		},
		{name: "newer disk", diskOffset: 950 * time.Millisecond},
		{
			name:          "legacy disk",
			legacy:        true,
			prefersMemory: true,
		},
		{
			name:          "legacy disk trimmed before merge",
			legacy:        true,
			prefersMemory: true,
			trimDisk:      true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			database := filepath.Join(t.TempDir(), "upbrr.db")
			source := filepath.Join(filepath.Dir(database), "Example.mkv")
			logger, err := New(config.LoggingConfig{Level: "warn"}, database)
			if err != nil {
				t.Fatal(err)
			}
			logPath, err := LogPath(database)
			if err != nil {
				t.Fatal(err)
			}
			// Use the persisted format from before and after precise markers.
			second := time.Date(2026, 10, 5, 12, 1, 2, 0, time.Local)
			at := ""
			if !test.legacy {
				at = fmt.Sprintf(" %d", second.Add(test.diskOffset).UnixNano())
			}
			line := fmt.Sprintf("%s TRACE: [input-log disk 1 record%s] disk detail %s\n",
				second.Format(inputLogTimeFormat), at, strings.Repeat("x", 60))
			limit, payload := 120, strings.Repeat("m", 50)
			if test.trimDisk {
				// Only one disk line fits in its own queue, but that line
				// and the memory line together fit the shared limit.
				line += fmt.Sprintf("%s TRACE: [input-log disk 2 record] recent disk record %s\n",
					second.Format(inputLogTimeFormat), strings.Repeat("x", 60))
				limit, payload = 160, ""
			}
			if err := os.WriteFile(logPath, []byte(line), 0o600); err != nil {
				t.Fatal(err)
			}
			store := &recordingAttempts{}
			current, _, finish := logger.BeginInputLog(ctx, store, source, "upload")
			FromContext(current, logger).Tracef("memory-only failure %s", payload)
			finish("", "failed")
			logger.inputLogs.entries[0].Time = second.Add(900 * time.Millisecond)
			attempts := []db.InputLogAttempt{store.attempts[0], {
				ID:         "disk",
				SourcePath: source,
				Kind:       "preparation",
				StartedAt:  second,
			}}
			result, err := readInputLogs(ctx, logger, database, attempts, limit, false)
			if err != nil || result.Coverage != "partial" ||
				strings.Contains(result.Text, "memory-only failure") != test.prefersMemory ||
				strings.Contains(result.Text, "disk detail") == test.prefersMemory {
				t.Fatalf("bounded same-second export = %#v, %v", result, err)
			}
			if strings.Contains(result.Text, "recent disk record") != test.trimDisk {
				t.Fatalf("unexpected retained disk record: %s", result.Text)
			}
			if strings.Contains(result.Text, "order within a second may be uncertain") != test.legacy {
				t.Fatalf("expected ordering warning only for legacy disk records: %s", result.Text)
			}
		})
	}
}

func TestInputLogUsesMemoryWhenFileLoggingOff(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database := filepath.Join(t.TempDir(), "upbrr.db")
	logger, err := New(config.LoggingConfig{Level: "info"}, database)
	if err != nil {
		t.Fatal(err)
	}
	logger.SetConsoleOutput(io.Discard, io.Discard)
	source := filepath.Join(filepath.Dir(database), "Example.mkv")
	store := &recordingAttempts{}
	first, _, finishFirst := logger.BeginInputLog(ctx, store, source, "CLI input")
	second, _, finishSecond := logger.BeginInputLog(ctx, store, source, "CLI input")
	FromContext(first, logger).Infof("older attempt")
	FromContext(second, logger).Infof("requested input")
	logger.Infof("global line")
	finishFirst("", "failed")
	finishSecond("", "completed")
	data, err := ReadInputLog(ctx, logger, database, store.attempts[1])
	if err != nil || data.Coverage != "memory" || !strings.Contains(data.Text, "requested input") ||
		strings.Contains(data.Text, "older attempt") || strings.Contains(data.Text, "global line") {
		t.Fatalf("memory export = %#v, %v", data, err)
	}
	if data, err := ReadInputLog(ctx, nil, database, store.attempts[1]); err != nil || data.Coverage != "expired" {
		t.Fatalf("memory incorrectly persisted: %#v, %v", data, err)
	}
}

func TestInputLogMemoryOverflowReportsPartial(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	database := filepath.Join(t.TempDir(), "upbrr.db")
	logger, err := NewWithInputLogStore(config.LoggingConfig{Level: "warn"}, database, newInputLogStore(2))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.Close() })
	store := &recordingAttempts{}
	capture, _, finish := logger.BeginInputLog(ctx, store, filepath.Join(filepath.Dir(database), "Example.mkv"), "input")
	log := FromContext(capture, logger)
	log.Tracef("evicted prefix")
	log.Tracef("retained middle")
	log.Tracef("retained tail")
	finish("", "completed")

	export, err := ReadInputLog(ctx, logger, database, store.attempts[0])
	if err != nil || export.Coverage != "partial" || strings.Contains(export.Text, "evicted prefix") ||
		!strings.Contains(export.Text, "retained middle") || !strings.Contains(export.Text, "retained tail") {
		t.Fatalf("truncated memory export = %#v, %v", export, err)
	}
}

func TestInputLogExportCapKeepsNewestWhenAppendedOutOfOrder(t *testing.T) {
	t.Parallel()
	for _, fromDisk := range []bool{false, true} {
		name := "memory"
		if fromDisk {
			name = "disk"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			database := filepath.Join(t.TempDir(), "upbrr.db")
			logger, err := New(config.LoggingConfig{Level: "warn"}, database)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = logger.Close() })
			store := &recordingAttempts{}
			source := filepath.Join(filepath.Dir(database), "Example.mkv")
			first, firstID, finishFirst := logger.BeginInputLog(ctx, store, source, "prepare")
			second, secondID, finishSecond := logger.BeginInputLog(ctx, store, source, "upload")
			stamp := time.Date(2026, time.October, 5, 12, 1, 2, 0, time.Local)
			if fromDisk {
				path, err := LogPath(database)
				if err != nil {
					t.Fatal(err)
				}
				line := "%s TRACE: [input-log %s 2 record %d] %s\n"
				disk := fmt.Sprintf(line, stamp.Format(inputLogTimeFormat), firstID, stamp.Add(time.Millisecond).UnixNano(), "newer detail") +
					fmt.Sprintf(line, stamp.Format(inputLogTimeFormat), secondID, stamp.UnixNano(), "older detail")
				if err := os.WriteFile(path, []byte(disk), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				FromContext(first, logger).Tracef("newer detail")
				FromContext(second, logger).Tracef("older detail")
				logger.inputLogs.entries[0].Time = stamp.Add(time.Millisecond)
				logger.inputLogs.entries[1].Time = stamp
			}
			finishFirst("", "completed")
			finishSecond("", "completed")
			export, err := readInputLogs(ctx, logger, database, store.attempts, 40, false)
			if err != nil || export.Coverage != "partial" || !strings.Contains(export.Text, "newer detail") ||
				strings.Contains(export.Text, "older detail") {
				t.Fatalf("capped export = %#v, %v", export, err)
			}
		})
	}
}

type recordingAttempts struct {
	attempts []db.InputLogAttempt
}

func (s *recordingAttempts) BeginInputLog(_ context.Context, attempt db.InputLogAttempt) error {
	s.attempts = append(s.attempts, attempt)
	return nil
}

func (s *recordingAttempts) FinishInputLog(context.Context, db.InputLogAttempt) error {
	return nil
}

func TestInputLogReaderPropagatesReadError(t *testing.T) {
	t.Parallel()
	readErr := errors.New("synthetic disk read failure")
	src := io.MultiReader(strings.NewReader("unrelated log line\n"), iotest.ErrReader(readErr))
	reader := &inputLogReader{}
	if err := reader.readLogRecords(t.Context(), src); !errors.Is(err, readErr) {
		t.Fatalf("readLogRecords error = %v, want %v", err, readErr)
	}
}

func TestInputLogSnapshotGrowthAndInvalidation(t *testing.T) {
	t.Parallel()

	newFixture := func(t *testing.T, maxFiles int) (*Logger, *rotatingWriter, string, string) {
		t.Helper()
		ctx := t.Context()
		root := t.TempDir()
		database := filepath.Join(root, "upbrr.db")
		logger, err := New(config.LoggingConfig{
			Level:          "warn",
			FileEnabled:    true,
			MaxTotalSizeMB: 1,
			MaxFiles:       maxFiles,
		}, database)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = logger.Close() })
		logger.SetConsoleOutput(io.Discard, io.Discard)
		writer, ok := logger.closer.(*rotatingWriter)
		if !ok {
			t.Fatal("expected rotating file sink")
		}
		store := &recordingAttempts{}
		source := filepath.Join(root, "Example.mkv")
		scoped, id, finish := logger.BeginInputLog(ctx, store, source, "open input")
		FromContext(scoped, logger).Tracef("captured detail")
		finish("", "completed")
		return logger, writer, filepath.Dir(writer.path), id
	}

	t.Run("ordinary active appends preserve snapshot completeness", func(t *testing.T) {
		t.Parallel()
		logger, writer, logDir, id := newFixture(t, 4)
		// Force a rotated file as well so both active and rotated files are present.
		if err := writer.rotateLocked(); err != nil {
			t.Fatal(err)
		}
		logger.Infof("active baseline line")

		root, err := os.OpenRoot(logDir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		reader := inputLogReader{
			records: map[string]*inputLogRecords{id: {}},
			limit:   inputExportLimit,
		}
		files, err := reader.snapshotFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		// Append to active upbrr.log before reading, including a line that would
		// corrupt the attempt if reads were not bounded to the snapshot size.
		logger.Infof("[input-log %s 99 record %d] post-snapshot line", id, time.Now().UnixNano())
		for _, f := range files {
			if err := reader.readFile(t.Context(), root, f); err != nil {
				t.Fatal(err)
			}
		}
		// Append again after reading and before verification.
		logger.Infof("post-read unrelated append")
		reader.verifyFiles(root, files)

		rec := reader.records[id]
		if reader.incomplete || !rec.started || !rec.ended || rec.incomplete {
			t.Fatalf("ordinary active appends invalidated snapshot: reader.incomplete=%t rec=%+v", reader.incomplete, rec)
		}
		for _, line := range reader.lines[reader.first:] {
			if strings.Contains(line.text, "post-snapshot line") {
				t.Fatalf("reader scanned past snapshot size: %q", line.text)
			}
		}
	})

	t.Run("active file replacement invalidates snapshot", func(t *testing.T) {
		t.Parallel()
		logger, writer, logDir, id := newFixture(t, 4)
		root, err := os.OpenRoot(logDir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		reader := inputLogReader{records: map[string]*inputLogRecords{id: {}}, limit: inputExportLimit}
		files, err := reader.snapshotFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.rotateLocked(); err != nil {
			t.Fatal(err)
		}
		logger.Infof("new active file after multi-file rotation")
		for _, f := range files {
			if err := reader.readFile(t.Context(), root, f); err != nil {
				t.Fatal(err)
			}
		}
		reader.verifyFiles(root, files)
		if !reader.incomplete {
			t.Fatal("expected replaced active file to mark snapshot incomplete")
		}
	})

	t.Run("single file rotation shrink invalidates snapshot", func(t *testing.T) {
		t.Parallel()
		logger, writer, logDir, id := newFixture(t, 1)
		root, err := os.OpenRoot(logDir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		reader := inputLogReader{records: map[string]*inputLogRecords{id: {}}, limit: inputExportLimit}
		files, err := reader.snapshotFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.rotateLocked(); err != nil {
			t.Fatal(err)
		}
		logger.Infof("short")
		for _, f := range files {
			if err := reader.readFile(t.Context(), root, f); err != nil {
				t.Fatal(err)
			}
		}
		reader.verifyFiles(root, files)
		if !reader.incomplete {
			t.Fatal("expected single-file truncation shrink to mark snapshot incomplete")
		}
	})

	t.Run("single file rotation regrown past snapshot invalidates snapshot", func(t *testing.T) {
		t.Parallel()
		logger, writer, logDir, id := newFixture(t, 1)
		root, err := os.OpenRoot(logDir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		reader := inputLogReader{records: map[string]*inputLogRecords{id: {}}, limit: inputExportLimit}
		files, err := reader.snapshotFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		// Read intact snapshot first, then truncate in-place and write more bytes
		// than the initial snapshot size so same-inode size grows past f.info.Size().
		for _, f := range files {
			if err := reader.readFile(t.Context(), root, f); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.rotateLocked(); err != nil {
			t.Fatal(err)
		}
		logger.Infof("regrown after single-file truncation %s", strings.Repeat("x", int(files[0].info.Size())+64))
		reader.verifyFiles(root, files)
		if !reader.incomplete {
			t.Fatal("expected single-file truncation regrown past snapshot to mark snapshot incomplete")
		}
	})

	t.Run("changed rotated file invalidates snapshot", func(t *testing.T) {
		t.Parallel()
		logger, writer, logDir, id := newFixture(t, 4)
		if err := writer.rotateLocked(); err != nil {
			t.Fatal(err)
		}
		logger.Infof("active line")
		root, err := os.OpenRoot(logDir)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		reader := inputLogReader{records: map[string]*inputLogRecords{id: {}}, limit: inputExportLimit}
		files, err := reader.snapshotFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if err := reader.readFile(t.Context(), root, f); err != nil {
				t.Fatal(err)
			}
		}
		rotatedFile, err := os.OpenFile(writer.path+".1", os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(rotatedFile, "2026/01/01 00:00:00 INFO: mutated rotated file\n"); err != nil {
			_ = rotatedFile.Close()
			t.Fatal(err)
		}
		if err := rotatedFile.Close(); err != nil {
			t.Fatal(err)
		}
		reader.verifyFiles(root, files)
		if !reader.incomplete {
			t.Fatal("expected modified rotated file to mark snapshot incomplete")
		}
	})

	t.Run("active file open errors surface while vanished files mark incomplete", func(t *testing.T) {
		t.Parallel()
		_, writer, logDir, id := newFixture(t, 4)
		root, err := os.OpenRoot(logDir)
		if err != nil {
			t.Fatal(err)
		}
		info, err := root.Lstat("upbrr.log")
		if err != nil {
			_ = root.Close()
			t.Fatal(err)
		}
		if _, _, ok, err := snapshotActiveLogFile(root, "missing.log", info); err != nil || ok {
			_ = root.Close()
			t.Fatalf("vanished active file = (ok=%t, err=%v), want (false, nil)", ok, err)
		}
		if err := root.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, ok, err := snapshotActiveLogFile(root, "upbrr.log", info); !errors.Is(err, os.ErrClosed) || ok {
			t.Fatalf("non-NotExist active open = (ok=%t, err=%v), want (false, os.ErrClosed)", ok, err)
		}
		if runtime.GOOS != "windows" && os.Geteuid() != 0 {
			if err := os.Chmod(writer.path, 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(writer.path, 0o600) })
			database := filepath.Join(filepath.Dir(logDir), "upbrr.db")
			_, err := ReadInputLog(t.Context(), nil, database, db.InputLogAttempt{
				ID:         id,
				SourcePath: filepath.Join(filepath.Dir(logDir), "Example.mkv"),
				Kind:       "open input",
				Outcome:    "completed",
				StartedAt:  time.Now().UTC(),
			})
			if !errors.Is(err, os.ErrPermission) {
				t.Fatalf("unreadable active log ReadInputLog err = %v, want os.ErrPermission", err)
			}
		}
	})
}

func TestInputLogLateWritesAfterFinishUseOrdinaryLogging(t *testing.T) {
	t.Parallel()

	for _, fileEnabled := range []bool{true, false} {
		name := "memory only"
		if fileEnabled {
			name = "file enabled"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			root := t.TempDir()
			database := filepath.Join(root, "upbrr.db")
			logger, err := NewWithConsoleLevel(config.LoggingConfig{
				Level:          "warn",
				FileEnabled:    fileEnabled,
				MaxTotalSizeMB: 1,
				MaxFiles:       2,
			}, database, "error")
			if err != nil {
				t.Fatal(err)
			}
			defer logger.Close()
			var stdout, stderr bytes.Buffer
			logger.SetConsoleOutput(&stdout, &stderr)
			subID, sub := logger.Subscribe(10)
			defer logger.Unsubscribe(subID)

			store := &recordingAttempts{}
			source := filepath.Join(root, "Example.mkv")
			scopedCtx, id, finish := logger.BeginInputLog(ctx, store, source, "open input")
			scoped := FromContext(scopedCtx, logger)

			scoped.Tracef("before finish trace")
			scoped.Warnf("before finish warn")
			finish("", "completed")
			store.attempts[0].Outcome = "completed"

			scoped.Tracef("after finish trace")
			scoped.Warnf("after finish warn")
			scoped.Errorf("after finish error")

			exported, err := ReadInputLog(ctx, logger, database, store.attempts[0])
			if err != nil {
				t.Fatal(err)
			}
			wantCoverage := "memory"
			if fileEnabled {
				wantCoverage = "complete"
			}
			if exported.Coverage != wantCoverage {
				t.Fatalf("export coverage = %q, want %q", exported.Coverage, wantCoverage)
			}
			for _, want := range []string{"before finish trace", "before finish warn"} {
				if !strings.Contains(exported.Text, want) {
					t.Fatalf("missing active capture line %q in export:\n%s", want, exported.Text)
				}
			}
			for _, unwanted := range []string{"after finish trace", "after finish warn", "after finish error"} {
				if strings.Contains(exported.Text, unwanted) {
					t.Fatalf("late write %q leaked into finished attempt export:\n%s", unwanted, exported.Text)
				}
			}
			memEntries := logger.recentInputLogs()
			if len(memEntries) != 2 || memEntries[0].Message != "before finish trace" || memEntries[1].Message != "before finish warn" {
				t.Fatalf("memory input logs attached late writes: %#v", memEntries)
			}
			recent := logger.Recent(10)
			if len(recent) != 3 || recent[0].Message != "before finish warn" ||
				recent[1].Message != "after finish warn" || recent[2].Message != "after finish error" {
				t.Fatalf("application buffer = %#v", recent)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "after finish error") ||
				strings.Contains(stderr.String(), "warn") || strings.Contains(stderr.String(), "trace") {
				t.Fatalf("console thresholds changed: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			if len(sub) != 3 {
				t.Fatalf("subscriber entries = %d, want 3", len(sub))
			}
			if fileEnabled {
				logPath, err := LogPath(database)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
				for _, want := range []string{"TRACE: after finish trace", "WARN: after finish warn", "ERROR: after finish error"} {
					found := false
					for _, line := range lines {
						if strings.Contains(line, want) {
							if strings.Contains(line, "[input-log "+id) {
								t.Fatalf("late disk write retained attempt marker: %q", line)
							}
							found = true
						}
					}
					if !found {
						t.Fatalf("late write %q was dropped from disk log:\n%s", want, string(raw))
					}
				}
			}
		})
	}

	t.Run("concurrent writes across finish keep disk and memory consistent", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()
		root := t.TempDir()
		database := filepath.Join(root, "upbrr.db")
		logger, err := New(config.LoggingConfig{
			Level:          "warn",
			FileEnabled:    true,
			MaxTotalSizeMB: 1,
			MaxFiles:       2,
		}, database)
		if err != nil {
			t.Fatal(err)
		}
		defer logger.Close()
		logger.SetConsoleOutput(io.Discard, io.Discard)
		store := &recordingAttempts{}
		scopedCtx, id, finish := logger.BeginInputLog(ctx, store, filepath.Join(root, "Example.mkv"), "open input")
		scoped := FromContext(scopedCtx, logger)

		start := make(chan struct{})
		wroteOne := make(chan struct{}, 1)
		var wg sync.WaitGroup
		for worker := range 4 {
			wg.Go(func() {
				<-start
				for i := range 25 {
					scoped.Tracef("concurrent-%d-%d", worker, i)
					if i == 0 {
						select {
						case wroteOne <- struct{}{}:
						default:
						}
					}
				}
			})
		}
		close(start)
		select {
		case <-wroteOne:
		case <-time.After(10 * time.Second):
			finish("", "completed")
			wg.Wait()
			t.Fatal("concurrent logging worker did not write before finish")
		}
		finish("", "completed")
		wg.Wait()
		store.attempts[0].Outcome = "completed"
		scoped.Tracef("strictly-after-finish")

		diskOnly, err := ReadInputLog(ctx, nil, database, store.attempts[0])
		if err != nil || diskOnly.Coverage != "complete" {
			t.Fatalf("disk export = %#v, %v", diskOnly, err)
		}
		if strings.Contains(diskOnly.Text, "strictly-after-finish") {
			t.Fatal("post-finish write entered disk attempt")
		}
		logPath, err := LogPath(database)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		type diskRecord struct {
			stamp   int64
			message string
		}
		var diskRecords []diskRecord
		markerPrefix := "[input-log " + id + " "
		for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
			_, rest, ok := strings.Cut(line, markerPrefix)
			if !ok {
				continue
			}
			meta, body, ok := strings.Cut(rest, "] ")
			if !ok {
				continue
			}
			parts := strings.Fields(meta)
			if len(parts) != 3 || parts[1] != "record" {
				continue
			}
			stamp, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil {
				t.Fatalf("parse disk marker timestamp %q: %v", meta, err)
			}
			diskRecords = append(diskRecords, diskRecord{stamp: stamp, message: body})
		}
		memEntries := logger.recentInputLogs()
		if len(memEntries) != len(diskRecords) {
			t.Fatalf("memory input logs count = %d, disk records = %d", len(memEntries), len(diskRecords))
		}
		for i, entry := range memEntries {
			if entry.Message == "strictly-after-finish" {
				t.Fatal("post-finish write entered memory input logs")
			}
			if entry.Message != diskRecords[i].message || entry.Time.UnixNano() != diskRecords[i].stamp {
				t.Fatalf("record %d mismatch: memory=(%q, %d) disk=(%q, %d)",
					i, entry.Message, entry.Time.UnixNano(), diskRecords[i].message, diskRecords[i].stamp)
			}
		}
	})
}

func TestInputLogStorePreservesCapOrderAndIndependenceAcrossGenerations(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	root := t.TempDir()
	database := filepath.Join(root, "upbrr.db")
	store := newInputLogStore(3)

	gen1, err := NewWithInputLogStore(config.LoggingConfig{
		Level:       "warn",
		FileEnabled: false,
	}, database, store)
	if err != nil {
		t.Fatal(err)
	}
	gen1.SetConsoleOutput(io.Discard, io.Discard)
	sub1ID, sub1 := gen1.Subscribe(4)
	defer gen1.Unsubscribe(sub1ID)

	gen2, err := NewWithInputLogStore(config.LoggingConfig{
		Level:          "info",
		FileEnabled:    true,
		MaxTotalSizeMB: 1,
		MaxFiles:       2,
	}, database, store)
	if err != nil {
		t.Fatal(err)
	}
	defer gen2.Close()
	gen2.SetConsoleOutput(io.Discard, io.Discard)
	sub2ID, sub2 := gen2.Subscribe(4)
	defer gen2.Unsubscribe(sub2ID)

	attempts := &recordingAttempts{}
	source := filepath.Join(root, "Example.mkv")
	scoped1, _, finish1 := gen1.BeginInputLog(ctx, attempts, source, "open input")
	FromContext(scoped1, gen1).Tracef("gen1-first")
	FromContext(scoped1, gen1).Warnf("gen1-second")

	// Still-live gen1 writes remain visible on gen2 before and after gen1 retires.
	if got := gen2.recentInputLogs(); len(got) != 2 || got[0].Message != "gen1-first" || got[1].Message != "gen1-second" {
		t.Fatalf("gen2 shared entries before retire = %#v", got)
	}
	finish1("", "failed")
	FromContext(scoped1, gen1).Warnf("gen1-late-after-finish")
	if err := gen1.Close(); err != nil {
		t.Fatal(err)
	}

	// Independent app buffers, levels, and subscribers remain per-logger.
	gen2.Infof("gen2-app-info")
	if recent1 := gen1.Recent(10); len(recent1) != 2 || recent1[0].Message != "gen1-second" || recent1[1].Message != "gen1-late-after-finish" {
		t.Fatalf("gen1 app buffer = %#v", recent1)
	}
	if recent2 := gen2.Recent(10); len(recent2) != 1 || recent2[0].Message != "gen2-app-info" {
		t.Fatalf("gen2 app buffer = %#v", recent2)
	}
	if len(sub1) != 2 || len(sub2) != 1 {
		t.Fatalf("subscriber isolation violated: sub1=%d sub2=%d", len(sub1), len(sub2))
	}

	// Cap and oldest-to-newest ordering hold across generations.
	scoped2, _, finish2 := gen2.BeginInputLog(ctx, attempts, source, "open input")
	FromContext(scoped2, gen2).Tracef("gen2-third")
	FromContext(scoped2, gen2).Tracef("gen2-fourth")
	finish2("", "completed")

	got := gen2.recentInputLogs()
	want := []string{"gen1-second", "gen2-third", "gen2-fourth"}
	if len(got) != len(want) {
		t.Fatalf("capped shared entries = %#v, want %v", got, want)
	}
	for i, msg := range want {
		if got[i].Message != msg {
			t.Fatalf("shared entry %d = %q, want %q", i, got[i].Message, msg)
		}
	}

	// A separate logger with no shared store starts cleanly isolated.
	isolated, err := New(config.LoggingConfig{Level: "info"}, database)
	if err != nil {
		t.Fatal(err)
	}
	defer isolated.Close()
	if isolatedEntries := isolated.recentInputLogs(); len(isolatedEntries) != 0 {
		t.Fatalf("isolated logger saw shared store entries: %#v", isolatedEntries)
	}
}
