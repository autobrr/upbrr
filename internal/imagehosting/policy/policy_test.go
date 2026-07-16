// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package policy

import (
	"slices"
	"testing"
)

func TestKnownUploadHostsAreDeterministic(t *testing.T) {
	t.Parallel()

	hosts := KnownUploadHosts()
	if !slices.IsSorted(hosts) {
		t.Fatalf("upload hosts are not sorted: %v", hosts)
	}
	for _, host := range []string{"hdb", "lostimg", "pixhost", "reelflix", "thr"} {
		if !IsUploadHost(host) {
			t.Errorf("expected upload host %q", host)
		}
	}
}

func TestHostAllowedUsesGenericAllowlist(t *testing.T) {
	t.Parallel()

	if !HostAllowed("PIXHOST", []string{"pixhost"}) {
		t.Fatal("expected case-insensitive allowlist match")
	}
	if HostAllowed("imgbox", []string{"pixhost"}) {
		t.Fatal("unexpected allowlist match")
	}
	if !HostAllowed("imgbox", nil) {
		t.Fatal("empty allowlist must permit every host")
	}
}
