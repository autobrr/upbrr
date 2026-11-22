// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package core

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/autobrr/upbrr/internal/config"
	"github.com/autobrr/upbrr/internal/services/db"
	"github.com/autobrr/upbrr/internal/trackers"

	"github.com/autobrr/upbrr/pkg/api"
)

func TestWorkflowPackCaptureUsesProjectedRequirementWithoutChangingOtherTrackerCapture(t *testing.T) {
	const packTracker api.TrackerID = "PACK"
	primary, secondary := "Movie.cd1.mkv", "Movie.cd2.mkv"
	subject := api.ScreenshotSubject{
		SourcePath:        "Example.Movie.2026",
		VideoPath:         primary,
		MediaInfoJSONPath: "primary.json",
		PackMembers: []api.ScreenshotPackMember{
			{ID: "first", Path: primary},
			{ID: "second", Path: secondary},
		},
	}
	for _, short := range []bool{false, true} {
		var planned []api.ScreenshotSubject
		capture := &workflowScreenshotFake{root: t.TempDir(), planFor: func(input api.ScreenshotSubject, count int) api.ScreenshotPlan {
			planned = append(planned, input)
			if input.PackMemberID != "" && (count != 2 || len(input.ManualFrames) != 0) {
				t.Errorf("pack capture should select two frames independently of manual release controls: %+v", input)
			}
			selections := []api.ScreenshotSelection{{Index: 1, TimestampSeconds: 60}}
			if input.PackMemberID != "" && (!short || input.PackMemberID != "second") {
				selections = append(selections, api.ScreenshotSelection{Index: 2, TimestampSeconds: 120})
			}
			return api.ScreenshotPlan{SuggestedSelections: selections}
		}}
		builder := workflowMediaBuilder{resolver: workflowMediaResolverFake{screenshotSubject: &subject}, screenshots: capture}
		snapshot, _, err := builder.Build(
			t.Context(),
			api.ReleaseRef{SourcePath: subject.SourcePath, Generation: 1},
			api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{
				{TrackerID: "AITHER", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}},
				{TrackerID: packTracker, Artifacts: api.TrackerArtifactRequirements{
					PackScreenshots: &api.PackScreenshotRequirement{MemberIDs: []string{"first", "second"}, CountPerMember: 2},
				}},
			}},
			api.MediaCaptureInstructions{Purpose: api.ScreenshotPurposeFinal},
			time.Now(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(planned) != 3 || planned[0].PackMemberID != "" || planned[1].VideoPath != primary ||
			planned[2].VideoPath != secondary || planned[2].MediaInfoJSONPath != "" {
			t.Fatalf("capture scope = %+v", planned)
		}
		counts := make(map[string]int)
		for _, artifact := range snapshot.Artifacts {
			counts[artifact.PackMemberID]++
		}
		if counts[""] != 1 {
			t.Fatalf("normal release screenshots = %d, want 1", counts[""])
		}
		if short {
			if len(snapshot.Artifacts) != 1 || len(snapshot.Failures) != 1 || snapshot.Failures[0].TrackerID != packTracker {
				t.Fatalf("short second file changed unrelated screenshots or lost pack tracker failure: %+v", snapshot)
			}
		} else if counts["first"] != 2 || counts["second"] != 2 || len(snapshot.Failures) != 0 {
			t.Fatalf("pack captures = %+v, failures = %+v", counts, snapshot.Failures)
		}
	}
}

