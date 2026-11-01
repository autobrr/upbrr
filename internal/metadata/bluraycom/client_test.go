// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package bluraycom

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

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

func TestFetchRejectsTruncatedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	defer server.Close()

	client := NewClient(server.Client())
	_, err := client.fetch(context.Background(), server.URL, "")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected truncation error, got %v", err)
	}
}

func TestLookupSkipsInvalidIMDBID(t *testing.T) {
	for _, tc := range []struct {
		name   string
		imdbID int
	}{
		{name: "zero", imdbID: 0},
		{name: "negative", imdbID: -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := NewClient(nil)
			result, err := client.Lookup(context.Background(), LookupInput{IMDBID: tc.imdbID})
			if err != nil {
				t.Fatalf("lookup invalid imdb id: %v", err)
			}
			if result != nil {
				t.Fatalf("expected nil result for invalid imdb id, got %#v", result)
			}
		})
	}
}

func TestParseReleaseInfoFallbackCapsSections(t *testing.T) {
	html := `
		<table>
			<tr><td><h3>Blu-ray Editions A</h3></td></tr>
			<tr><td><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example-A/101/" title="Example A">A</a></td></tr>
			<tr><td><h3>Blu-ray Editions B</h3></td></tr>
			<tr><td><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example-B/102/" title="Example B">B</a></td></tr>
			<tr><td><h3>Blu-ray Editions C</h3></td></tr>
			<tr><td><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example-C/103/" title="Example C">C</a></td></tr>
			<tr><td><h3>Blu-ray Editions D</h3></td></tr>
			<tr><td><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example-D/104/" title="Example D">D</a></td></tr>
		</table>`

	releases, err := parseReleaseInfo(html, LookupInput{DiscType: "BDMV", Is3D: "yes"})
	if err != nil {
		t.Fatalf("parse release info: %v", err)
	}
	if len(releases) != maxFallbackReleaseSections {
		t.Fatalf("expected fallback cap %d, got %d: %#v", maxFallbackReleaseSections, len(releases), releases)
	}
	if releases[0].ReleaseID != "101" || releases[2].ReleaseID != "103" {
		t.Fatalf("unexpected fallback releases: %#v", releases)
	}
}

func TestParseReleaseInfoEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name        string
		html        string
		input       LookupInput
		wantCount   int
		wantRelease string
	}{
		{
			name:      "malformed html returns empty",
			html:      `<table><tr><td><h3><`,
			input:     LookupInput{DiscType: "BDMV", Resolution: "1080p"},
			wantCount: 0,
		},
		{
			name:      "missing sections returns empty",
			html:      `<table><tr><td><h3>Streaming Editions</h3><a href="https://www.blu-ray.com/movies/Example/100/">Example</a></td></tr></table>`,
			input:     LookupInput{DiscType: "DVD"},
			wantCount: 0,
		},
		{
			name:      "empty release list returns empty",
			html:      `<table><tr><td><h3>Blu-ray Editions</h3></td></tr><tr><td>No releases</td></tr></table>`,
			input:     LookupInput{DiscType: "BDMV", Resolution: "1080p"},
			wantCount: 0,
		},
		{
			name: "duplicate releases are deduped",
			html: `<table>
				<tr><td><h3>Blu-ray Editions</h3></td></tr>
				<tr><td><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example/123/" title="Example">Example</a></td></tr>
				<tr><td><img width="18" height="12" title="United States"><a href="https://www.blu-ray.com/movies/Example/123/" title="Example Duplicate">Example Duplicate</a></td></tr>
			</table>`,
			input:       LookupInput{DiscType: "BDMV", Resolution: "1080p"},
			wantCount:   1,
			wantRelease: "123",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			releases, err := parseReleaseInfo(tc.html, tc.input)
			if err != nil {
				t.Fatalf("parse release info: %v", err)
			}
			if len(releases) != tc.wantCount {
				t.Fatalf("expected %d releases, got %d: %#v", tc.wantCount, len(releases), releases)
			}
			if tc.wantRelease != "" && releases[0].ReleaseID != tc.wantRelease {
				t.Fatalf("expected release %q, got %#v", tc.wantRelease, releases[0])
			}
		})
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

