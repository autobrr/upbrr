// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { describe, expect, it } from "vitest";
import type { ReleaseWorkflowCurrent } from "../api/generated/release-workflow";
import { workflowQuestionnaires } from "./projections";

const current = (): ReleaseWorkflowCurrent =>
  ({
    workflow: { id: "workflow-1" },
    projections: {
      id: "projections-1",
      revision: 2,
      projections: [
        {
          trackerId: "EXAMPLE",
          displayName: "Example Tracker",
          questionnaire: [
            { key: "subtitle", label: "Subtitle review", required: true, value: "no" },
          ],
        },
      ],
    },
    dryRun: {
      workflowId: "workflow-1",
      projectionSet: { id: "projections-1", revision: 2 },
      reports: [
        {
          trackerId: "EXAMPLE",
          questionnaire: [
            {
              key: "poster",
              label: "Poster",
              required: true,
              value: "",
              help: "A new group requires a poster",
            },
            { key: "director", label: "Director", required: true, value: "Example Director" },
          ],
        },
        { trackerId: "OTHER", questionnaire: [{ key: "secretly-selected", required: true }] },
      ],
    },
  }) as unknown as ReleaseWorkflowCurrent;

describe("workflowQuestionnaires", () => {
  it("uses exact late requirements without losing pure questions or widening tracker selection", () => {
    const workflow = current();
    const questions = workflowQuestionnaires(workflow);
    expect(questions).toHaveLength(1);
    expect(questions[0].preparationQuestionnaire).toEqual([
      {
        key: "poster",
        label: "Poster",
        required: true,
        value: "",
        help: "A new group requires a poster",
      },
      { key: "director", label: "Director", required: true, value: "Example Director" },
    ]);
    expect(questions[0].questionnaire).toEqual([
      { key: "subtitle", label: "Subtitle review", required: true, value: "no" },
    ]);
    expect(workflow.projections?.projections[0].questionnaire).toHaveLength(1);
  });

  it("keeps late validation of a pure question in its original panel", () => {
    const workflow = current();
    const updated: ReleaseWorkflowCurrent = {
      ...workflow,
      dryRun: {
        ...workflow.dryRun!,
        reports: [
          {
            ...workflow.dryRun!.reports[0],
            questionnaire: [
              {
                key: "subtitle",
                label: "Subtitle review",
                required: true,
                value: "",
                help: "Review this choice again",
              },
            ],
          },
        ],
      },
    };
    expect(workflowQuestionnaires(updated)[0].questionnaire).toEqual([
      {
        key: "subtitle",
        label: "Subtitle review",
        required: true,
        value: "",
        help: "Review this choice again",
      },
    ]);
    expect(workflowQuestionnaires(updated)[0].preparationQuestionnaire).toEqual([]);
  });

  it("preserves exact reviewed URL values when late reports redact display values", () => {
    const workflow = current();
    const reviewed = "https://www.youtube.com/watch?v=EXAMPLE";
    const updated = {
      ...workflow,
      projections: {
        ...workflow.projections!,
        projections: [
          {
            ...workflow.projections!.projections[0],
            questionnaire: [],
            questionnaireAnswers: { poster: reviewed },
          },
        ],
      },
      dryRun: {
        ...workflow.dryRun!,
        reports: [
          {
            ...workflow.dryRun!.reports[0],
            questionnaire: [
              {
                key: "poster",
                label: "Poster",
                required: true,
                value: "https://www.youtube.com/watch",
              },
            ],
          },
        ],
      },
    };
    expect(workflowQuestionnaires(updated)[0].preparationQuestionnaire?.[0]).toEqual({
      key: "poster",
      label: "Poster",
      required: true,
      value: reviewed,
    });
  });

  it("keeps an empty required late value instead of restoring a rejected displayed default", () => {
    const workflow = current();
    const updated: ReleaseWorkflowCurrent = {
      ...workflow,
      projections: {
        ...workflow.projections!,
        projections: [
          {
            ...workflow.projections!.projections[0],
            questionnaire: [],
            questionnaireAnswers: { channel: "rejected" },
          },
        ],
      },
      dryRun: {
        ...workflow.dryRun!,
        reports: [
          {
            ...workflow.dryRun!.reports[0],
            questionnaire: [
              {
                key: "channel",
                label: "Channel",
                required: true,
                value: "",
                help: "Choose a valid channel",
              },
            ],
          },
        ],
      },
    };
    expect(workflowQuestionnaires(updated)[0].preparationQuestionnaire?.[0]).toEqual({
      key: "channel",
      label: "Channel",
      required: true,
      value: "",
      help: "Choose a valid channel",
    });
  });

  it.each(["workflow", "projection", "revision"])(
    "ignores stale %s report requirements",
    (mismatch) => {
      const workflow = current();
      const stale: ReleaseWorkflowCurrent = {
        ...workflow,
        dryRun: {
          ...workflow.dryRun!,
          workflowId: mismatch === "workflow" ? "workflow-old" : workflow.workflow.id,
          projectionSet: {
            id: mismatch === "projection" ? "projections-old" : "projections-1",
            revision: mismatch === "revision" ? 1 : 2,
          },
        },
      };
      expect(workflowQuestionnaires(stale)[0].questionnaire).toEqual(
        workflow.projections?.projections[0].questionnaire,
      );
    },
  );

  it("does not revive questions without current projections", () => {
    const workflow = current();
    expect(workflowQuestionnaires({ ...workflow, projections: undefined })).toEqual([]);
    expect(workflowQuestionnaires(null)).toEqual([]);
  });
});
