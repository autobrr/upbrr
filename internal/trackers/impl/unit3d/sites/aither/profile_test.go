// SPDX-License-Identifier: GPL-2.0-or-later

package aither

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestSourceOnlyImageReusableRequiresAitherProvenanceForWsrvNL(t *testing.T) {
	t.Parallel()
	link := "https://wsrv.nl/?url=https%3A%2F%2Fexample.org%2Fshot.png"
	if !sourceOnlyImageReusable(link, []api.TrackerMetadata{{Tracker: "AITHER", Description: "[img]" + link + "[/img]"}}) {
		t.Fatal("Aither's own wsrv.nl image should remain usable on Aither")
	}
	if sourceOnlyImageReusable(link, []api.TrackerMetadata{{Tracker: "PTP", Description: "[img]" + link + "[/img]"}}) {
		t.Fatal("imported wsrv.nl image cannot be reused on Aither")
	}
	if !sourceOnlyImageReusable("https://wsrv.aither.cc/?url=shot.png", nil) {
		t.Fatal("Aither proxy image should remain usable on Aither")
	}
	if !sourceOnlyImageReusable(link, []api.TrackerMetadata{{Tracker: "AITHER", Description: "[comparison=A|B]\r\n" + link + "\r\n[/comparison]"}}) {
		t.Fatal("bare CRLF comparison image from Aither should remain usable on Aither")
	}
	if !sourceOnlyImageReusable(link, []api.TrackerMetadata{{Tracker: "AITHER", Description: "[comparison=A|B]" + link + "[/comparison]"}}) {
		t.Fatal("bare image at end of Aither comparison should remain usable on Aither")
	}
}
