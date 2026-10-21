// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/autobrr/upbrr/pkg/api"
)

type cliItemContextKey struct{}

func publishCLIInvocation(streams cliIO, args []string, submissionSuppressed bool) {
	bridge := cliBridgeFromOutput(streams.out)
	if bridge == nil {
		return
	}
	bridge.mu.Lock()
	view := bridge.latest
	bridge.mu.Unlock()
	view.Arguments = safeCLIInvocation(args)
	view.Debug = submissionSuppressed
	bridge.Publish(view)
}

func withCLIItemPresentation(ctx context.Context, streams cliIO, source string, total int) context.Context {
	bridge := cliBridgeFromOutput(streams.out)
	if bridge == nil {
		return ctx
	}
	bridge.mu.Lock()
	view := bridge.latest
	view.Item++
	view.ItemsTotal = total
	view.Result = ""
	view.ItemFailed = false
	view.WorkflowID, view.Revision, view.OperationID = "", 0, ""
	view.Source, view.Summary, view.Stage = source, "", "Preparing"
	view.Lanes = nil
	bridge.mu.Unlock()
	bridge.Publish(view)
	return context.WithValue(ctx, cliItemContextKey{}, view.Item)
}

func publishCLIItemResult(streams cliIO, err error) {
	bridge := cliBridgeFromOutput(streams.out)
	if bridge == nil {
		return
	}
	bridge.mu.Lock()
	view := bridge.latest
	bridge.mu.Unlock()
	if view.Result == "" {
		view.Result = "Item completed."
	}
	view.ItemFailed = err != nil
	if err != nil {
		view.Result = "Item failed or interrupted. Verify retained state before retrying."
	}
	bridge.Publish(view)
}

func publishCLITrackerDecline(streams cliIO, tracker api.TrackerID) {
	bridge := cliBridgeFromOutput(streams.out)
	if bridge == nil {
		return
	}
	bridge.mu.Lock()
	view := bridge.latest
	view.Lanes = slices.Clone(view.Lanes)
	bridge.mu.Unlock()
	for index := range view.Lanes {
		if view.Lanes[index].ID == string(tracker) {
			view.Lanes[index].State, view.Lanes[index].Reason = "Declined", "not_approved"
			view.Lanes[index].Status = cliLaneBlocked
		}
	}
	bridge.Publish(view)
}

func (s *cliWorkflowSession) publishDeclinedAction(action api.RequiredAction) {
	bridge := cliBridgeFromOutput(s.streams.out)
	if bridge == nil {
		return
	}
	bridge.mu.Lock()
	view := bridge.latest
	view.Lanes = slices.Clone(view.Lanes)
	bridge.mu.Unlock()
	view.Stage = "Declined"
	view.Result = "Required action declined; no new submission was started."
	state, reason := "Not submitted", "required_action_declined"
	if action.Kind == api.RequiredActionReconcileSubmission {
		view.Result = "External outcome remains unresolved; inspect remote effects before retrying."
		state, reason = "Unresolved external outcome", "reconciliation_required"
	}
	for index := range view.Lanes {
		lane := &view.Lanes[index]
		if action.TrackerID != "" && lane.ID != string(action.TrackerID) {
			continue
		}
		if action.Kind == api.RequiredActionApproveTrackers && !slices.ContainsFunc(action.Options, func(option api.RequiredActionOption) bool {
			return option.Value == lane.ID
		}) {
			continue
		}
		// A local No does not change retained remote/debug results or the
		// confirmed submission history. Global decisions also retain blockers
		// on sibling lanes outside the pending action.
		preserve := slices.ContainsFunc(s.current.Workflow.SubmissionExclusions, func(exclusion api.SubmissionExclusion) bool {
			return string(exclusion.TrackerID) == lane.ID
		})
		if result := s.current.UploadResult; result != nil {
			preserve = preserve || slices.ContainsFunc(result.Results, func(result api.UploadTrackerResult) bool {
				return string(result.TrackerID) == lane.ID
			})
		}
		if result := s.current.DryRun; result != nil {
			preserve = preserve || slices.ContainsFunc(result.Reports, func(report api.TrackerDryRunReport) bool {
				return string(report.TrackerID) == lane.ID
			})
		}
		if preserve || (action.TrackerID == "" && lane.Reason != "" && action.Kind != api.RequiredActionApproveTrackers) {
			continue
		}
		if action.Kind == api.RequiredActionApproveTrackers || action.Kind == api.RequiredActionAuthorizeRules {
			lane.State, lane.Reason = "Declined", "not_approved"
		} else {
			lane.State, lane.Reason = state, reason
		}
		lane.Status = cliLaneBlocked
		if action.Kind == api.RequiredActionReconcileSubmission {
			lane.Status = cliLanePending
		}
	}
	bridge.Publish(view)
}