func TestExtractCoverImagesPairsPreviewsWithFullImages(t *testing.T) {
	htmlText := `<div id="triggers">
		<img id="frontimage_overlay" src="https://img.example/front_large.jpg?t=1">
		<img id="slipimage_overlay" src="https://img.example/slip_large.jpg?t=1">
		<img id="unusedimage_overlay" src="https://img.example/unused_large.jpg?t=1">
	</div>
	<script>
		$('#frontimage_container').append('<img id="frontimage" src="https://img.example/front.jpg?t=1">');
		$('#backimage_container').append('<img id="largebackimage" src="https://img.example/back.jpg?t=1">');
		$('#slipimage_container').append('<img id="slipimage" src="https://img.example/slip.jpg?t=1">');
	</script>`
	root, err := html.Parse(strings.NewReader(htmlText))
	if err != nil {
		t.Fatalf("parse html: %v", err)
	}
	want := []api.BlurayImage{
		{
			Kind:       "front",
			URL:        "https://img.example/front.jpg",
			PreviewURL: "https://img.example/front_large.jpg",
		},
		{Kind: "back", URL: "https://img.example/back.jpg"},
		{
			Kind:       "slip",
			URL:        "https://img.example/slip.jpg",
			PreviewURL: "https://img.example/slip_large.jpg",
		},
	}
	if got := extractCoverImages(htmlText, root); !slices.Equal(got, want) {
		t.Fatalf("cover images = %#v, want %#v", got, want)
	}
}

func TestParseReleaseDetailsEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name             string
		html             string
		wantSpecsMissing bool
		wantImages       int
	}{
		{
			name:             "malformed html has missing specs",
			html:             `<table><tr><td><span`,
			wantSpecsMissing: true,
		},
		{
			name:             "missing specs node has missing specs",
			html:             `<html><body>No specs</body></html>`,
			wantSpecsMissing: true,
		},
		{
			name:             "empty specs node has missing specs",
			html:             `<table><tr><td width="228px" style="font-size: 12px"></td></tr></table>`,
			wantSpecsMissing: true,
		},
		{
			name: "truncated image markup is ignored",
			html: `<table><tr><td width="228px" style="font-size: 12px">
				<span class="subheading">Video</span>Codec: AVC / MPEG-4 AVC<br>Resolution: 1080p<br>
				<span class="subheading">Audio</span><div id="longaudio">English: DTS-HD Master Audio 5.1</div>
				<span class="subheading">Discs</span>Single disc (1 BD-50)
			</td></tr></table>
			<script>$("#x").append('<img id="frontimage" src="https://img.example/front.jpg?x=1"')</script>`,
			wantSpecsMissing: false,
			wantImages:       0,
		},
		{
			name: "empty image URL is ignored",
			html: `<table><tr><td width="228px" style="font-size: 12px">
				<span class="subheading">Video</span>Codec: AVC / MPEG-4 AVC<br>Resolution: 1080p<br>
				<span class="subheading">Audio</span><div id="longaudio">English: DTS-HD Master Audio 5.1</div>
				<span class="subheading">Discs</span>Single disc (1 BD-50)
			</td></tr></table>
			<div class="simple_overlay"><img id="frontimage" src=""></div>`,
			wantSpecsMissing: false,
			wantImages:       0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release, err := parseReleaseDetails(tc.html, sampleCandidate())
			if err != nil {
				t.Fatalf("parse details: %v", err)
			}
			if release.SpecsMissing != tc.wantSpecsMissing {
				t.Fatalf("expected SpecsMissing=%v, got %#v", tc.wantSpecsMissing, release)
			}
			if len(release.CoverImages) != tc.wantImages {
				t.Fatalf("expected %d cover images, got %#v", tc.wantImages, release.CoverImages)
			}
		})
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
		Audio: []discparse.BDAudio{{
			Language: "English",
			Codec:    "Dolby TrueHD",
			Channels: "7.1",
			Atmos:    "Atmos",
		}},
		Subtitles: []string{
			"English",
		},
	})
	if candidate.Score < 94.5 {
		t.Fatalf("expected high score, got %.1f notes=%v", candidate.Score, candidate.MatchNotes)
	}
}

func TestScoreCandidateEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mutate           func(*api.BlurayReleaseCandidate)
		bdinfo           *discparse.BDInfo
		wantScore        float64
		wantSpecsMissing bool
		wantNote         string
	}{
		{
			name: "partial audio match",
			mutate: func(candidate *api.BlurayReleaseCandidate) {
				candidate.Specs = matchingSpecs()
				candidate.Specs.Audio = []string{"English: Dolby TrueHD 5.1"}
			},
			bdinfo:    matchingBDInfo(),
			wantScore: 95,
			wantNote:  "audio full=0 partial=1 missing=0 extra=0 (-5.0)",
		},
		{
			name: "missing subtitles",
			mutate: func(candidate *api.BlurayReleaseCandidate) {
				candidate.Specs = matchingSpecs()
				candidate.Specs.Subtitles = nil
			},
			bdinfo:           matchingBDInfo(),
			wantScore:        90,
			wantSpecsMissing: true,
			wantNote:         "missing subtitle specs (-5)",
		},
		{
			name: "disc format mismatch",
			mutate: func(candidate *api.BlurayReleaseCandidate) {
				candidate.Specs = matchingSpecs()
				candidate.Specs.Discs.Format = "BD-50"
			},
			bdinfo:    matchingBDInfo(),
			wantScore: 50,
			wantNote:  `disc format mismatch "BD-50" vs BD-100 (-50)`,
		},
		{
			name:             "missing specs floors score",
			bdinfo:           matchingBDInfo(),
			wantScore:        0,
			wantSpecsMissing: true,
			wantNote:         "missing video specs (-5)",
		},
		{
			name: "subtitle variant does not substring match",
			mutate: func(candidate *api.BlurayReleaseCandidate) {
				candidate.Specs = matchingSpecs()
				candidate.Specs.Subtitles = []string{"English SDH"}
			},
			bdinfo:    matchingBDInfo(),
			wantScore: 85,
			wantNote:  "subtitles matched=0 missing=1 extra=1 (-15.0)",
		},
		{
			name: "subtitle token order matches",
			mutate: func(candidate *api.BlurayReleaseCandidate) {
				candidate.Specs = matchingSpecs()
				candidate.Specs.Subtitles = []string{"SDH English"}
			},
			bdinfo: &discparse.BDInfo{
				SizeGB: 70,
				Video:  []discparse.BDVideo{{Codec: "HEVC", Resolution: "2160p"}},
				Audio: []discparse.BDAudio{{
					Language: "English",
					Codec:    "Dolby TrueHD",
					Channels: "7.1",
					Atmos:    "Atmos",
				}},
				Subtitles: []string{"English SDH"},
			},
			wantScore: 100,
			wantNote:  "subtitles matched=1 missing=0 extra=0 (-0.0)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := sampleCandidate()
			if tc.mutate != nil {
				tc.mutate(&candidate)
			}
			scoreCandidate(&candidate, tc.bdinfo)
			if candidate.Score != tc.wantScore {
				t.Fatalf("expected score %.1f, got %.1f notes=%v", tc.wantScore, candidate.Score, candidate.MatchNotes)
			}
			if candidate.SpecsMissing != tc.wantSpecsMissing {
				t.Fatalf("expected SpecsMissing=%v, got %#v", tc.wantSpecsMissing, candidate)
			}
			if tc.wantNote != "" && !hasNote(candidate.MatchNotes, tc.wantNote) {
				t.Fatalf("expected note %q in %v", tc.wantNote, candidate.MatchNotes)
			}
		})
	}
}

