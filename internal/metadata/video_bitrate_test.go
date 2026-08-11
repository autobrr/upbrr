package metadata

import (
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestVideoBitrateAssessment(t *testing.T) {
	tests := []struct {
		name string
		json string
		want api.VideoBitrateAssessment
	}{
		{
			name: "direct video track",
			json: `{"media":{"track":[{"@type":"Video","BitRate":"12 Mb/s"}]}}`,
			want: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusPresent, BitsPerSecond: 12_000_000},
		},
		{
			name: "invalid track then valid track",
			json: `{"media":{"track":[{"@type":"Video","BitRate":"bad"},{"@type":"Video","BitRate":"12 Mb/s"}]}}`,
			want: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusPresent, BitsPerSecond: 12_000_000},
		},
		{
			name: "invalid track then usable overall",
			json: `{"media":{"track":[{"@type":"General","OverallBitRate":"20 Mb/s"},{"@type":"Video","BitRate":"bad"},{"@type":"Audio","BitRate":"2 Mb/s"},{"@type":"Audio","BitRate":"1 Mb/s"}]}}`,
			want: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusPresent, BitsPerSecond: 17_000_000},
		},
		{
			name: "invalid track with no derived evidence",
			json: `{"media":{"track":[{"@type":"Video","BitRate":"bad"}]}}`,
			want: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusInvalid},
		},
		{
			name: "byte-per-second video track",
			json: `{"media":{"track":[{"@type":"Video","BitRate":"1.5 MB/s"}]}}`,
			want: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusPresent, BitsPerSecond: 12_000_000},
		},
		{
			name: "unparseable audio disables fallback",
			json: `{"media":{"track":[{"@type":"General","OverallBitRate":"20 Mb/s"},{"@type":"Video"},{"@type":"Audio","BitRate":"2 Mb/s"},{"@type":"Audio","BitRate":"bad"},{"@type":"Audio","BitRate":"1 Mb/s"}]}}`,
			want: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusUnavailable},
		},
		{
			name: "overall minus parseable audio tracks",
			json: `{"media":{"track":[{"@type":"General","OverallBitRate":"20 Mb/s"},{"@type":"Video"},{"@type":"Audio","BitRate":"2 Mb/s"},{"@type":"Audio","BitRate":"1 Mb/s"}]}}`,
			want: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusPresent, BitsPerSecond: 17_000_000},
		},
		{
			name: "unavailable",
			json: `{"media":{"track":[{"@type":"General"},{"@type":"Video"}]}}`,
			want: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusUnavailable},
		},
		{
			name: "invalid",
			json: `{"media":{"track":[{"@type":"General","OverallBitRate":"bad"},{"@type":"Video"}]}}`,
			want: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusInvalid},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := videoBitrateAssessment(mustParseMediaInfoDoc(tc.json))
			if got != tc.want {
				t.Fatalf("assessment = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestParseMediaInfoBitrate(t *testing.T) {
	tests := []struct {
		name   string
		value  any
		want   int64
		wantOK bool
	}{
		{name: "empty", wantOK: false},
		{
			name:   "bad",
			value:  "bad",
			wantOK: false,
		},
		{
			name:   "kbit",
			value:  "512 kbit/s",
			want:   512_000,
			wantOK: true,
		},
		{
			name:   "mbit",
			value:  "12 Mbit/s",
			want:   12_000_000,
			wantOK: true,
		},
		{
			name:   "gbit",
			value:  "1.5 Gbit/s",
			want:   1_500_000_000,
			wantOK: true,
		},
		{
			name:   "lowercase mb/s",
			value:  "12 mb/s",
			want:   12_000_000,
			wantOK: true,
		},
		{
			name:   "KB/s",
			value:  "512 KB/s",
			want:   4_096_000,
			wantOK: true,
		},
		{
			name:   "MB/s",
			value:  "1.5 MB/s",
			want:   12_000_000,
			wantOK: true,
		},
		{
			name:   "GB/s",
			value:  "2 GB/s",
			want:   16_000_000_000,
			wantOK: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseMediaInfoBitrate(tc.value)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("parseMediaInfoBitrate(%v) = (%d, %v), want (%d, %v)", tc.value, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
