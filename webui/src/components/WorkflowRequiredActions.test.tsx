// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type {
  Operation as WorkflowOperationStatus,
  RequiredAction,
  WorkflowContinuation,
} from "../api/generated/release-workflow";
import { WorkflowOperationProgress } from "./WorkflowOperationProgress";
import { WorkflowRequiredActions } from "./WorkflowRequiredActions";

afterEach(cleanup);

const action = (values: Partial<RequiredAction> = {}): RequiredAction => ({
  createdAt: "2026-07-26T00:00:00Z",
  id: "action-1",
  kind: "review_duplicates",
  prompt: "Review the duplicate match.",
  status: "pending",
  trackerId: "EXAMPLE",
  workflowRevision: 4,
  ...values,
});

const continuation = (requiredActions: readonly RequiredAction[]): WorkflowContinuation => ({
  availableGoals: [],
  disposition: "needs_action",
  lifecycle: "waiting",
  refs: {},
  requiredActions,
});

describe("WorkflowRequiredActions", () => {
  it("keeps source evidence visible while showing Blu-ray playlist progress", () => {
    render(
      <WorkflowOperationProgress
        operation={
          {
            command: "prepare_release",
            completed: 300,
            events: [
              {
                command: "prepare_release",
                disposition: "none",
                lifecycle: "running",
                message: "PLAYLIST: 7/26",
                operationId: "operation-1",
                phase: "bdinfo",
                scope: "workflow",
                scopeId: "bdinfo",
                sequence: 1,
                severity: "info",
                state: "running",
                timestamp: "2026-07-26T00:00:00Z",
                workflowId: "workflow-1",
              },
            ],
            id: "operation-1",
            items: [
              {
                completed: 0,
                id: "source_evidence",
                kind: "preparation_phase",
                label: "Collect source evidence",
                phase: "source_evidence",
                status: "running",
                total: 1,
              },
              {
                completed: 0,
                id: "bdinfo",
                kind: "preparation_phase",
                label: "Analyze Blu-ray playlists",
                phase: "bdinfo",
                status: "running",
                total: 1200,
              },
            ],
            message: "Analyzing selected Blu-ray playlists.",
            operation: "preparation",
            progress: 25,
            revision: 1,
            sequence: 1,
            startedAt: "2026-07-26T00:00:00Z",
            status: "running",
            total: 1200,
            updatedAt: "2026-07-26T00:00:00Z",
            workflowId: "workflow-1",
          } as WorkflowOperationStatus
        }
      />,
    );

    expect(screen.getByText("Collect source evidence")).toBeInTheDocument();
    expect(screen.getByText("Analyze Blu-ray playlists")).toBeInTheDocument();
    expect(screen.getByText("PLAYLIST: 7/26")).toBeInTheDocument();
  });

  it("stays visible after operation progress is complete and routes non-dupe actions", () => {
    const navigate = vi.fn();
    const completed = {
      command: "upload-media-images",
      completed: 1,
      id: "operation-1",
      progress: 100,
      status: "completed",
      total: 1,
    } as WorkflowOperationStatus;

    render(
      <>
        <WorkflowOperationProgress operation={completed} />
        <WorkflowRequiredActions
          continuation={continuation([
            action({
              kind: "select_metadata",
              prompt: "Review release metadata.",
              trackerId: undefined,
            }),
          ])}
          onNavigate={navigate}
        />
      </>,
    );

    expect(screen.queryByText("upload-media-images")).not.toBeInTheDocument();
    expect(screen.getByText("Review release metadata.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Review metadata" }));
    expect(navigate).toHaveBeenCalledWith("input");
  });

  it("renders unsupported actions without inventing navigation", () => {
    const navigate = vi.fn();
    const { rerender } = render(
      <WorkflowRequiredActions
        continuation={continuation([
          action({
            id: "action-unsupported",
            kind: "authenticate_tracker",
            prompt: "Authenticate the tracker.",
            trackerId: undefined,
          }),
        ])}
        onNavigate={navigate}
      />,
    );

    expect(screen.getByText("Authenticate the tracker.")).toBeInTheDocument();
    expect(screen.getByText("Continue from the relevant workflow page.")).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();

    rerender(
      <WorkflowRequiredActions
        continuation={continuation([action({ status: "resolved" })])}
        onNavigate={navigate}
      />,
    );
    expect(screen.queryByText("Action required")).not.toBeInTheDocument();
  });

  it("leaves duplicate review and tracker naming inside dupe tracker cards", () => {
    const navigate = vi.fn();
    render(
      <WorkflowRequiredActions
        continuation={continuation([
          action(),
          action({
            id: "action-2",
            kind: "provide_tracker_input",
            prompt: "Confirm the tracker release name.",
          }),
        ])}
        onNavigate={navigate}
      />,
    );

    expect(screen.queryByText("Review the duplicate match.")).not.toBeInTheDocument();
    expect(screen.queryByText("Confirm the tracker release name.")).not.toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
  });
});
