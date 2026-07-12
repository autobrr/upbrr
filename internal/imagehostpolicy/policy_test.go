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
	for _, host := range []string{"imgbb", "onlyimage", "ptscreens", "passtheimage"} {
		if !HostAllowed(host, ptpHosts) {
			t.Fatalf("PTP upload hosts should include supported host %s: %v", host, ptpHosts)
		}
	}
	if len(ptpHosts) != 5 {
		t.Fatalf("PTP upload hosts should only allow supported PTP hosts: %v", ptpHosts)
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

func TestPolicyMetadataExposesLostimgAsLSTOwnedUploadHost(t *testing.T) {
	t.Parallel()

	metadata := PolicyMetadata()
	lstHosts := metadata.TrackerUploadHosts["LST"]

	if !HostAllowed("lostimg", lstHosts) {
		t.Fatalf("LST upload hosts should include lostimg: %v", lstHosts)
	}
	if got := metadata.OwnedHosts["lostimg"]; got != "LST" {
		t.Fatalf("lostimg owner = %q, want LST", got)
	}
}

func TestPolicyMetadataExposesReelflixAsRFOwnedUploadHost(t *testing.T) {
	t.Parallel()

	metadata := PolicyMetadata()
	rfHosts := metadata.TrackerUploadHosts["RF"]

	if !HostAllowed("reelflix", rfHosts) {
		t.Fatalf("RF upload hosts should include reelflix: %v", rfHosts)
	}
	if got := metadata.OwnedHosts["reelflix"]; got != "RF" {
		t.Fatalf("reelflix owner = %q, want RF", got)
	}
}

func TestPolicyMetadataExposesUTPPMAsUTPOwnedUploadHost(t *testing.T) {
	t.Parallel()

	metadata := PolicyMetadata()
	utpHosts := metadata.TrackerUploadHosts["UTP"]

	if !HostAllowed("utppm", utpHosts) {
		t.Fatalf("UTP upload hosts should include utppm: %v", utpHosts)
	}
	if got := metadata.OwnedHosts["utppm"]; got != "UTP" {
		t.Fatalf("utppm owner = %q, want UTP", got)
	}
}
