// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/authmaterial"
	"github.com/autobrr/upbrr/internal/cookies"
	"github.com/autobrr/upbrr/internal/services/db"
)

func TestChangeAuthPasswordUpdatesHashAndRevokesRetainedSessions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	if err := BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	store, err := newAuthStore(dbPath)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	record.BrowseRoot = filepath.Join(t.TempDir(), "media")
	if err := store.UpdateRecord(record); err != nil {
		t.Fatalf("UpdateRecord: %v", err)
	}

	sessionStore, err := newSessionStore(dbPath)
	if err != nil {
		t.Fatalf("newSessionStore: %v", err)
	}
	if err := sessionStore.Save([]session{{
		ID:        "retained-session",
		Username:  "tester",
		CSRFToken: "synthetic-csrf",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
		Retain:    true,
	}}); err != nil {
		t.Fatalf("Save session: %v", err)
	}

	backupPath, err := ChangeAuthPassword(t.Context(), dbPath, "very-secure-password", "replacement-secure-password")
	if err != nil {
		t.Fatalf("ChangeAuthPassword: %v", err)
	}
	backup := readAuthBackup(t, backupPath)
	if !verifyPassword("very-secure-password", backup.PasswordHash) {
		t.Fatal("backup does not preserve the previous password")
	}
	if backup.BrowseRoot != record.BrowseRoot {
		t.Fatalf("backup browse root = %q, want %q", backup.BrowseRoot, record.BrowseRoot)
	}
	updated, err := store.Load()
	if err != nil {
		t.Fatalf("Load updated record: %v", err)
	}
	if !verifyPassword("replacement-secure-password", updated.PasswordHash) {
		t.Fatal("replacement password does not verify")
	}
	if verifyPassword("very-secure-password", updated.PasswordHash) {
		t.Fatal("previous password still verifies")
	}
	if updated.BrowseRoot != record.BrowseRoot {
		t.Fatalf("browse root = %q, want %q", updated.BrowseRoot, record.BrowseRoot)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dbPath), sessionFileName)); !os.IsNotExist(err) {
		t.Fatalf("retained session file still exists: %v", err)
	}
}

func TestChangeAuthPasswordRejectsWrongCurrentPasswordWithoutRevokingSessions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	if err := BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	sessionPath := filepath.Join(filepath.Dir(dbPath), sessionFileName)
	if err := os.WriteFile(sessionPath, []byte("[]"), 0o600); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	backupPath, err := ChangeAuthPassword(t.Context(), dbPath, "incorrect-password", "replacement-secure-password")
	if err == nil || !strings.Contains(err.Error(), "current password is incorrect") {
		t.Fatalf("ChangeAuthPassword error = %v", err)
	}
	if backupPath != "" {
		t.Fatalf("rejected password created backup %q", backupPath)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session file changed after rejected password: %v", err)
	}
	record, err := authmaterial.LoadRecordFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadRecordFromDBPath: %v", err)
	}
	if !verifyPassword("very-secure-password", record.PasswordHash) {
		t.Fatal("current password changed after rejected attempt")
	}
}

