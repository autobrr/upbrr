package a4k

import (
	"context"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestCheckRequirementsUsesPreparedVideoBitrate(t *testing.T) {
	base := api.TrackerValidationSubject{
		Type:    "ENCODE",
		Source:  "WEB-DL",
		Release: api.ReleaseInfo{Resolution: "2160p"},
		Assessments: api.ReleaseAssessments{VideoBitrate: api.VideoBitrateAssessment{
			Status:        api.VideoBitrateStatusPresent,
			BitsPerSecond: 12_000_000,
		}},
	}
	if failures, err := checkRequirements(context.Background(), base, nil); err != nil || len(failures) != 0 {
		t.Fatalf("prepared bitrate validation failures=%v err=%v", failures, err)
	}

	base.Assessments.VideoBitrate.Status = api.VideoBitrateStatusUnavailable
	failures, err := checkRequirements(context.Background(), base, nil)
	if err != nil || len(failures) != 1 || failures[0].Rule != "a4k_bitrate" {
		t.Fatalf("unavailable bitrate validation failures=%v err=%v", failures, err)
	}
}
