// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package imagehostpolicy

import "testing"

func TestPolicyMetadataExposesOnlySupportedTrackerUploadHosts(t *testing.T) {
	t.Parallel()

	metadata := PolicyMetadata()
	ptpHosts := metadata.TrackerUploadHosts["PTP"]

	if !HostAllowed("pixhost", ptpHosts) {
		t.Fatalf("PTP upload hosts should include supported host pixhost: %v", ptpHosts)
	}
	if len(ptpHosts) != 1 {
		t.Fatalf("PTP upload hosts should only allow pixhost: %v", ptpHosts)
	}
	if HostAllowed("imgur", ptpHosts) {
		t.Fatalf("PTP upload hosts should exclude unsupported host: %v", ptpHosts)
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
