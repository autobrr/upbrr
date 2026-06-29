// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package guiapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/cookies"
	servicedb "github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackerauth"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestAppImportTrackerAuthCookieContentRejectsOverCap(t *testing.T) {
	t.Parallel()

	_, err := (&App{}).ImportTrackerAuthCookieContent(
		"AR",
		"cookies.txt",
		strings.Repeat("x", trackerauth.MaxCookieImportContentBytes+1),
	)
	if err == nil {
		t.Fatal("expected over-cap import error")
	}
	if !strings.Contains(err.Error(), "cookie file content exceeds") {
		t.Fatalf("expected shared content cap error, got %v", err)
	}
}

func TestImportTrackerAuthCookieFileRejectsOverCap(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", trackerauth.MaxCookieImportContentBytes+1)), 0o600); err != nil {
		t.Fatalf("write oversized cookie file: %v", err)
	}

	_, err := importTrackerAuthCookieFile(context.Background(), trackerauth.NewService(config.Config{}), "AR", path)
	if err == nil {
		t.Fatal("expected over-cap import error")
	}
	if !strings.Contains(err.Error(), "cookie file content exceeds") {
		t.Fatalf("expected shared content cap error, got %v", err)
	}
}

func TestImportTrackerAuthCookieFileImportsValidFile(t *testing.T) {
	t.Parallel()

	dbPath := newGUITrackerAuthTestDB(t)
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(".example.test\tTRUE\t/\tTRUE\t0\tsession\tabc\n"), 0o600); err != nil {
		t.Fatalf("write cookie file: %v", err)
	}
	service := trackerauth.NewService(config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}})

	status, err := importTrackerAuthCookieFile(context.Background(), service, "AR", path)
	if err != nil {
		t.Fatalf("importTrackerAuthCookieFile: %v", err)
	}
	if status.CookieCount != 1 {
		t.Fatalf("expected imported cookie count, got %#v", status)
	}
}

func TestAppImportTrackerAuthCookiesTreatsDialogCancelAsNoop(t *testing.T) {
	oldOpen := openTrackerAuthCookieDialog
	t.Cleanup(func() { openTrackerAuthCookieDialog = oldOpen })
	openTrackerAuthCookieDialog = func(context.Context, runtime.OpenDialogOptions) (string, error) {
		return "", errors.New("shellItem is nil")
	}
	app := &App{runtimeCtx: newAppRuntimeContext(context.Background())}

	status, err := app.ImportTrackerAuthCookies("AR")
	if err != nil {
		t.Fatalf("ImportTrackerAuthCookies: %v", err)
	}
	if status.TrackerID != "AR" {
		t.Fatalf("expected current AR status on cancel, got %#v", status)
	}
}

func TestAppImportTrackerAuthCookiesPreservesRealDialogError(t *testing.T) {
	oldOpen := openTrackerAuthCookieDialog
	t.Cleanup(func() { openTrackerAuthCookieDialog = oldOpen })
	openTrackerAuthCookieDialog = func(context.Context, runtime.OpenDialogOptions) (string, error) {
		return "", errors.New("dialog failed")
	}
	app := &App{runtimeCtx: newAppRuntimeContext(context.Background())}

	_, err := app.ImportTrackerAuthCookies("AR")
	if err == nil {
		t.Fatal("expected dialog error")
	}
	if !strings.Contains(err.Error(), "open tracker cookie dialog") {
		t.Fatalf("expected wrapped dialog error, got %v", err)
	}
}

func TestAppImportTrackerAuthCookieContentCanceledContextDoesNotPersistCookies(t *testing.T) {
	t.Parallel()

	dbPath := newGUITrackerAuthTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app := &App{
		runtimeCtx: newAppRuntimeContext(ctx),
		cfg:        config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
	}

	_, err := app.ImportTrackerAuthCookieContent("AR", "cookies.txt", ".example.test\tTRUE\t/\tTRUE\t0\tsession\tabc\n")
	if err == nil {
		t.Fatal("expected canceled import error")
	}
	if _, loadErr := cookies.LoadTrackerCookieMap(context.Background(), dbPath, "AR"); loadErr == nil {
		t.Fatal("canceled import persisted cookies")
	}
}

func TestAppTestTrackerAuthUsesRuntimeContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app := &App{
		runtimeCtx: newAppRuntimeContext(ctx),
		cfg: config.Config{
			Trackers: config.TrackersConfig{
				Trackers: map[string]config.TrackerConfig{
					"MTV": {
						URL:      "http://127.0.0.1:1",
						Username: "user",
						Password: "pass",
					},
				},
			},
		},
	}

	status, err := app.TestTrackerAuth("MTV")
	if err != nil {
		t.Fatalf("TestTrackerAuth: %v", err)
	}
	if !strings.Contains(status.LastError, "context canceled") {
		t.Fatalf("expected context canceled status, got %#v", status)
	}
}

func TestAppLoginTrackerAuthBTNCookiesWithoutAPIPreservesMissingAPIStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := newGUITrackerAuthTestDB(t)
	if err := cookies.SaveTrackerCookieMap(ctx, dbPath, "BTN", map[string]string{"session": "abc"}); err != nil {
		t.Fatalf("SaveTrackerCookieMap: %v", err)
	}
	app := &App{
		runtimeCtx: newAppRuntimeContext(ctx),
		cfg:        config.Config{MainSettings: config.MainSettingsConfig{DBPath: dbPath}},
	}

	status, err := app.LoginTrackerAuth("BTN", api.TrackerAuthLoginRequest{})
	if err != nil {
		t.Fatalf("LoginTrackerAuth: %v", err)
	}
	if status.State != "login_required" || status.CookieCount != 1 || !strings.Contains(status.Message, "API key is required") {
		t.Fatalf("expected BTN cookie-only Wails login to preserve missing API status, got %#v", status)
	}
	if strings.Contains(status.Message, "username/password missing") {
		t.Fatalf("missing credentials masked missing API status: %#v", status)
	}
}

func newGUITrackerAuthTestDB(t *testing.T) string {
	t.Helper()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "upbrr.db")
	if err := authmaterial.BootstrapAuthFile(dbPath, "tester", "long-enough-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	repo, err := servicedb.OpenWithLoggerContext(ctx, dbPath, api.NopLogger{})
	if err != nil {
		t.Fatalf("OpenWithLoggerContext: %v", err)
	}
	if err := repo.MigrateContext(ctx); err != nil {
		_ = repo.Close()
		t.Fatalf("MigrateContext: %v", err)
	}
	_ = repo.Close()
	return dbPath
}