// publishOperationPresentation refreshes retained workflow projections during
// polling, so new questionnaires and tracker names replace earlier authority.
// A failed presentation refresh keeps the last snapshot without stopping work.
func (s *cliWorkflowSession) publishOperationPresentation(ctx context.Context, operation api.WorkflowOperationStatus) {
	if cliBridgeFromOutput(s.streams.out) != nil {
		current, err := s.core.CurrentReleaseWorkflow(ctx, cliWorkflowOwnerID, operation.WorkflowID)
		if err != nil {
			if s.logger != nil {
				s.logger.Debugf("workflow presentation: state=refresh_failed decision=retain_snapshot error=%s", safeDiagnosticText(err.Error()))
			}
		} else {
			s.current = current
		}
	}
	s.current.Operation = &operation
	s.publishPresentation(ctx)
}

func (s *cliWorkflowSession) publishPresentation(ctx context.Context) {
	if s.streams.presenter == nil {
		return
	}
	item, _ := ctx.Value(cliItemContextKey{}).(uint64)
	view := cliView{
		Item:       item,
		WorkflowID: s.current.Workflow.ID,
		Revision:   s.current.Workflow.Revision,
		Source:     s.intent.sourcePath,
		Stage:      string(s.current.Workflow.Status),
		Debug:      s.submissionSuppressed,
	}
	if bridge := cliBridgeFromOutput(s.streams.out); bridge != nil {
		bridge.mu.Lock()
		view.Debug = view.Debug || bridge.latest.Debug
		bridge.mu.Unlock()
	}
	if s.current.Release != nil {
		preview := cliWorkflowMetadataPreview(s.current)
		var summary strings.Builder
		fmt.Fprintf(&summary, "Upload name: %s\n", preview.ReleaseName)
		if ids := metadataExternalIDLabels(preview.Identity); len(ids) > 0 {
			fmt.Fprintln(&summary, strings.Join(ids, " • "))
		}
		media := s.current.Release.Release.Media
		fmt.Fprintf(
			&summary,
			"Video: %s / %s / %s bit\nAudio: %s / %s\nAudio languages: %s\nSubtitle languages: %s\n",
			media.VideoCodec,
			media.VideoEncode,
			media.BitDepth,
			media.Audio,
			media.Channels,
			strings.Join(media.AudioLanguages, ", "),
			strings.Join(media.SubtitleLanguages, ", "),
		)
		view.Summary = summary.String()
	}
	selected := normalizeCLIWorkflowTrackerIDs(s.uploadRequest.Trackers)
	if s.current.Selection != nil {
		for _, id := range s.current.Selection.TrackerIDs {
			if !slices.Contains(selected, id) {
				selected = append(selected, id)
			}
		}
	}
	for _, exclusion := range s.current.Workflow.SubmissionExclusions {
		if !slices.Contains(selected, exclusion.TrackerID) {
			selected = append(selected, exclusion.TrackerID)
		}
	}
	if bridge := cliBridgeFromOutput(s.streams.out); bridge != nil {
		bridge.mu.Lock()
		if bridge.latest.Item == item && bridge.latest.WorkflowID == view.WorkflowID {
			for _, lane := range bridge.latest.Lanes {
				id := api.TrackerID(lane.ID)
				if !slices.Contains(selected, id) {
					selected = append(selected, id)
				}
			}
		}
		bridge.mu.Unlock()
	}
	view.Lanes = make([]cliLaneView, len(selected))
	for index, id := range selected {
		lane := cliLaneView{ID: string(id), State: "Unchecked"}
		if s.current.Selection != nil && !slices.Contains(s.current.Selection.TrackerIDs, id) {
			lane.State, lane.Reason = "Skipped", "excluded_from_current_selection"
			lane.Status = cliLaneBlocked
		}
		if projections := s.current.Projections; projections != nil {
			for _, projection := range projections.Projections {
				if projection.TrackerID != id {
					continue
				}
				if projection.Readiness != "" {
					lane.State = string(projection.Readiness)
				}
				switch projection.Readiness {
				case api.ReadinessStatusReady:
					lane.State = "Awaiting dupe check"
				case api.ReadinessStatusIneligible, api.ReadinessStatusBlocked:
					lane.Status = cliLaneBlocked
				case api.ReadinessStatusUnknown, api.ReadinessStatusStale, "":
				}
				lane.Detail = "Upload name: " + projection.UploadReleaseName
				for _, decision := range projection.PolicyDecisions {
					if decision.Blocking {
						if lane.Reason == "" {
							lane.Reason = decision.Code
							if message := strings.TrimSpace(decision.Message); message != "" {
								lane.Reason += ": " + message
							}
						}
						lane.Detail += "\nPolicy: " + decision.Code + ": " + decision.Message
					}
				}
				if len(projection.Failures) > 0 {
					lane.Status = cliLaneBlocked
					for _, failure := range projection.Failures {
						if lane.Reason == "" || failure.Failure.Code != api.OperationFailureNoEligibleTrackers {
							lane.Reason = string(failure.Failure.Code)
						}
						lane.Detail += "\nFailure: " + string(failure.Failure.Code)
					}
				}
				if firstPendingCLICompositeAction(projection.RequiredActions) != nil {
					lane.State, lane.Status = "Awaiting decision", cliLanePending
				}
			}
		}
		for _, outcome := range s.current.Continuation.TrackerOutcomes {
			if outcome.TrackerID != id {
				continue
			}
			switch {
			case outcome.UploadEligibility == api.UploadEligibilitySkipped:
				lane.State = "Skipped"
				lane.Status = cliLaneBlocked
			case outcome.Lifecycle == api.OperationLifecycleRunning:
				lane.State, lane.Status = "Running", cliLanePending
			case outcome.Lifecycle == api.OperationLifecycleQueued:
				lane.State, lane.Status = "Queued", cliLanePending
			case outcome.Disposition == api.WorkflowDispositionCanceled:
				lane.State = "Canceled"
				lane.Status = cliLaneBlocked
			case outcome.Disposition == api.WorkflowDispositionFailed:
				lane.State = "Failed"
				lane.Status = cliLaneBlocked
			case outcome.UploadEligibility == api.UploadEligibilityEligible:
				lane.State = "Eligible"
				lane.Status, lane.Reason = cliLaneReady, ""
			default:
				if outcome.Lifecycle != api.OperationLifecycleReady || outcome.Disposition != api.WorkflowDispositionNone {
					lane.State = string(outcome.Lifecycle) + " / " + string(outcome.Disposition)
				}
			}
			if outcome.UploadSkipReason != "" {
				if outcome.UploadSkipReason != api.UploadSkipReasonNotReady || lane.Reason == "" {
					lane.Reason = string(outcome.UploadSkipReason)
				}
			}
			if detail := strings.TrimSpace(outcome.UploadSkipDetail); detail != "" {
				lane.Reason = detail
				lane.Detail += "\nSkip reason: " + detail
			}
			if len(outcome.Failures) > 0 {
				for _, failure := range outcome.Failures {
					if lane.Reason == "" || (failure.Failure.Code != api.OperationFailureNoEligibleTrackers && outcome.UploadSkipDetail == "") {
						lane.Reason = string(failure.Failure.Code)
					}
					lane.Detail += "\nFailure: " + string(failure.Failure.Code)
				}
			}
			if firstPendingCLICompositeAction(outcome.RequiredActions) != nil {
				lane.State, lane.Status = "Awaiting decision", cliLanePending
			}
		}
		if approval := s.current.TrackerApproval; approval != nil && lane.Status == cliLaneReady && slices.Contains(approval.ApprovedTrackerIDs, id) {
			lane.State = "Approved"
		}
		if result := s.current.DryRun; view.Debug && result != nil {
			for _, report := range result.Reports {
				if report.TrackerID == id {
					lane.State = "Debug: " + string(report.Status)
					lane.Reason = "submission_suppressed"
					lane.Status = cliLanePending
					switch report.Status {
					case api.StageStatusCompleted:
						lane.Status = cliLaneReady
					case api.StageStatusFailed, api.StageStatusSkipped:
						lane.Status = cliLaneBlocked
					case api.StageStatusPending, api.StageStatusQueued, api.StageStatusReady, api.StageStatusBlocked, api.StageStatusStale,
						api.StageStatusPartial, api.StageStatusRunning, api.StageStatusExecuted, api.StageStatusInterrupted,
						api.StageStatusCanceled, api.StageStatusUnavailable, "":
					}
					if report.ClientInjection.Status != "" {
						lane.Detail += "\nClient injection: " + string(report.ClientInjection.Status)
					}
				}
			}
		}
		if result := s.current.UploadResult; result != nil {
			for _, tracker := range result.Results {
				if tracker.TrackerID != id {
					continue
				}
				lane.State = "Submission: " + string(tracker.EffectiveSubmissionStatus()) + " / Client: " + string(tracker.EffectiveClientInjectionStatus())
				lane.Reason = ""
				lane.Status = cliLanePending
				if tracker.EffectiveSubmissionStatus() == api.StageStatusCompleted {
					lane.Status = cliLaneReady
					lane.URL = tracker.RemoteURL
				} else if tracker.EffectiveSubmissionStatus() == api.StageStatusFailed || tracker.EffectiveSubmissionStatus() == api.StageStatusSkipped {
					lane.Status = cliLaneBlocked
				}
				for _, failure := range tracker.Failures {
					lane.Reason += string(failure.Failure.Code) + " "
				}
			}
		}
		for _, exclusion := range s.current.Workflow.SubmissionExclusions {
			if exclusion.TrackerID == id {
				lane.State, lane.Reason = "Already uploaded", exclusion.Reason
				lane.Status = cliLaneReady
			}
		}
		view.Lanes[index] = lane
	}
	if op := s.current.Operation; op != nil {
		view.OperationID = op.ID
		view.Stage = op.Phase + " / " + string(op.Status)
		view.Completed, view.Total = op.Completed, op.Total
	}
	if bridge := cliBridgeFromOutput(s.streams.out); bridge != nil {
		bridge.mu.Lock()
		view.Arguments = bridge.latest.Arguments
		view.ItemsTotal = bridge.latest.ItemsTotal
		view.Result = bridge.latest.Result
		bridge.mu.Unlock()
	}
	if s.current.Workflow.AllSelectedTrackersAlreadyUploaded() {
		view.Result = "Already uploaded to all selected trackers; no new submission was started."
	}
	s.streams.presenter.Publish(view)
	if bridge := cliBridgeFromOutput(s.streams.out); bridge != nil && s.current.Operation != nil {
		op := s.current.Operation
		bridge.mu.Lock()
		epoch := bridge.epoch
		current := bridge.latest.Item == item && bridge.latest.WorkflowID == view.WorkflowID && bridge.latest.OperationID == op.ID &&
			bridge.latest.Revision == view.Revision && op.WorkflowID == view.WorkflowID
		bridge.mu.Unlock()
		if current {
			for _, operationItem := range op.Items[:min(len(op.Items), cliTelemetryLimit)] {
				bridge.Progress(cliTelemetry{
					Item:        item,
					Epoch:       epoch,
					WorkflowID:  string(view.WorkflowID),
					OperationID: string(op.ID),
					Lane:        operationItem.ID,
					Attempt:     string(op.ID),
					Phase:       operationItem.Label + ": " + operationItem.Phase + " / " + string(operationItem.Status),
					Completed:   operationItem.Completed,
					Total:       operationItem.Total,
					ItemOnly:    true,
				})
			}
		}
	}
}

