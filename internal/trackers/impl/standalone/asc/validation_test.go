// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package asc

import (
	"strings"
	"testing"

	"github.com/autobrr/go-torrent/metainfo"
)

func TestValidateASCVideoNamesChecksSingleFileVOB(t *testing.T) {
	t.Parallel()

	const name = "Synthetic.Release.576p.DVDRip.DD2.0.MPEG2-GRP.VOB"
	info := metainfo.Info{Name: name, Length: 1}
	if err := validateASCVideoNames(info, "General\nComplete name : "+name, ""); err != nil {
		t.Fatalf("compliant VOB was rejected: %v", err)
	}
	info.Name = "Synthetic.Release.576p.DVDRip.MPEG2-GRP.VOB"
	if err := validateASCVideoNames(info, "", ""); err == nil || !strings.Contains(err.Error(), "audio token") {
		t.Fatalf("VOB without audio token should fail naming validation, got %v", err)
	}
}
