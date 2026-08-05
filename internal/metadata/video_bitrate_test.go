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
			name: "overall minus every parseable audio track",
			json: `{"media":{"track":[{"@type":"General","OverallBitRate":"20 Mb/s"},{"@type":"Video"},{"@type":"Audio","BitRate":"2 Mb/s"},{"@type":"Audio","BitRate":"bad"},{"@type":"Audio","BitRate":"1 Mb/s"}]}}`,
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
