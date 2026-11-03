// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/internal/languageutil"
	"github.com/autobrr/upbrr/internal/trackers"
	trackerimpl "github.com/autobrr/upbrr/internal/trackers/impl"
	"github.com/autobrr/upbrr/pkg/api"
)

// reviewInputLanguages collects one manifest-bound source patch before tracker
// work. Canonical preparation then rebuilds all selected trackers' projections.
func (s *cliWorkflowSession) reviewInputLanguages(ctx context.Context, reader *bufio.Reader, debug bool, defaults []string) error {
	// Language eligibility already has an explicit non-submitting debug bypass.
	// Do not turn that diagnostic path into a mandatory source correction.
	if debug || s.current.Workflow.AllSelectedTrackersAlreadyUploaded() {
		return nil
	}
	selected := s.uploadRequest.Trackers
	if len(selected) == 0 && len(defaults) > 0 {
		registry, err := trackerimpl.NewRegistry()
		if err != nil {
			return fmt.Errorf("upbrr: resolve source language review trackers: %w", err)
		}
		selected = trackers.ResolveExplicitTrackersWithRegistry(defaults, s.logger, registry)
	}
	trackerIDs := languageReviewTrackerScope(normalizeCLIWorkflowTrackerIDs(selected), s.uploadRequest.TrackersRemove)
	if len(trackerIDs) == 0 {
		return nil
	}
	if err := s.continueUntilStable(ctx, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: s.nextIdempotencyKey("language-readiness"),
		Goal:           api.WorkflowGoalInputReady,
		Intent:         api.WorkflowIntent{TrackerIDs: trackerIDs},
	}); err != nil {
		return err
	}
	if s.current.InputReadiness == nil || s.current.Release == nil {
		return nil
	}
	bindCLIQuestion(ctx, s.streams.out, s.current.Workflow.ID, s.current.Workflow.Revision, "",
		fmt.Sprintf("Programme language review for inspected source generation %d.", s.current.Release.Release.Generation))
	corrections, err := collectCLIInputLanguages(
		reader,
		s.streams.out,
		s.intent.interaction,
		s.current.InputReadiness.Fields,
		s.current.Release.Release.Source.SourcePath,
		s.current.Release.Release.Media,
	)
	if err != nil {
		return err
	}
	origins, err := collectCLIInputAudioOrigins(reader, s.streams.out, s.intent.interaction,
		s.current.InputReadiness.Fields, s.current.Release.Release.Source.SourcePath, s.current.Release.Release.Media)
	if err != nil || (len(corrections) == 0 && len(origins) == 0) {
		return err
	}
	if s.current.Corrections == nil {
		return errors.New("upbrr: track language review requires a current correction revision")
	}
	patch := &api.ReleaseCorrectionPatch{ExpectedRevision: new(s.current.Corrections.Revision)}
	patch.Values.Metadata.TrackLanguages = corrections
	patch.Values.Metadata.TrackAudioOrigins = origins
	return s.continueUntilStable(ctx, api.ContinueReleaseWorkflowRequest{
		IdempotencyKey: s.nextIdempotencyKey("language-corrections"),
		Goal:           api.WorkflowGoalInputReady,
		Intent:         api.WorkflowIntent{TrackerIDs: trackerIDs, CorrectionPatch: patch},
	})
}

func languageReviewTrackerScope(selected []api.TrackerID, removed []string) []api.TrackerID {
	excluded := normalizeCLIWorkflowTrackerIDs(removed)
	return slices.DeleteFunc(slices.Clone(selected), func(id api.TrackerID) bool { return slices.Contains(excluded, id) })
}

func collectCLIInputLanguages(
	reader *bufio.Reader,
	output io.Writer,
	interaction api.InteractionMode,
	fields []api.InputReadinessFieldOutcome,
	sourcePath string,
	media api.MediaFacts,
) ([]api.TrackLanguageCorrection, error) {
	tracks := media.Tracks
	var corrections []api.TrackLanguageCorrection
	seen := make(map[string]bool)
	for _, field := range fields {
		if field.CorrectionField == nil || *field.CorrectionField != api.CorrectionFieldMetadataTrackLanguages ||
			field.Status == api.InputReadinessFieldReady || seen[field.TrackID] {
			continue
		}
		seen[field.TrackID] = true
		index := slices.IndexFunc(tracks, func(track api.MediaTrackFacts) bool { return track.ID == field.TrackID })
		if index < 0 || tracks[index].ManifestFingerprint == "" {
			return nil, errors.New("upbrr: track language review no longer matches inspected source; reprepare the input")
		}
		track := tracks[index]
		if interaction == api.InteractionModeUnattended {
			fmt.Fprint(output, safeTerminalText(fmt.Sprintf(
				"Unattended: programme track %s needs language evidence for %s. Correct with --track-languages '%s=LANGUAGE', or use --uac for source review.\n",
				track.ID,
				strings.Join(trackerIDStrings(field.TrackerIDs), ", "),
				track.ID,
			)))
			continue
		}
		prompt := cliProgrammeTrackPrompt(sourcePath, media, field, track)
		prompt.Question += " languages: "
		prompt.Help = fmt.Sprintf("Enter verified language names or codes, separated by commas. Do not guess; ZXX is only for verified non-linguistic audio.\n"+
			"This corrects this track's language evidence and rebuilds the selected trackers' assessments; it does not classify the soundtrack as original or dubbed.\n"+
			"To correct it later, use --track-languages %q with this inspected source. Use --input-only to inspect current track IDs after source changes.", track.ID+"=LANGUAGE")
		answer, err := askCLIField(reader, output, prompt)
		if err != nil {
			return nil, err
		}
		languages := languageutil.NormalizeLanguageList([]string{answer})
		if len(languages) == 0 || slices.ContainsFunc(languages, func(value string) bool {
			return !languageutil.IsIdentifiedLanguage(value)
		}) {
			return nil, fmt.Errorf("upbrr: programme track %s requires identified languages, not unknown or unspecified multilingual evidence", track.ID)
		}
		corrections = append(corrections, api.TrackLanguageCorrection{
			TrackID:             track.ID,
			Languages:           languages,
			ManifestFingerprint: track.ManifestFingerprint,
		})
	}
	return corrections, nil
}

func trackerIDStrings(ids []api.TrackerID) []string {
	values := make([]string, len(ids))
	for index, id := range ids {
		values[index] = string(id)
	}
	return values
}
