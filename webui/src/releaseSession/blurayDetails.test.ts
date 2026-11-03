// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { expect, it } from "vitest";
import type { MetadataPreview } from "../types";
import type { ReleaseWorkflowCurrent } from "../api/generated/release-workflow";
import { metadataPreviewWithBlurayDetails } from "./projections";

it("enriches only matching listing cards from the current inspection without replacing selection authority", () => {
  const preview = {
    SourcePath: "Example.Disc",
    Bluray: {
      SelectedReleaseID: "102",
      Candidates: [
        { ReleaseID: "101", Title: "Example A", Accepted: false, Score: 0 },
        { ReleaseID: "102", Title: "Example B", Accepted: true, Score: 0 },
      ],
    },
  } as unknown as MetadataPreview;
  const current = {
    workflow: { id: "workflow" },
    release: { release: { Source: { SourcePath: preview.SourcePath } } },
    operation: {
      workflowId: "workflow",
      operation: "preparation",
      items: [
        {
          kind: "bluray_detail",
          blurayCandidate: {
            ReleaseID: "101",
            Title: "Example A",
            Accepted: true,
            DetailsStatus: "complete",
            Score: 98,
            ReleaseDate: "2026-01-02",
            MatchNotes: ["disc matches"],
          },
        },
        {
          kind: "bluray_detail",
          blurayCandidate: { ReleaseID: "unlisted", DetailsStatus: "complete", Score: 100 },
        },
      ],
    },
  } as unknown as ReleaseWorkflowCurrent;
  const enriched = metadataPreviewWithBlurayDetails(preview, current)!;
  expect(enriched.Bluray!.Candidates).toHaveLength(2);
  expect(enriched.Bluray!.Candidates[0]).toMatchObject({
    DetailsStatus: "complete",
    ReleaseDate: "2026-01-02",
    Score: 98,
    Accepted: false,
  });
  expect(enriched.Bluray!.SelectedReleaseID).toBe("102");
  expect(enriched.Bluray!.Candidates[1].Accepted).toBe(true);
  expect(preview.Bluray!.Candidates[0].Score).toBe(0);
  expect(
    metadataPreviewWithBlurayDetails(preview, {
      ...current,
      operation: { ...current.operation!, workflowId: "older-workflow" },
    }),
  ).toBe(preview);
  expect(
    metadataPreviewWithBlurayDetails(preview, {
      ...current,
      release: {
        ...current.release!,
        release: {
          ...current.release!.release,
          Source: { ...current.release!.release.Source, SourcePath: "Different.Disc" },
        },
      },
    }),
  ).toBe(preview);
});
