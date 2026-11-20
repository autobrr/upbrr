package releaseworkflow

import (
	"context"
	"errors"
	"testing"

	"github.com/autobrr/upbrr/pkg/api"
)

type comparisonDiscoveryFake struct {
	comparisonMediaFake
	discoveries int
	failure     error
}

func (b *comparisonDiscoveryFake) DiscoverComparisonSource(_ context.Context, _ api.ReleaseRef, id, _ string, _ bool) (api.ComparisonSourceDiscovery, error) {
	b.discoveries++
	return api.ComparisonSourceDiscovery{
		SourceID:    id,
		Fingerprint: "discovery-1",
		Candidates: []api.ComparisonProgrammeCandidate{{
			ID:              "title-1",
			Kind:            "dvd",
			DiscID:          "disc-1",
			DiscName:        "Disc 1",
			Label:           "Title 1",
			DurationSeconds: 120,
			Supported:       true,
		}},
	}, b.failure
}

func TestComparisonDiscoveryPreservesAppliedIntentAndReview(t *testing.T) {
	module, repository, dupes, current := newDuplicateReuseWorkflow(t)
	builder := &comparisonDiscoveryFake{}
	module.mediaBuilder = builder
	current = executeCommand(t, module, EditComparisonsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Instructions:     api.ComparisonInstructions{Action: "configure", Plan: reviewedComparisonFixture(t)},
	})
	current = executeCommand(t, module, EditComparisonsCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Media:            current.Workflow.Media,
		Instructions:     api.ComparisonInstructions{Action: "approve", ExpectedFingerprint: current.Media.Comparisons.Fingerprint},
	})
	before, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	checks := dupes.full
	request := api.DiscoverReleaseWorkflowComparisonSourceRequest{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		IdempotencyKey:   "discover-1",
		Media:            current.Workflow.Media,
		SourceID:         "dvd",
		Path:             "Example.DVD",
	}
	discovery, err := module.DiscoverComparisonSource(t.Context(), testOwnerID, request)
	if err != nil || discovery.SourceID != "dvd" || len(discovery.Candidates) != 1 {
		t.Fatalf("discovery=%+v err=%v", discovery, err)
	}
	if builder.discoveries != 1 || builder.edits != 2 || builder.uploads != 0 || dupes.full != checks {
		t.Fatal("read-only discovery performed comparison/tracker/hosting work")
	}
	stale := request
	stale.ExpectedRevision--
	if _, err := module.DiscoverComparisonSource(t.Context(), testOwnerID, stale); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale discovery authority accepted: %v", err)
	}
	builder.failure = errors.New("source unavailable")
	if _, err := module.DiscoverComparisonSource(t.Context(), testOwnerID, request); !errors.Is(err, builder.failure) {
		t.Fatalf("discovery failure lost: %v", err)
	}
	after, err := repository.Load(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Workflow.Revision != before.Workflow.Revision || *after.Workflow.Media != *before.Workflow.Media {
		t.Fatal("discovery advanced applied workflow/media authority")
	}
	media := after.Media[after.Workflow.Media.ID]
	if media.Comparisons == nil || media.Comparisons.ApprovedFingerprint != current.Media.Comparisons.ApprovedFingerprint {
		t.Fatal("discovery destroyed prior review")
	}
}
