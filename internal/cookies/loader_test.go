// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package cookies

import (
	"context"
	"strings"
	"testing"
)

func TestIsMissingCookieSchemaError(t *testing.T) {
	t.Parallel()

	db := newTestCookieDB(t)
	ctx := context.Background()

	var missingTableErr error
	if _, err := db.ExecContext(ctx, `SELECT * FROM missing_cookie_table`); err != nil {
		missingTableErr = err
	} else {
		t.Fatal("expected missing table error")
	}

	if !isMissingCookieSchemaError(missingTableErr) {
		t.Fatalf("expected missing table error to be classified as missing schema: %v", missingTableErr)
	}

	var genericSQLiteErr error
	if _, err := db.ExecContext(ctx, `SELECT FROM tracker_cookies`); err != nil {
		genericSQLiteErr = err
	} else {
		t.Fatal("expected generic sqlite error")
	}

	if strings.Contains(strings.ToLower(genericSQLiteErr.Error()), "no such table") {
		t.Fatalf("expected non-schema sqlite error, got %v", genericSQLiteErr)
	}

	if isMissingCookieSchemaError(genericSQLiteErr) {
		t.Fatalf("expected generic sqlite error to not be classified as missing schema: %v", genericSQLiteErr)
	}
}
