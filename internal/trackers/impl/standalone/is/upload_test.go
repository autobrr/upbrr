// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package is

import "testing"

func TestSuccessfulUploadResponseExtractsTorrentID(t *testing.T) {
	id, ok := successfulUploadResponse("https://immortalseed.me/details.php?hash=abc123", "")
	if !ok {
		t.Fatal("expected upload success")
	}
	if id != "abc123" {
		t.Fatalf("expected torrent id abc123, got %q", id)
	}
}

func TestSuccessfulUploadResponseAcceptsThankYouText(t *testing.T) {
	id, ok := successfulUploadResponse("https://immortalseed.me/upload.php", "<html>Thank you for uploading</html>")
	if !ok {
		t.Fatal("expected upload success")
	}
	if id != "" {
		t.Fatalf("expected no torrent id from success text, got %q", id)
	}
}

func TestSuccessfulUploadResponseRejectsMissingSuccessSignals(t *testing.T) {
	id, ok := successfulUploadResponse("https://immortalseed.me/upload.php", "<html>failed</html>")
	if ok {
		t.Fatalf("expected upload failure, got id %q", id)
	}
}
