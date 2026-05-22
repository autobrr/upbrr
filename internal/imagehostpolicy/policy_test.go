// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehostpolicy

import "testing"

func TestPolicyMetadataExposesOnlySupportedTrackerUploadHosts(t *testing.T) {
	t.Parallel()

	metadata := PolicyMetadata()
	a4kHosts := metadata.TrackerUploadHosts["A4K"]

	if !HostAllowed("ptpimg", a4kHosts) {
		t.Fatalf("A4K upload hosts should include supported host ptpimg: %v", a4kHosts)
	}
	if HostAllowed("imgur", a4kHosts) {
		t.Fatalf("A4K upload hosts should exclude unsupported policy host imgur: %v", a4kHosts)
	}
	if HostAllowed("postimg", a4kHosts) {
		t.Fatalf("A4K upload hosts should exclude unsupported policy host postimg: %v", a4kHosts)
	}
}

func TestPolicyMetadataDefensivelyCopiesOwnedHosts(t *testing.T) {
	t.Parallel()

	metadata := PolicyMetadata()
	metadata.OwnedHosts["hdb"] = "OTHER"

	if got := OwnerForHost("hdb"); got != "HDB" {
		t.Fatalf("OwnerForHost(hdb) = %q, want HDB", got)
	}
}
