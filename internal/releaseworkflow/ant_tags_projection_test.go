// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package releaseworkflow

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/preparedrelease"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

type legacyANTTagsDefinition struct{ trackers.Definition }

func (legacyANTTagsDefinition) ProjectionQuestionnaire(trackers.PreparationInput) *api.TrackerQuestionnaire {
	return &api.TrackerQuestionnaire{Tracker: "ANT", Fields: []api.TrackerQuestionnaireField{{
		Key:      "tags",
		Kind:     "text",
		Required: true,
	}}}
}

func TestProjectionContinuationRefreshesPersistedANTTagsWithoutRepreparing(t *testing.T) {
	const trackerID api.TrackerID = "ANT"
	currentRegistry := trackerimpl.MustNewRegistry()
	descriptor, ok := currentRegistry.LookupDescriptor(string(trackerID))
	if !ok {
		t.Fatal("tracker missing")
	}
	oldRegistry := trackers.NewRegistry()
	descriptor.Definition = legacyANTTagsDefinition{Definition: descriptor.Definition}
	descriptor.Validation.ID = "standalone-ant-constructibility-v2"
	if err := oldRegistry.RegisterDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	oldProjector, err := trackers.NewWorkflowProjector(oldRegistry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	// Use a matching single-tracker catalog so the schema version is the sole catalog change.
	newRegistry := trackers.NewRegistry()
	descriptor, _ = currentRegistry.LookupDescriptor(string(trackerID))
	if err := newRegistry.RegisterDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	newProjector, err := trackers.NewWorkflowProjector(newRegistry, config.Config{}, api.NopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "projection-reload.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	repository, err := NewPersistentRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	preparations := 0
	preparer := testPreparer()
	preparer.SubjectFunc = func(_ context.Context, input api.UploadSubjectInput) (api.UploadSubject, error) {
		return api.UploadSubject{
			SourcePath:        input.Release.SourcePath,
			MediaInfoTextPath: filepath.Join(filepath.Dir(input.Release.SourcePath), "MEDIAINFO.txt"),
			ProviderMetadata: api.SourceScopedMetadata{TMDB: &api.TMDBMetadata{
				TMDBID: 123,
				Title:  "Example Movie",
				Year:   2026,
				Genres: "Science Fiction, Action, Adventure, Thriller",
			}},
			ReleaseName: "Example.Movie.2026.1080p.BluRay.x264-GRP",
			Source:      "BluRay",
			Type:        "ENCODE",
			Identity:    api.ExternalIdentity{Category: api.CanonicalCategoryMovie, TMDBID: 123},
			Release: api.ReleaseInfo{
				Title:      "Example Movie",
				Year:       2026,
				Category:   "MOVIE",
				Resolution: "1080p",
			},
		}, nil
	}
	base := preparer.PrepareFunc
	preparer.PrepareFunc = func(ctx context.Context, input api.PrepareInput) (api.PrepareResult, error) {
		preparations++
		result, err := base(ctx, input)
		result.Release.Compatibility.ContractVersion = preparedrelease.ContractVersion
		return result, err
	}
	first, err := New(repository, NewMemoryPrivateResourceStore(), preparer, WithTrackerProjectionBuilder(oldProjector), WithProcessEpoch("old-schema"))
	if err != nil {
		t.Fatal(err)
	}
	current := executeCommand(t, first, CreateWorkflowCommand{})
	current = executeCommand(t, first, PrepareReleaseCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Input:            api.PrepareInput{SourcePath: filepath.Join(t.TempDir(), "Example.Movie.mkv")},
	})
	current = executeCommand(t, first, ProjectTrackersCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		TrackerIDs:       []api.TrackerID{trackerID},
	})
	oldProjection := *current.Workflow.TrackerProjections
	if fields := current.Projections.Projections[0].Questionnaire; len(fields) != 1 || fields[0].Key != "tags" || !fields[0].Required || fields[0].Value != "" {
		t.Fatalf("fixture lacks legacy required-empty tags: %#v", fields)
	}
	if err := first.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(repository, NewMemoryPrivateResourceStore(), preparer, WithTrackerProjectionBuilder(newProjector), WithProcessEpoch("current-schema"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Shutdown(context.Background()) })
	current, err = reopened.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared := current.Release.Release
	updated, err := reopened.Continue(t.Context(), testOwnerID, api.ContinueReleaseWorkflowRequest{
		Authority:      &api.WorkflowAuthority{WorkflowID: current.Workflow.ID, ExpectedRevision: current.Workflow.Revision},
		Goal:           api.WorkflowGoalTrackersProjected,
		IdempotencyKey: "refresh-schema",
		Intent:         api.WorkflowIntent{TrackerIDs: []api.TrackerID{trackerID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Operation == nil {
		t.Fatal("legacy schema was treated as already reached")
	}
	waitForWorkflowOperation(
		t,
		reopened,
		current.Workflow.ID,
		updated.Operation.ID,
		func(status api.WorkflowOperationStatus) bool { return isTerminalProgressStatus(status.Status) },
	)
	updated, err = reopened.Current(t.Context(), testOwnerID, current.Workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Projections == nil || updated.Projections.ID == oldProjection.ID {
		t.Fatal("legacy projection was not replaced")
	}
	projection := updated.Projections.Projections[0]
	if len(projection.Questionnaire) != 1 || projection.Questionnaire[0].Key != "requestid" || projection.Questionnaire[0].Required || !projection.DupeReady ||
		!projection.UploadReady {
		t.Fatalf("usable genres retained a blocked projection: %+v", projection)
	}
	if preparations != 1 || updated.Release.Release.Generation != prepared.Generation || updated.Release.Release.Compatibility != prepared.Compatibility || prepared.Compatibility.ContractVersion != preparedrelease.ContractVersion {
		t.Fatal("schema upgrade changed prepared generation")
	}
}
