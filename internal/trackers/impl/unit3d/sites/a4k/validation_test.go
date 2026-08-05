// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

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

func TestValidationRequirementsBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		subject    api.TrackerValidationSubject
		wantRule   string
		wantPasses bool
	}{
		{
			name:       "2160p passes",
			subject:    api.TrackerValidationSubject{Type: "WEBDL", Release: api.ReleaseInfo{Resolution: "2160p"}, Assessments: api.ReleaseAssessments{VideoBitrate: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusPresent, BitsPerSecond: 10_000_000}}},
			wantPasses: true,
		},
		{
			name:     "2160i is not 2160p",
			subject:  api.TrackerValidationSubject{Type: "WEBDL", Release: api.ReleaseInfo{Resolution: "2160i"}},
			wantRule: "a4k_resolution",
		},
		{
			name:     "1080p is below exact resolution",
			subject:  api.TrackerValidationSubject{Type: "WEBDL", Release: api.ReleaseInfo{Resolution: "1080p"}},
			wantRule: "a4k_resolution",
		},
		{
			name:     "WEBRip token is rejected",
			subject:  api.TrackerValidationSubject{Type: "WEBRIP", Release: api.ReleaseInfo{Resolution: "2160p"}},
			wantRule: "a4k_webrip",
		},
		{
			name:     "WEB-Rip source token is rejected",
			subject:  api.TrackerValidationSubject{Source: "WEB-Rip", Release: api.ReleaseInfo{Resolution: "2160p"}},
			wantRule: "a4k_webrip",
		},
		{
			name:       "WEBRipper is not a WEBRip token",
			subject:    api.TrackerValidationSubject{Source: "WEBRipper", Release: api.ReleaseInfo{Resolution: "2160p"}, Assessments: api.ReleaseAssessments{VideoBitrate: api.VideoBitrateAssessment{Status: api.VideoBitrateStatusPresent, BitsPerSecond: 10_000_000}}},
			wantPasses: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failures, err := checkRequirements(context.Background(), test.subject, api.NopLogger{})
			if err != nil {
				t.Fatalf("check A4K requirements: %v", err)
			}
			if test.wantPasses {
				if len(failures) != 0 {
					t.Fatalf("unexpected failures: %#v", failures)
				}
				return
			}
			for _, failure := range failures {
				if failure.Rule == test.wantRule && failure.Disposition == api.RuleDispositionStrict {
					return
				}
			}
			t.Fatalf("missing strict failure %q in %#v", test.wantRule, failures)
		})
	}
}
