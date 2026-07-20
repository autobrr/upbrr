// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package webserver

import (
	"errors"
	"testing"

	"github.com/autobrr/upbrr/internal/webserver/jobs"
	"github.com/autobrr/upbrr/pkg/api"
)

func TestUploadReviewRegistryOwnerScopeSingleUseAndCopies(t *testing.T) {
	t.Parallel()
	registry := NewUploadReviewRegistry()
	snapshot := UploadReviewSnapshot{
		Execution: jobs.UploadExecutionSnapshot{
			PreparedGeneration: 7,
			RuntimeGeneration:  11,
			Input: api.UploadReviewInput{
				Release:  api.ReleaseRef{SourcePath: `C:\Example\Example.Release.2026.1080p-GRP.mkv`, Generation: 7},
				Trackers: []string{"EXAMPLE"},
			},
		},
		Review: api.UploadReview{Trackers: []api.TrackerReview{{Tracker: "EXAMPLE"}}},
	}
	token, err := registry.Issue("owner-a", snapshot)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	snapshot.Execution.Input.Trackers[0] = "MUTATED"
	if _, err := registry.Consume("owner-b", token); !errors.Is(err, ErrUploadReviewNotFound) {
		t.Fatalf("foreign Consume error = %v", err)
	}
	consumed, err := registry.Consume("owner-a", token)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed.Execution.Input.Trackers[0] != "EXAMPLE" || consumed.Review.Trackers[0].Tracker != "EXAMPLE" {
		t.Fatalf("snapshot was not isolated: %#v", consumed)
	}
	if _, err := registry.Consume("owner-a", token); !errors.Is(err, ErrUploadReviewNotFound) {
		t.Fatalf("second Consume error = %v", err)
	}
}

func TestUploadReviewRegistryRejectsMissingLineage(t *testing.T) {
	t.Parallel()
	if _, err := NewUploadReviewRegistry().Issue("owner", UploadReviewSnapshot{}); err == nil {
		t.Fatal("expected invalid lineage error")
	}
}
