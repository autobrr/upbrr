// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

// reviewBlurayReleases presents listings before optional detail work in both
// plain CLI and the TUI's shared question surface. Unattended runs never scan.
func (s *cliWorkflowSession) reviewBlurayReleases(ctx context.Context, reader *bufio.Reader) error {
	if s.intent.interaction == api.InteractionModeUnattended || s.uploadRequest.Bluray != nil {
		return nil
	}
	for {
		if s.current.Release == nil {
			return nil
		}
		bluray := s.current.Release.Release.ProviderMetadata.Bluray
		if bluray == nil || len(bluray.Candidates) == 0 || bluray.SelectedReleaseID != "" && !bluray.AutoSelected {
			return nil
		}
		var listing strings.Builder
		fmt.Fprintln(&listing, "Blu-ray.com releases")
		for i, c := range bluray.Candidates {
			score := "details not fetched"
			switch c.DetailsStatus {
			case "complete":
				score = fmt.Sprintf("score %.1f/100", c.Score)
			case "failed":
				score = "details unavailable"
			}
			fmt.Fprintf(&listing, "%d. %s | %s | %s | %s\n", i+1, c.Title, c.Country, c.Publisher, score)
			if c.DetailsStatus == "complete" {
				for _, note := range c.MatchNotes {
					fmt.Fprintf(&listing, "   %s\n", note)
				}
			}
		}
		fmt.Fprintln(&listing, "a. Fetch details and score all releases\nn. Skip Blu-ray.com selection")
		bindCLIQuestion(ctx, s.streams.out, s.current.Workflow.ID, s.current.Workflow.Revision, "", listing.String())
		answer, err := askCLIField(reader, s.streams.out, cliPrompt{
			Kind:     cliPromptText,
			Question: "Release number, a (all), or n (skip): ",
			Evidence: listing.String(),
			Help: "A number selects that edition and fetches its details if needed. a fetches details and scores all listed editions before another choice. n skips selection. " +
				"Use --bluray-release ID or URL for an exact edition on a later run, or --bluray-release auto to restore automatic matching.",
		})
		if err != nil {
			return err
		}
		var selection *api.BluraySelection
		var details *api.BlurayDetailRequest
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "a":
			details = &api.BlurayDetailRequest{All: true}
		case "n":
			selection = &api.BluraySelection{Mode: "skip"}
		default:
			n, err := strconv.Atoi(strings.TrimSpace(answer))
			if err != nil || n < 1 || n > len(bluray.Candidates) {
				fmt.Fprintln(s.streams.out, "Choose a listed release number, a, or n.")
				continue
			}
			selection = &api.BluraySelection{Mode: "manual", Release: bluray.Candidates[n-1].ReleaseID}
		}
		if s.current.FactInstructions == nil {
			return errors.New("upbrr: Blu-ray selection requires retained fact instructions")
		}
		instructions := s.current.FactInstructions.Instructions
		if selection != nil {
			instructions.Bluray = selection
		}
		input := api.PrepareInput{
			SourcePath:   s.intent.sourcePath,
			Intent:       api.PreparationIntentPreview,
			Instructions: instructions,
			Controls:     api.PreparationControls{Interaction: s.intent.interaction, BlurayDetails: details},
			Force:        true,
		}
		if err := s.continueUntilStable(ctx, api.ContinueReleaseWorkflowRequest{
			IdempotencyKey: s.nextIdempotencyKey("bluray"),
			Goal:           api.WorkflowGoalPrepared,
			Intent:         api.WorkflowIntent{FactInstructions: &instructions, Preparation: &input},
		}); err != nil {
			return err
		}
		if selection != nil {
			s.uploadRequest.Bluray = selection
			return nil
		}
	}
}