// Round-trip the local and hosted associations through SQLite, then change the
// selected trackers without discarding the PTP assets for a future reopen.
func TestWorkflowReopenKeepsPTPScreenshotsAndHostedLinksScoped(t *testing.T) {
	t.Parallel()
	for _, pack := range []bool{false, true} {
		name := "disc"
		if pack {
			name = "pack"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			root := t.TempDir()
			source := filepath.Join(root, "Example.Release.2026")
			dbPath := filepath.Join(root, "workflow.db")
			repo, err := db.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = repo.Close() }()
			if err := repo.Migrate(); err != nil {
				t.Fatal(err)
			}
			release := api.ReleaseRef{SourcePath: source, Generation: 1}
			binding := api.PreparedMediaBinding{
				SourcePath: source,
 PreparedMediaFingerprint: "prepared",
 PreparedGeneration: 1,
				CompatibilityKey: api.MediaCompatibilityKey(workflowMediaTestSHA256(name)),
			}
			subject := api.ScreenshotSubject{
SourcePath: source,
 MediaBinding: binding,
				Discs:       []api.ScreenshotDiscSubject{{ID: "primary"}, {ID: "extra"}},
				PackMembers: []api.ScreenshotPackMember{{ID: "first"}, {ID: "second"}},
			}
			cfg := config.Config{
				MainSettings: config.MainSettingsConfig{DBPath: dbPath},
				ImageHosting: config.ImageHostingConfig{Host1: "pixhost"},
			}
			account, err := workflowMediaHostAccountScope(cfg, "pixhost")
			if err != nil {
				t.Fatal(err)
			}
			registry := mediaImageHostRegistry(t)
			if err := registry.RegisterDescriptor(trackers.Descriptor{
				Name: "PTP",
 Definition: mediaImageHostDefinition("PTP"),
 Family: trackers.FamilyStandalone,
				BaseURL: "https://ptp.example.invalid",
 UploadContentMode: trackers.UploadContentModeScreenshots,
				ImageHost: &trackers.ImageHostPolicy{AllowedHosts: []string{"pixhost"}},
			}); err != nil {
				t.Fatal(err)
			}
			builder := workflowMediaBuilder{
config: cfg,
 resolver: workflowMediaResolverFake{screenshotSubject: &subject},
				media: &mediaModule{
cfg: cfg,
 repo: repo,
 mediaReuse: repo,
 registry: registry,
},
}
			other := api.TrackerReleaseProjection{TrackerID: "ONE", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}}
			ptp := api.TrackerReleaseProjection{TrackerID: "PTP", Artifacts: api.TrackerArtifactRequirements{ScreenshotCount: 1}}
			if pack {
				ptp.Artifacts.ScreenshotCount = 0
				ptp.Artifacts.PackScreenshots = &api.PackScreenshotRequirement{MemberIDs: []string{"first", "second"}, CountPerMember: 1}
			} else {
				ptp.Artifacts.DiscScreenshots = &api.DiscScreenshotRequirement{DiscIDs: []string{"primary", "extra"}, AdditionalCount: 2}
			}
			both := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{other, ptp}}
			onlyOther := api.TrackerReleaseProjectionSet{Projections: []api.TrackerReleaseProjection{other}}
			now := time.Now()
			saved, private, err := builder.mediaMutationBase(release, both, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			private.screenshotSubject = subject
			for i := range 3 {
				id := api.PublicResourceID(fmt.Sprintf("screen-%d", i))
				hostedID := api.PublicResourceID(fmt.Sprintf("hosted-%d", i))
				image := api.ScreenshotImage{
Path: filepath.Join(root, string(id)+".png"),
 Purpose: api.ScreenshotPurposeFinal,
 Index: i,
}
				if pack && i > 0 {
					// Equal frame metadata and bytes must not collapse two pack members.
					image.Index = 0
					image.PackMemberID = subject.PackMembers[i-1].ID
				} else if !pack {
					image.DiscID = "primary"
					if i > 0 {
						image.DiscID, image.AdditionalDisc = "extra", true
					}
				}
				if err := os.WriteFile(image.Path, []byte("same image bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
				artifact := publicMediaArtifact(saved.CaptureFingerprint, "screen", i, api.MediaArtifactScreenshot, image.Purpose, image)
				artifact.ID = id
				url := "https://images.invalid/" + string(id) + ".png"
				saved.Artifacts = append(saved.Artifacts, artifact, api.MediaArtifact{
					ID: hostedID,
 Kind: api.MediaArtifactHostedImage,
 Purpose: image.Purpose,
 Selected: true,
					Order: i,
 Source: string(id),
 Host: "pixhost",
 URL: url,
				})
				private.ArtifactImages[id] = image
				private.HostedSources[hostedID] = id
				private.HostedImages[hostedID] = api.UploadedImageLink{
					Host: "pixhost",
 ImagePath: image.Path,
 UsageScope: "global",
 AccountScope: account,
 RawURL: url,
 UploadedAt: now,
				}
			}
			saved.ID, saved.WorkflowID, saved.Revision = "media", "workflow", 1
			if err := builder.RecordReusableMedia(ctx, saved, private); err != nil {
				t.Fatal(err)
			}
			ordinary, ordinaryPrivate, err := builder.restoreCachedMedia(ctx, release, onlyOther, nil)
			if err != nil || len(ordinary.Artifacts) != 2 {
				t.Fatalf("non-PTP reopened media = %+v, err = %v", ordinary.Artifacts, err)
			}
			ordinary.ID, ordinary.WorkflowID, ordinary.Revision = saved.ID, saved.WorkflowID, 2
			if err := builder.RecordReusableMedia(ctx, ordinary, ordinaryPrivate); err != nil {
				t.Fatal(err)
			}
			release.Generation++
			subject.MediaBinding.PreparedGeneration = release.Generation
			subject.MediaBinding.PreparedMediaFingerprint = "prepared-again"
			reopened, restored, err := builder.restoreCachedMedia(ctx, release, both, nil)
			if err != nil || len(reopened.Artifacts) != 6 || reopened.Status != api.StageStatusCompleted {
				t.Fatalf("mixed reopened media = %+v, err = %v", reopened, err)
			}
			attempts, ready := builder.restoredHostedImageAttemptsForSubject(ctx, reopened, restored, both.Projections, api.UploadSubject{})
			if !ready || len(attempts) != 2 {
				t.Fatalf("reopened cached hosted attempts = %+v, ready = %t", attempts, ready)
			}
			expected := [][]string{{"https://images.invalid/screen-0.png"}, {"https://images.invalid/screen-1.png", "https://images.invalid/screen-2.png"}}
			if !pack {
				expected[1] = append(slices.Clone(expected[0]), expected[1]...)
			}
			for i, attempt := range attempts {
				var urls []string
				for _, result := range attempt.Results {
					urls = append(urls, result.URL)
				}
				slices.Sort(urls)
				if !slices.Equal(urls, expected[i]) {
					t.Fatalf("tracker %s URLs = %v, want %v", both.Projections[i].TrackerID, urls, expected[i])
				}
			}
			if err := os.Remove(private.ArtifactImages["screen-2"].Path); err != nil {
				t.Fatal(err)
			}
			incomplete, _, err := builder.restoreCachedMedia(ctx, release, both, nil)
			if err != nil || len(incomplete.Artifacts) != 4 || incomplete.Status != api.StageStatusBlocked {
				t.Fatalf("missing scoped capture should leave its link unavailable and coverage blocked: %+v, err = %v", incomplete, err)
			}
		})
	}
}
