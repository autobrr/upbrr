// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"testing"

	"github.com/autobrr/upbrr/internal/metadata/discparse"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestParseReleaseInfoFiltersMediaType(t *testing.T) {
	html := `
		<table>
			<tr><td><h3>Blu-ray Editions</h3></td></tr>
			<tr><td>
				<img width="18" height="12" title="United States">
				<a href="https://www.blu-ray.com/movies/Example-Blu-ray/123/" title="Example Blu-ray">Example</a>
				<small style="color: green">$10</small>
				<small style="color: #999999">Criterion</small>
			</td></tr>
			<tr><td><h3>4K Blu-ray Editions</h3></td></tr>
			<tr><td>
				<img width="18" height="12" title="United Kingdom">
				<a href="https://www.blu-ray.com/movies/Example-4K-Blu-ray/456/" title="Example 4K">Example 4K</a>
				<small style="color: green">$20</small>
				<small style="color: #999999">Arrow</small>
			</td></tr>
		</table>`

	releases, err := parseReleaseInfo(html, LookupInput{DiscType: "BDMV", Resolution: "2160p"})
	if err != nil {
		t.Fatalf("parse release info: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected one 4K release, got %d: %#v", len(releases), releases)
	}
	if releases[0].ReleaseID != "456" || releases[0].Publisher != "Arrow" || releases[0].Region != "GBR" {
		t.Fatalf("unexpected release: %#v", releases[0])
	}
}

func TestParseReleaseDetailsExtractsSpecsAndImages(t *testing.T) {
	html := `
		<table><tr><td width="228px" style="font-size: 12px">
			<span class="subheading">Video</span>
			Codec: HEVC / H.265<br>Resolution: 2160p<br>
			<span class="subheading">Audio</span>
			<div id="longaudio">English: Dolby TrueHD Atmos 7.1<br>French: DTS-HD Master Audio 5.1</div>
			<span class="subheading">Subtitles</span>
			<div id="longsubs">English, French</div>
			<span class="subheading">Discs</span>
			Two-disc set (1 BD-100, 1 BD-50)
			<span class="subheading">Playback</span>
			4K Blu-ray: Region A
		</td></tr></table>
		<script>$("#x").append('<img id="frontimage" src="https://img.example/front.jpg?x=1">')</script>`

	release, err := parseReleaseDetails(html, sampleCandidate())
	if err != nil {
		t.Fatalf("parse details: %v", err)
	}
	if release.Specs.Video.Codec != "HEVC / H.265" || release.Specs.Video.Resolution != "2160p" {
		t.Fatalf("unexpected video specs: %#v", release.Specs.Video)
	}
	if len(release.Specs.Audio) != 2 || len(release.Specs.Subtitles) != 2 {
		t.Fatalf("unexpected audio/sub specs: %#v", release.Specs)
	}
	if release.Specs.Discs.Format != "1 BD-100" || release.Specs.Playback.Region != "A" {
		t.Fatalf("unexpected disc/playback specs: %#v", release.Specs)
	}
	if len(release.CoverImages) != 1 || release.CoverImages[0].URL != "https://img.example/front.jpg" {
		t.Fatalf("unexpected cover images: %#v", release.CoverImages)
	}
}

func TestScoreCandidateHonorsThresholdShape(t *testing.T) {
	candidate := sampleCandidate()
	candidate.Specs.Video.Codec = "HEVC"
	candidate.Specs.Video.Resolution = "2160p"
	candidate.Specs.Discs.Format = "BD-100"
	candidate.Specs.Audio = []string{"English: Dolby TrueHD Atmos 7.1"}
	candidate.Specs.Subtitles = []string{"English"}

	scoreCandidate(&candidate, &discparse.BDInfo{
		SizeGB: 70,
		Video:  []discparse.BDVideo{{Codec: "HEVC", Resolution: "2160p"}},
		Audio:  []discparse.BDAudio{{Language: "English", Codec: "Dolby TrueHD", Channels: "7.1", Atmos: "Atmos"}},
		Subtitles: []string{
			"English",
		},
	})
	if candidate.Score < 94.5 {
		t.Fatalf("expected high score, got %.1f notes=%v", candidate.Score, candidate.MatchNotes)
	}
}

func sampleCandidate() api.BlurayReleaseCandidate {
	return api.BlurayReleaseCandidate{
		ReleaseID: "123",
		Title:     "Example",
		URL:       "https://www.blu-ray.com/movies/Example-Blu-ray/123/",
		Country:   "United States",
		Region:    "USA",
	}
}
