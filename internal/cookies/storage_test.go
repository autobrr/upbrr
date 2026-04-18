// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestCookieStoreRunInTransactionRollsBackOnSaveFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := newTestCookieDB(t)

	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatalf("create cookie store: %v", err)
	}

	validKey := []byte("0123456789abcdef0123456789abcdef")
	if err := store.SaveCookie(ctx, "tracker", "session", "original", validKey); err != nil {
		t.Fatalf("seed cookie: %v", err)
	}

	err = store.RunInTransaction(ctx, func(tx *sql.Tx) error {
		if err := store.DeleteAllTrackerCookiesTx(ctx, tx, "tracker"); err != nil {
			return err
		}

		return store.SaveCookieTx(ctx, tx, "tracker", "session", "replacement", []byte("short"))
	})
	if err == nil {
		t.Fatal("expected transaction to fail")
	}
	if !strings.Contains(err.Error(), "SaveCookie: invalid encryption key") {
		t.Fatalf("expected invalid encryption key validation error, got %v", err)
	}

	got, err := store.GetCookie(ctx, "tracker", "session", validKey)
	if err != nil {
		t.Fatalf("get cookie after rollback: %v", err)
	}
	if got != "original" {
		t.Fatalf("expected original cookie after rollback, got %q", got)
	}
}

func TestCookieStoreGetCookieRejectsEmptyTrackerOrName(t *testing.T) {
	t.Parallel()

	db := newTestCookieDB(t)
	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatalf("create cookie store: %v", err)
	}

	for _, tc := range []struct {
		name      string
		trackerID string
		cookie    string
	}{
		{name: "empty tracker", trackerID: "", cookie: "session"},
		{name: "empty cookie", trackerID: "tracker", cookie: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.GetCookie(context.Background(), tc.trackerID, tc.cookie, []byte("0123456789abcdef0123456789abcdef"))
			if err == nil {
				t.Fatal("expected input validation error")
			}
			if err.Error() != "SaveCookie: trackerID and cookieName must be non-empty" {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestCookieStoreDeleteCookieRejectsEmptyTrackerOrName(t *testing.T) {
	t.Parallel()

	db := newTestCookieDB(t)
	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatalf("create cookie store: %v", err)
	}

	for _, tc := range []struct {
		name      string
		trackerID string
		cookie    string
	}{
		{name: "empty tracker", trackerID: "", cookie: "session"},
		{name: "empty cookie", trackerID: "tracker", cookie: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := store.DeleteCookie(context.Background(), tc.trackerID, tc.cookie)
			if err == nil {
				t.Fatal("expected input validation error")
			}
			if err.Error() != "SaveCookie: trackerID and cookieName must be non-empty" {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestCookieStoreGetCookieReturnsNotFoundError(t *testing.T) {
	t.Parallel()

	db := newTestCookieDB(t)
	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatalf("create cookie store: %v", err)
	}

	_, err = store.GetCookie(context.Background(), "tracker", "missing", []byte("0123456789abcdef0123456789abcdef"))
	if err == nil {
		t.Fatal("expected missing cookie error")
	}
	if !errors.Is(err, ErrCookieNotFound) {
		t.Fatalf("expected ErrCookieNotFound, got %v", err)
	}
}

func TestCookieStoreSaveCookieRejectsInvalidEncryptionKey(t *testing.T) {
	t.Parallel()

	db := newTestCookieDB(t)
	store, err := NewCookieStore(db)
	if err != nil {
		t.Fatalf("create cookie store: %v", err)
	}

	err = store.SaveCookie(context.Background(), "tracker", "session", "value", []byte("short"))
	if err == nil {
		t.Fatal("expected invalid encryption key error")
	}
	if err.Error() != "SaveCookie: invalid encryption key" {
		t.Fatalf("unexpected validation error: %v", err)
	}
}