func TestChangeAuthPasswordRewrapsLegacyCookies(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	ensureMigratedTestDB(t, dbPath)
	legacy := authRecord{
		Username:     "tester",
		PasswordHash: makeLegacyHash(),
		CreatedAt:    time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	if err := os.WriteFile(AuthFilePath(dbPath), raw, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	repo, err := db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("OpenContext: %v", err)
	}
	oldKey, err := cookies.NewKeyManager(repo.RawDB()).InitializeEncryptionKey(t.Context(), dbPath)
	if err != nil {
		_ = repo.Close()
		t.Fatalf("InitializeEncryptionKey: %v", err)
	}
	cookieStore, err := cookies.NewCookieStore(repo.RawDB())
	if err != nil {
		_ = repo.Close()
		t.Fatalf("NewCookieStore: %v", err)
	}
	if err := cookieStore.SaveCookie(t.Context(), "tracker", "session", "cookie-value", oldKey); err != nil {
		_ = repo.Close()
		t.Fatalf("SaveCookie: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("Close seed repository: %v", err)
	}

	backupPath, err := ChangeAuthPassword(t.Context(), dbPath, "very-secure-password", "replacement-secure-password")
	if err != nil {
		t.Fatalf("ChangeAuthPassword: %v", err)
	}
	backup := readAuthBackup(t, backupPath)
	if strings.TrimSpace(backup.EncryptionKeySeed) != "" || !verifyPassword("very-secure-password", backup.PasswordHash) {
		t.Fatal("backup does not preserve the legacy auth record")
	}
	updated, err := authmaterial.LoadRecordFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadRecordFromDBPath: %v", err)
	}
	if updated.PendingUpgrade != nil || strings.TrimSpace(updated.EncryptionKeySeed) == "" {
		t.Fatalf(
			"legacy auth update incomplete: pending=%t seed_present=%t",
			updated.PendingUpgrade != nil,
			strings.TrimSpace(updated.EncryptionKeySeed) != "",
		)
	}
	if !verifyPassword("replacement-secure-password", updated.PasswordHash) {
		t.Fatal("replacement password does not verify")
	}

	repo, err = db.OpenContext(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("reopen repository: %v", err)
	}
	defer repo.Close()
	newHelper, _, err := updated.AuthMaterial().PrimaryHelper()
	if err != nil {
		t.Fatalf("PrimaryHelper: %v", err)
	}
	salt, err := loadCookieEncryptionSalt(t.Context(), repo.RawDB())
	if err != nil {
		t.Fatalf("loadCookieEncryptionSalt: %v", err)
	}
	newKey, err := cookies.DeriveEncryptionKey(newHelper, salt)
	if err != nil {
		t.Fatalf("DeriveEncryptionKey: %v", err)
	}
	cookieStore, err = cookies.NewCookieStore(repo.RawDB())
	if err != nil {
		t.Fatalf("NewCookieStore: %v", err)
	}
	value, err := cookieStore.GetCookie(t.Context(), "tracker", "session", newKey)
	if err != nil {
		t.Fatalf("GetCookie: %v", err)
	}
	if value != "cookie-value" {
		t.Fatalf("cookie value = %q, want %q", value, "cookie-value")
	}
}

func TestUpdateBrowseRootsReplacesPolicy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	if err := BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	first := t.TempDir()
	second := t.TempDir()

	count, backupPath, err := UpdateBrowseRoots(t.Context(), dbPath, []string{first, second, first}, false)
	if err != nil {
		t.Fatalf("UpdateBrowseRoots: %v", err)
	}
	if count != 2 {
		t.Fatalf("root count = %d, want 2", count)
	}
	record, err := authmaterial.LoadRecordFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadRecordFromDBPath: %v", err)
	}
	roots := splitBrowsePolicyRoots(record.BrowseRoot)
	if len(roots) != 2 || !sameFilesystemPath(roots[0], first) || !sameFilesystemPath(roots[1], second) {
		t.Fatalf("stored roots = %#v", roots)
	}
	if record.AllowUnrestrictedBrowse {
		t.Fatal("restricted policy unexpectedly allows unrestricted browsing")
	}
	backup := readAuthBackup(t, backupPath)
	if backup.BrowseRoot != "" || backup.AllowUnrestrictedBrowse {
		t.Fatal("first backup contains the updated browse policy")
	}

	count, secondBackupPath, err := UpdateBrowseRoots(t.Context(), dbPath, nil, true)
	if err != nil {
		t.Fatalf("UpdateBrowseRoots unrestricted: %v", err)
	}
	if count != 0 {
		t.Fatalf("unrestricted root count = %d, want 0", count)
	}
	record, err = authmaterial.LoadRecordFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadRecordFromDBPath unrestricted: %v", err)
	}
	if record.BrowseRoot != "" || !record.AllowUnrestrictedBrowse {
		t.Fatalf("unrestricted policy: has_roots=%t unrestricted=%t", record.BrowseRoot != "", record.AllowUnrestrictedBrowse)
	}
	if secondBackupPath == backupPath {
		t.Fatal("successive browse updates reused one backup path")
	}
	secondBackup := readAuthBackup(t, secondBackupPath)
	secondBackupRoots := splitBrowsePolicyRoots(secondBackup.BrowseRoot)
	if len(secondBackupRoots) != 2 || secondBackup.AllowUnrestrictedBrowse {
		t.Fatalf("second backup does not preserve the restricted policy: %#v", secondBackup)
	}
}

func TestUpdateBrowseRootsRejectsRootsWithUnrestrictedAccess(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	if err := BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}

	if _, _, err := UpdateBrowseRoots(t.Context(), dbPath, []string{t.TempDir()}, true); err == nil ||
		!strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("UpdateBrowseRoots mixed policy error = %v", err)
	}
	record, err := authmaterial.LoadRecordFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadRecordFromDBPath: %v", err)
	}
	if record.BrowseRoot != "" || record.AllowUnrestrictedBrowse {
		t.Fatal("rejected mixed policy changed the auth record")
	}
}

func TestUpdateBrowseRootsRejectsMissingDirectoryWithoutChangingPolicy(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	if err := BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	existing := t.TempDir()
	if _, _, err := UpdateBrowseRoots(t.Context(), dbPath, []string{existing}, false); err != nil {
		t.Fatalf("seed browse roots: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	if _, _, err := UpdateBrowseRoots(t.Context(), dbPath, []string{missing}, false); err == nil {
		t.Fatal("expected missing browse root to fail")
	}
	record, err := authmaterial.LoadRecordFromDBPath(dbPath)
	if err != nil {
		t.Fatalf("LoadRecordFromDBPath: %v", err)
	}
	roots := splitBrowsePolicyRoots(record.BrowseRoot)
	if len(roots) != 1 || !sameFilesystemPath(roots[0], existing) {
		t.Fatalf("browse roots changed after rejected update: %#v", roots)
	}
}

func TestUpdateBrowseRootsKeepsPendingPasswordUpdateInSync(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state", "db.sqlite")
	if err := BootstrapAuthFile(dbPath, "tester", "very-secure-password"); err != nil {
		t.Fatalf("BootstrapAuthFile: %v", err)
	}
	store, err := newAuthStore(dbPath)
	if err != nil {
		t.Fatalf("newAuthStore: %v", err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	target := record
	target.PasswordHash = "pending-password-hash"
	if err := store.BeginPendingUpgrade(record, target); err != nil {
		t.Fatalf("BeginPendingUpgrade: %v", err)
	}

	root := t.TempDir()
	if _, _, err := UpdateBrowseRoots(t.Context(), dbPath, []string{root}, false); err != nil {
		t.Fatalf("UpdateBrowseRoots: %v", err)
	}
	updated, err := store.Load()
	if err != nil {
		t.Fatalf("Load updated record: %v", err)
	}
	if updated.PendingUpgrade == nil {
		t.Fatal("pending password update was cleared")
	}
	if !sameFilesystemPath(updated.BrowseRoot, root) || !sameFilesystemPath(updated.PendingUpgrade.Target.BrowseRoot, root) {
		t.Fatalf("active and pending browse roots differ: active=%q pending=%q", updated.BrowseRoot, updated.PendingUpgrade.Target.BrowseRoot)
	}
}

func readAuthBackup(t *testing.T, path string) authRecord {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth backup: %v", err)
	}
	var record authRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("unmarshal auth backup: %v", err)
	}
	return record
}