func TestScoreCandidateEqualScores(t *testing.T) {
	left := sampleCandidate()
	left.Specs = matchingSpecs()
	right := sampleCandidate()
	right.Specs = matchingSpecs()
	right.ReleaseID = "456"
	right.Title = "Example B"

	scoreCandidate(&left, matchingBDInfo())
	scoreCandidate(&right, matchingBDInfo())

	if left.Score != right.Score || left.Score != 100 {
		t.Fatalf("expected equal perfect scores, got left=%.1f right=%.1f", left.Score, right.Score)
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

func matchingSpecs() api.BluraySpecs {
	return api.BluraySpecs{
		Video:     api.BlurayVideoSpec{Codec: "HEVC / H.265", Resolution: "2160p"},
		Audio:     []string{"English: Dolby TrueHD Atmos 7.1"},
		Subtitles: []string{"English"},
		Discs:     api.BlurayDiscSpec{Format: "BD-100"},
	}
}

func matchingBDInfo() *discparse.BDInfo {
	return &discparse.BDInfo{
		SizeGB: 70,
		Video:  []discparse.BDVideo{{Codec: "HEVC", Resolution: "2160p"}},
		Audio: []discparse.BDAudio{{
			Language: "English",
			Codec:    "Dolby TrueHD",
			Channels: "7.1",
			Atmos:    "Atmos",
		}},
		Subtitles: []string{"English"},
	}
}

func hasNote(notes []string, want string) bool {
	return slices.Contains(notes, want)
}

func TestReleaseCalendarDates(t *testing.T) {
	for _, tc := range []struct{ name, link, text, want string }{
		{"supplied calendar link", "https://www.blu-ray.com/movies/releasedates.php?year=2020&month=12#December01", "Dec 1, 2020", "2020-12-01"},
		{"relative calendar link", "releasedates.php?year=2020&month=12#December01", "Dec 1, 2020", "2020-12-01"},
		{"different edition", "/movies/releasedates.php?year=2024&month=2#February29", "Feb 29, 2024", "2024-02-29"},
		{"invalid day", "/movies/releasedates.php?year=2023&month=2#February29", "Feb 29, 2023", ""},
		{"month disagrees", "/movies/releasedates.php?year=2020&month=11#December01", "Dec 1, 2020", ""},
		{"missing day", "/movies/releasedates.php?year=2020&month=12", "December 2020", ""},
		{"missing date", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, err := parseReleaseDetails(fmt.Sprintf(`<a href="%s">%s</a>`, tc.link, tc.text), sampleCandidate())
			if err != nil {
				t.Fatal(err)
			}
			if candidate.ReleaseDate != tc.want {
				t.Fatalf("release date = %q, want %q", candidate.ReleaseDate, tc.want)
			}
		})
	}
}

type releaseFixtureTransport func(*http.Request) (*http.Response, error)

func (f releaseFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLookupCountryAuthorityAndEditionDates(t *testing.T) {
	for _, tc := range []struct {
		marker, selected, want string
		threshold              float64
		lowUSA, tie            bool
	}{
		{"GER", "", "102", 80, false, false},
		{"GER", "", "102", 85, false, false},
		{"USA", "", "101", 100, true, false},
		{"", "", "101", 80, false, false},
		{"XYZ", "", "101", 80, false, false},
		{"GER", "", "", 100, false, false},
		{"GER", "", "102", 100, false, true},
		{"GER", "103", "103", 100, false, false},
		{"JPN", "", "", 80, false, false},
		{"GER", "101", "", 80, false, false},
	} {
		t.Run(tc.marker+tc.selected+fmt.Sprint(tc.threshold), func(t *testing.T) {
			client := NewClient(&http.Client{Transport: releaseFixtureTransport(func(r *http.Request) (*http.Response, error) {
				var body string
				switch r.URL.Path {
				case "/search/":
					body = `<div class="figure"><a class="alphaborder" href="https://www.blu-ray.com/movies/Example/100/">Example</a></div>`
				case "/products/menu_ajax.php":
					var catalogue strings.Builder
					catalogue.WriteString(`<h3>Blu-ray Editions</h3>`)
					countries := []string{"United States", "Germany", "Germany"}
					for _, i := range []int{2, 0, 1} {
						country := countries[i]
						fmt.Fprintf(&catalogue, `<img width="18" height="12" title="%s"><a href="https://www.blu-ray.com/movies/Edition/%d/" title="Edition %d">Edition</a>`, country, 101+i, i)
					}
					body = catalogue.String()
				default:
					codec := "HEVC / H.265"
					if strings.Contains(r.URL.Path, "103") && !tc.tie || strings.Contains(r.URL.Path, "101") && tc.lowUSA {
						codec = "AVC"
					}
					body = fmt.Sprintf(`<a href="/movies/releasedates.php?year=2020&month=12#December%02d">Release date</a><table><tr><td width="228px" style="font-size: 12px"><span class="subheading">Video</span>Codec: %s<br>Resolution: 2160p<br><span class="subheading">Audio</span><div id="longaudio">English: Dolby TrueHD Atmos 7.1</div><span class="subheading">Subtitles</span><div id="longsubs">English</div><span class="subheading">Discs</span>Single disc (1 BD-100)</td></tr></table>`, map[string]int{
						"/movies/Edition/101/": 1,
						"/movies/Edition/102/": 2,
						"/movies/Edition/103/": 3,
					}[r.URL.Path], codec)
					if strings.Contains(r.URL.Path, "102") && !tc.tie {
						body = strings.ReplaceAll(body, "English</div>", "French</div>")
					}
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
				}, nil
			})})
			result, err := client.Lookup(t.Context(), LookupInput{
				IMDBID:            1234567,
				SourcePath:        filepath.Join(t.TempDir(), "Example.Movie.2020."+tc.marker+".2160p.BluRay-GRP"),
				DiscType:          "BDMV",
				Resolution:        "2160p",
				BDInfo:            matchingBDInfo(),
				ScoreThreshold:    tc.threshold,
				SingleThreshold:   tc.threshold,
				SelectedReleaseID: tc.selected,
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.SelectedReleaseID != tc.want {
				t.Fatalf("selected = %q, want %q (%s)", result.SelectedReleaseID, tc.want, result.SelectionReason)
			}
			wantIDs := []string{"101", "102", "103"}
			wantScores := []float64{100, 85, 20}
			switch tc.marker {
			case "GER":
				wantIDs, wantScores = []string{"102", "103"}, []float64{85, 20}
				if tc.tie {
					wantScores = []float64{100, 100}
				}
			case "USA":
				wantIDs, wantScores = []string{"101"}, []float64{20}
			case "JPN":
				wantIDs, wantScores = nil, nil
			}
			if len(result.Candidates) != len(wantIDs) {
				t.Fatalf("country-scoped candidates = %#v, want IDs %v", result.Candidates, wantIDs)
			}
			for i, candidate := range result.Candidates {
				if candidate.ReleaseID != wantIDs[i] || candidate.Score != wantScores[i] {
					t.Fatalf("score/order = %#v, want IDs %v scores %v", result.Candidates, wantIDs, wantScores)
				}
				day := map[string]int{
					"101": 1,
					"102": 2,
					"103": 3,
				}[candidate.ReleaseID]
				if candidate.ReleaseDate != fmt.Sprintf("2020-12-%02d", day) {
					t.Fatalf("edition date = %q", candidate.ReleaseDate)
				}
			}
			if tc.marker == "JPN" && !strings.Contains(result.SelectionReason, "Japan") {
				t.Fatalf("missing country outcome: %s", result.SelectionReason)
			}
		})
	}
}

func TestSelectReleaseCountsUniqueCountryEditions(t *testing.T) {
	metadata := &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{
		{
			ReleaseID: "foreign",
			Country:   "Germany",
			Score:     100,
		},
		{
			ReleaseID: "only",
			Country:   "United States",
			Score:     0,
		},
		{
			ReleaseID: "only",
			Country:   "United States",
			Score:     0,
		},
	}}
	SelectRelease(metadata, LookupInput{
		SourcePath:      filepath.Join(t.TempDir(), "Example.Movie.2026.USA.BluRay-GRP"),
		ScoreThreshold:  100,
		SingleThreshold: 100,
	})
	if metadata.SelectedReleaseID != "only" || !metadata.AutoSelected || len(metadata.Candidates) != 1 || metadata.Threshold != 0 || metadata.SelectionReason != "single_country" {
		t.Fatalf("unique country edition not selected: %#v", metadata)
	}
}

func TestSelectReleaseCountryCorrectionPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, wantCountry, wantID string
		correction                *string
		wantCount                 int
	}{
		{"folder", "United States", "usa", nil, 1},
		{"country correction", "Germany", "ger", new("GER"), 1},
		{"country name correction", "Germany", "ger", new("Germany"), 1},
		{"explicit clear", "", "", new(""), 2},
		{"unknown correction", "", "", new("XYZ"), 2},
		{"playback region is not country", "", "", new("A"), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata := &api.BlurayMetadata{Candidates: []api.BlurayReleaseCandidate{
				{
					ReleaseID: "ger",
					Country:   "Germany",
					Score:     20,
				},
				{
					ReleaseID: "usa",
					Country:   "United States",
					Score:     0,
					Specs:     api.BluraySpecs{Playback: api.BlurayPlaybackSpec{Region: "B"}},
				},
			}}
			SelectRelease(metadata, LookupInput{
				SourcePath:      filepath.Join(t.TempDir(), "Example.Movie.2026.USA.BluRay-GRP"),
				CountryOverride: tc.correction,
				ScoreThreshold:  100,
				SingleThreshold: 100,
			})
			if metadata.Country != tc.wantCountry || metadata.SelectedReleaseID != tc.wantID || len(metadata.Candidates) != tc.wantCount {
				t.Fatalf("country correction result: country=%q selected=%q candidates=%d", metadata.Country, metadata.SelectedReleaseID, len(metadata.Candidates))
			}
		})
	}
}