func (s *cliWorkflowSession) bindAction(ctx context.Context, action api.RequiredAction, evidence string) {
	s.publishPresentation(ctx)
	bindCLIQuestion(ctx, s.streams.out, s.current.Workflow.ID, action.WorkflowRevision, action.ID, evidence)
}

// presentationProgressContext starts a new reporter epoch for this queue item.
// Duplicate and torrent counts remain item telemetry; workflow counts own the
// aggregate bar. Older callback closures cannot update the new reporter lifetime.
func (s *cliWorkflowSession) presentationProgressContext(ctx context.Context) context.Context {
	bridge := cliBridgeFromOutput(s.streams.out)
	if bridge == nil {
		return ctx
	}
	item, _ := ctx.Value(cliItemContextKey{}).(uint64)
	bridge.mu.Lock()
	bridge.epoch++
	bridge.progress = nil
	bridge.telemetry = nil
	bridge.latest.Epoch = bridge.epoch
	view := bridge.latest
	bridge.view = &view
	epoch := bridge.epoch
	bridge.mu.Unlock()
	bridge.notify()
	base := cliTelemetry{Item: item, Epoch: epoch}
	ctx = api.WithDupeProgressReporter(ctx, func(update api.DupeProgressUpdate) {
		printCLIWorkflowDupeProgress(s.streams.out, update)
		if update.Total > 0 && (update.Completed > 0 || update.Status == "queued") {
			progress := base
			progress.Phase, progress.Lane = "check-duplicates", "check-duplicates"
			progress.Completed, progress.Total = update.Completed, update.Total
			progress.ItemOnly = true
			bridge.Progress(progress)
		}
	})
	ctx = api.WithWorkflowProgressReporter(ctx, func(update api.WorkflowProgressUpdate) {
		progress := base
		progress.ItemOnly = update.ItemOnly
		if update.ItemOnly {
			progress.Lane = update.ItemID
		}
		progress.Phase, progress.Completed, progress.Total = update.Phase, update.Completed, update.Total
		bridge.Progress(progress)
	})
	ctx = api.WithUploadProgressReporter(ctx, func(update api.UploadProgressUpdate) {
		progress := base
		progress.Phase, progress.Lane = update.Task, update.Task
		if update.Tracker != "" {
			progress.Lane = update.Tracker + ":" + update.Task
		}
		progress.Completed, progress.Total = update.CompletedPieces, update.TotalPieces
		progress.ItemOnly = true
		bridge.Progress(progress)
	})
	ctx = api.WithPreparationProgressReporter(ctx, func(update api.PreparationProgressUpdate) {
		progress := base
		progress.Phase = update.Label
		if update.TotalBytes > 0 {
			progress.Total = 100
			progress.Completed = int(update.CompletedBytes * 100 / update.TotalBytes)
		}
		bridge.Progress(progress)
	})
	ctx = api.WithImageUploadProgressReporter(ctx, func(update api.ImageUploadProgressUpdate) {
		progress := base
		progress.Phase, progress.Lane, progress.Attempt = "Image hosting: "+update.Host+" / "+string(update.Status), update.UsageScope, update.AttemptID
		progress.Completed, progress.Total = update.Completed, update.Total
		bridge.Progress(progress)
	})
	return ctx
}
