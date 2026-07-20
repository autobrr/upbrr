// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package jobs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSnapshotJSONContractAndZeroTimes(t *testing.T) {
	t.Parallel()
	upload, err := json.Marshal(TrackerUploadSnapshot{
		JobID:         "job",
		Trackers:      []TrackerUploadTrackerState{},
		FailedTrackers: []string{},
	})
	if err != nil {
		t.Fatalf("marshal upload: %v", err)
	}
	for _, field := range []string{`"jobID":"job"`, `"release":{"SourcePath":"","Generation":0}`, `"status":""`, `"currentTask":""`, `"trackers":[]`, `"failedTrackers":[]`, `"startedAt":""`, `"finishedAt":""`} {
		if !strings.Contains(string(upload), field) {
			t.Errorf("upload JSON %s missing %s", upload, field)
		}
	}
	dupe, err := json.Marshal(DupeCheckSnapshot{JobID: "job", Trackers: []DupeCheckTrackerState{}})
	if err != nil {
		t.Fatalf("marshal dupe: %v", err)
	}
	for _, field := range []string{`"jobID":"job"`, `"release":{"SourcePath":"","Generation":0}`, `"status":""`, `"trackers":[]`, `"completedCount":0`, `"totalCount":0`, `"startedAt":""`, `"finishedAt":""`} {
		if !strings.Contains(string(dupe), field) {
			t.Errorf("dupe JSON %s missing %s", dupe, field)
		}
	}
}
