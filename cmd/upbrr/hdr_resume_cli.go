// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/autobrr/upbrr/internal/releaseworkflow"
	"github.com/autobrr/upbrr/pkg/api"
)

func runCLIHDRResume(ctx context.Context, service cliReleaseWorkflowCore, opts cliOptions, streams cliIO, logger api.Logger) error {
	active, err := service.GetActiveInput(ctx, cliWorkflowOwnerID)
	if err != nil {
		return fmt.Errorf("read retained HDR workflow: %w", err)
	}
	if active.State != api.ActiveInputActive || active.Current == nil || string(active.Current.Workflow.ID) != opts.HDRResumeWorkflow ||
		active.Current.HDRAnalysis == nil && active.Current.HDRPendingRequest == nil {
		return errors.New("HDR resume requires the exact retained active CLI workflow; closed or replaced generations cannot be resumed")
	}
	current := *active.Current
	key := strings.ToLower(rand.Text())
	if opts.HDRImageIDs != "" {
		if current.HDRAnalysis == nil {
			return errors.New("resume the pending HDR analysis before selecting its images")
		}
		ids := strings.Split(opts.HDRImageIDs, ",")
		artifacts := make([]api.PublicResourceID, len(ids))
		for i, id := range ids {
			artifacts[i] = api.PublicResourceID(strings.TrimSpace(id))
		}
		_, err := service.ExecuteReleaseWorkflow(ctx, cliWorkflowOwnerID, releaseworkflow.SetHDRAnalysisEnabledCommand{
			WorkflowID:       current.Workflow.ID,
			ExpectedRevision: current.Workflow.Revision,
			IdempotencyKey:   key,
			Enabled:          true,
			ArtifactIDs:      artifacts,
		})
		if err != nil {
			return fmt.Errorf("select retained HDR images: %w", err)
		}
		fmt.Fprintln(streams.out, "Selected current HDR artifacts for the next description generation.")
		return nil
	}
	instructions, err := cliHDRResumeInstructions(current, opts)
	if err != nil {
		return err
	}
	operation, err := service.StartReleaseWorkflow(ctx, cliWorkflowOwnerID, releaseworkflow.AnalyzeHDRCommand{
		WorkflowID:       current.Workflow.ID,
		ExpectedRevision: current.Workflow.Revision,
		Instructions:     instructions,
		IdempotencyKey:   key,
	})
	if err != nil {
		return fmt.Errorf("start retained HDR analysis: %w", err)
	}
	session := &cliWorkflowSession{
		core:           service,
		logger:         logger,
		streams:        streams,
		progressWriter: streams.out,
	}
	_, waitErr := session.waitForOperation(ctx, operation)
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	latest, err := service.CurrentReleaseWorkflow(loadCtx, cliWorkflowOwnerID, current.Workflow.ID)
	if waitErr != nil {
		if err == nil {
			printCLIHDRResult(streams.out, latest)
		}
		return waitErr
	}
	if err != nil {
		return fmt.Errorf("read HDR analysis outcome: %w", err)
	}
	printCLIHDRResult(streams.out, latest)
	if latest.HDRAnalysis == nil || latest.HDRAnalysis.Status != api.StageStatusCompleted {
		return errors.New("requested HDR outputs are incomplete; completed artifacts remain retained")
	}
	return nil
}

func printCLIHDRResult(out io.Writer, current releaseworkflow.CommandResult) {
	if current.HDRAnalysis == nil {
		return
	}
	var report strings.Builder
	fmt.Fprintf(&report, "HDR workflow: %s · attempt: %s · status: %s\n", current.Workflow.ID, current.HDRAnalysis.AttemptID, current.HDRAnalysis.Status)
	for _, target := range current.HDRAnalysis.Targets {
		for _, artifact := range target.Artifacts {
			fmt.Fprintf(&report, "HDR artifact: %s · %s\n", artifact.ID, artifact.Kind)
		}
	}
	if bridge := cliBridgeFromOutput(out); bridge != nil {
		bridge.RetainHDRReport(report.String())
	}
	fmt.Fprint(out, report.String())
}

func cliHDRResumeInstructions(current releaseworkflow.CommandResult, opts cliOptions) (api.HDRAnalysisInstructions, error) {
	if pending := current.HDRPendingRequest; pending != nil {
		if current.Release == nil ||
			pending.Release != (api.ReleaseRef{SourcePath: current.Release.Release.Source.SourcePath, Generation: current.Release.Release.Generation}) {
			return api.HDRAnalysisInstructions{}, errors.New("HDR resume cannot cross prepared generations")
		}
		if opts.HDRRetryModes != "" {
			return api.HDRAnalysisInstructions{}, errors.New("resume the pending HDR request before requesting another decoded retry")
		}
		request, err := pending.Normalize()
		if err != nil {
			return api.HDRAnalysisInstructions{}, fmt.Errorf("validate pending HDR resume: %w", err)
		}
		return request, nil
	}
	prior := current.HDRAnalysis
	if prior == nil || current.Release == nil ||
		prior.Release != (api.ReleaseRef{SourcePath: current.Release.Release.Source.SourcePath, Generation: current.Release.Release.Generation}) {
		return api.HDRAnalysisInstructions{}, errors.New("HDR resume cannot cross prepared generations")
	}
	request := api.HDRAnalysisInstructions{
		Release:        prior.Release,
		TargetIDs:      prior.TargetIDs,
		PeakSource:     prior.PeakSource,
		ProfileVersion: prior.ProfileVersion,
		Formats:        prior.Formats,
		Decoded:        prior.Decoded,
	}
	if opts.HDRRetryModes != "" {
		if opts.HDRRetryAttempt != prior.AttemptID {
			return api.HDRAnalysisInstructions{}, errors.New("decoded retry attempt is no longer current")
		}
		request.DecodedRetry = &api.HDRDecodedRetry{AnalysisAttemptID: opts.HDRRetryAttempt}
		for mode := range strings.SplitSeq(opts.HDRRetryModes, ",") {
			request.DecodedRetry.Modes = append(request.DecodedRetry.Modes, api.HDRDecodedMode(strings.TrimSpace(mode)))
		}
	}
	request, err := request.Normalize()
	if err != nil {
		return api.HDRAnalysisInstructions{}, fmt.Errorf("validate retained HDR resume: %w", err)
	}
	return request, nil
}
