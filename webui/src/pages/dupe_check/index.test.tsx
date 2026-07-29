// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { WorkflowOperationProgress } from "../../components/WorkflowOperationProgress";
import type { DuplicatesFacet } from "../../releaseSession/types";
import type { Operation as WorkflowOperationStatus } from "../../api/generated/release-workflow";
import DupeCheckPage from ".";

afterEach(cleanup);

const facetFor = (
  view: Partial<DuplicatesFacet["view"]> = {},
  commands: Partial<Omit<DuplicatesFacet, "view">> = {},
): DuplicatesFacet => ({
  view: {
    status: "idle",
    assessment: null,
    projections: null,
    preflight: null,
    completed: 0,
    total: 0,
    ignoredTrackers: [],
    selectedTrackers: ["EXAMPLE"],
    releaseNameOverrides: {},
    error: "",
    ...view,
  },
  run: vi.fn(async () => true),
  cancel: vi.fn(async () => true),
  chooseTrackers: vi.fn(),
  confirmReleaseName: vi.fn(),
  setIgnored: vi.fn(),
  ...commands,
});

const renderPage = (facet: DuplicatesFacet, trackers = ["EXAMPLE"]) =>
  render(
    <DupeCheckPage
      facet={facet}
      sourcePath="C:\\media\\Example"
      trackerUploadItems={trackers.map((name) => ({ name, config: {} }))}
      trackerIconSrcByName={{}}
    />,
  );

describe("DupeCheckPage", () => {
  it("forwards duplicate-check intent through the facet", () => {
    const run = vi.fn(async () => true);
    renderPage(facetFor({}, { run }));

    fireEvent.click(screen.getByRole("button", { name: "Run dupe check" }));
    expect(run).toHaveBeenCalledOnce();
  });

  it("requires confirmation or editing of projected non-scene release names", () => {
    const run = vi.fn(async () => true);
    const confirmReleaseName = vi.fn();
    renderPage(
      facetFor(
        {
          selectedTrackers: ["AR"],
          projections: {
            projections: [
              {
                trackerId: "AR",
                displayName: "AR",
                uploadReleaseName: "Example.Release.2026-GRP",
                policyDecisions: [
                  {
                    code: "release_name_confirmation",
                    decision: "confirmation_required",
                    blocking: true,
                  },
                ],
              },
            ],
          } as unknown as NonNullable<DuplicatesFacet["view"]["projections"]>,
        },
        { run, confirmReleaseName },
      ),
      ["AR"],
    );

    const input = screen.getByRole("textbox", { name: "Release name for AR" });
    expect(input).toHaveValue("Example.Release.2026-GRP");
    fireEvent.change(input, { target: { value: "Example.Release.2026.EDIT-GRP" } });
    expect(confirmReleaseName).toHaveBeenCalledWith("AR", "Example.Release.2026.EDIT-GRP");
    fireEvent.click(screen.getByRole("button", { name: "Confirm names & run dupe check" }));
    expect(run).toHaveBeenCalledOnce();
  });

  it("owns tracker selection and blocks execution while selection is empty", () => {
    const chooseTrackers = vi.fn();
    renderPage(facetFor({ selectedTrackers: [] }, { chooseTrackers }));

    expect(screen.getByRole("button", { name: "Run dupe check" })).toBeDisabled();
    fireEvent.click(screen.getByRole("checkbox", { name: "EXAMPLE" }));
    expect(chooseTrackers).toHaveBeenCalledWith(["EXAMPLE"]);
  });

  it("leaves running progress to the release layout", () => {
    renderPage(
      facetFor({
        status: "running",
        completed: 1,
        total: 3,
        selectedTrackers: ["EXAMPLE", "SECOND", "THIRD"],
      }),
      ["EXAMPLE", "SECOND", "THIRD"],
    );

    expect(screen.getByRole("button", { name: "Checking 1/3..." })).toBeDisabled();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });

  it("renders exactly one layout-owned progress region with safe recovery detail", () => {
    const operation: WorkflowOperationStatus = {
      id: "operation-1",
      workflowId: "workflow-1",
      revision: 3,
      sequence: 2,
      command: "check_duplicates",
      operation: "duplicate_check",
      phase: "duplicate_check",
      status: "failed",
      progress: 100,
      completed: 1,
      total: 1,
      message: "Operation failed.",
      failures: [
        {
          failure: {
            Code: "tracker_unavailable",
            Operation: "duplicate_check",
            Message: "Tracker duplicate checking is temporarily unavailable.",
            Recovery: "retry_operation",
          },
        },
      ],
      events: [
        {
          sequence: 2000,
          workflowId: "workflow-1",
          operationId: "operation-1",
          command: "check_duplicates",
          phase: "duplicate_check",
          scope: "workflow",
          scopeId: "workflow-1",
          lifecycle: "running",
          state: "running",
          disposition: "none",
          severity: "debug",
          message: "Checking tracker duplicate evidence.",
          timestamp: "2026-07-21T00:00:01Z",
        },
        {
          sequence: 2001,
          workflowId: "workflow-1",
          operationId: "operation-1",
          command: "check_duplicates",
          phase: "duplicate_check",
          scope: "tracker",
          scopeId: "EXAMPLE",
          lifecycle: "terminal",
          state: "failed",
          disposition: "failed",
          severity: "error",
          failureCode: "tracker_unavailable",
          recovery: "retry_operation",
          message: "Tracker duplicate checking is temporarily unavailable.",
          timestamp: "2026-07-21T00:00:01Z",
        },
      ],
      startedAt: "2026-07-21T00:00:00Z",
      updatedAt: "2026-07-21T00:00:01Z",
      completedAt: "2026-07-21T00:00:01Z",
    };

    render(
      <>
        <WorkflowOperationProgress operation={operation} />
        <DupeCheckPage
          facet={facetFor({ status: "running" })}
          sourcePath="C:\\media\\Example"
          trackerUploadItems={[{ name: "EXAMPLE", config: {} }]}
          trackerIconSrcByName={{}}
        />
      </>,
    );

    expect(screen.getAllByRole("progressbar")).toHaveLength(1);
    expect(
      screen.getByText("Tracker duplicate checking is temporarily unavailable."),
    ).toBeInTheDocument();
    expect(screen.getByText("Recovery: retry operation.")).toBeInTheDocument();
  });

  it("shows exact upload names and deduplicated blockers without false zero matches", () => {
    renderPage(
      facetFor({
        status: "ready",
        selectedTrackers: ["EXAMPLE"],
        projections: {
          projections: [
            {
              trackerId: "EXAMPLE",
              displayName: "Example Tracker",
              canonicalReleaseName: "Example Release 2026 1080p-GRP",
              uploadReleaseName: "Example.Release.2026.1080p-GRP",
              duplicateCriteria: { name: "Example Release 2026" },
              policyDecisions: [
                {
                  code: "movie_only",
                  decision: "ineligible",
                  blocking: true,
                  message: "Category TV is not movie.",
                },
              ],
              failures: [
                {
                  failure: {
                    Code: "no_eligible_trackers",
                    Operation: "duplicate_check",
                    Message: "Category TV is not movie.",
                    Recovery: "select_trackers",
                  },
                },
              ],
              readiness: "ineligible",
            },
          ],
        } as unknown as NonNullable<DuplicatesFacet["view"]["projections"]>,
        preflight: {
          results: [
            {
              trackerId: "EXAMPLE",
              state: "action_required",
              failures: [
                {
                  failure: {
                    Code: "tracker_auth_not_ready",
                    Operation: "duplicate_check",
                    Message: "Category TV is not movie.",
                    Recovery: "authenticate",
                  },
                },
              ],
            },
          ],
        } as unknown as NonNullable<DuplicatesFacet["view"]["preflight"]>,
      }),
    );

    expect(screen.getByText("Example Release 2026 1080p-GRP")).toBeInTheDocument();
    expect(screen.getByText("Example.Release.2026.1080p-GRP")).toBeInTheDocument();
    expect(screen.getByText("Example Release 2026")).toBeInTheDocument();
    expect(screen.getByText("Preflight action_required")).toBeInTheDocument();
    expect(screen.getAllByText("Category TV is not movie.")).toHaveLength(1);
    expect(
      screen.getByText("Duplicate search not run because this tracker is blocked."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/0 match\(es\)/)).not.toBeInTheDocument();
  });

  it("separates strict, manual-review, and advisory policy evidence", () => {
    renderPage(
      facetFor({
        status: "ready",
        projections: {
          projections: [
            {
              trackerId: "EXAMPLE",
              displayName: "Example Tracker",
              uploadReleaseName: "Example.Release.2026.1080p-GRP",
              readiness: "ineligible",
              policyDecisions: [
                {
                  code: "release_name_policy",
                  decision: "standalone/example/v2",
                  blocking: false,
                },
                {
                  code: "container_support",
                  decision: "ineligible",
                  blocking: true,
                  disposition: "strict",
                  evidenceStatus: "complete",
                  message: "Selected container is not accepted.",
                },
                {
                  code: "release_identity",
                  decision: "ineligible",
                  blocking: true,
                  disposition: "waivable",
                  evidenceStatus: "partial",
                  message: "Release identity needs manual confirmation.",
                },
                {
                  code: "metadata_poster",
                  decision: "advisory",
                  blocking: false,
                  disposition: "advisory",
                  evidenceStatus: "unavailable",
                  message: "Poster metadata is not available.",
                },
              ],
            },
          ],
        } as unknown as NonNullable<DuplicatesFacet["view"]["projections"]>,
      }),
    );

    expect(screen.getByText("Strict blockers")).toBeInTheDocument();
    expect(screen.getByText("Manual review / waivable")).toBeInTheDocument();
    expect(screen.getByText("Advisories")).toBeInTheDocument();
    expect(screen.getByText("1 advisory")).toBeInTheDocument();
    expect(screen.queryByText("release name policy")).not.toBeInTheDocument();
    expect(screen.getByText("Selected container is not accepted.")).toBeInTheDocument();
    expect(screen.getByText("Evidence complete", { exact: false })).toBeInTheDocument();
    expect(
      screen.getByText("Evidence partial · manual review needed", { exact: false }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Evidence unavailable · prerequisite/action needed", { exact: false }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Provide missing metadata or evidence before relying on this decision."),
    ).toBeInTheDocument();
  });

  it("renders auth failure as retryable blocked lane evidence without an action card", () => {
    const authFailure = {
      failure: {
        Code: "tracker_auth_required",
        Operation: "duplicate_check",
        Message:
          "Tracker authentication is not ready for this attempt. Resolve authentication outside the upload workflow, then restart it.",
        Recovery: "authenticate_trackers",
      },
      trackerId: "BETA",
    };
    renderPage(
      facetFor({
        status: "ready",
        selectedTrackers: ["ALPHA", "BETA"],
        projections: {
          projections: [
            {
              trackerId: "ALPHA",
              displayName: "Alpha",
              uploadReleaseName: "Example.Release.2026.ALPHA-GRP",
              readiness: "ready",
            },
            {
              trackerId: "BETA",
              displayName: "Beta",
              uploadReleaseName: "Example.Release.2026.BETA-GRP",
              readiness: "blocked",
              dupeReady: false,
              uploadReady: false,
              failures: [authFailure],
            },
          ],
        } as unknown as NonNullable<DuplicatesFacet["view"]["projections"]>,
        preflight: {
          results: [
            { trackerId: "ALPHA", state: "ready", authReady: true },
            {
              trackerId: "BETA",
              state: "retryable",
              authReady: false,
              failures: [authFailure],
            },
          ],
        } as unknown as NonNullable<DuplicatesFacet["view"]["preflight"]>,
        assessment: {
          results: [
            {
              trackerId: "ALPHA",
              status: "completed",
              decision: "no_match",
              matches: [],
            },
            {
              trackerId: "BETA",
              status: "skipped",
              decision: "skipped",
              matches: [],
              failures: [authFailure],
            },
          ],
        } as unknown as NonNullable<DuplicatesFacet["view"]["assessment"]>,
      }),
      ["ALPHA", "BETA"],
    );

    expect(screen.getByText("Projection blocked")).toBeInTheDocument();
    expect(screen.getByText("Preflight retryable")).toBeInTheDocument();
    expect(
      screen.getAllByText(
        "Tracker authentication is not ready for this attempt. Resolve authentication outside the upload workflow, then restart it.",
      ),
    ).toHaveLength(1);
    expect(screen.queryByText(/authenticate this tracker/i)).not.toBeInTheDocument();
    expect(screen.getByText("Alpha")).toBeInTheDocument();
  });

  it("strictly blocks in-client matches and keeps remote ignore decisions optional", () => {
    const setIgnored = vi.fn();
    renderPage(
      facetFor(
        {
          status: "ready",
          selectedTrackers: ["AITHER", "REMOTE"],
          assessment: {
            results: [
              {
                trackerId: "AITHER",
                uploadReleaseName: "Example.Release.S01E01.1080p-GRP",
                matches: [{ id: "123", name: "Strict match", reason: "in_client" }],
                decision: "accepted",
                status: "completed",
              },
              {
                trackerId: "REMOTE",
                uploadReleaseName: "Example.Release.S01E01.1080p-GRP",
                matches: [{ id: "456", name: "Optional match", reason: "same release" }],
                decision: "pending",
                status: "completed",
              },
            ],
          } as unknown as NonNullable<DuplicatesFacet["view"]["assessment"]>,
          projections: {
            projections: ["AITHER", "REMOTE"].map((trackerId) => ({
              trackerId,
              displayName: trackerId,
              canonicalReleaseName: "Example Release S01E01 1080p-GRP",
              uploadReleaseName: "Example.Release.S01E01.1080p-GRP",
              policyDecisions: [],
              readiness: "ready",
            })),
          } as unknown as NonNullable<DuplicatesFacet["view"]["projections"]>,
          preflight: {
            results: ["AITHER", "REMOTE"].map((trackerId) => ({
              trackerId,
              state: "ready",
            })),
          } as unknown as NonNullable<DuplicatesFacet["view"]["preflight"]>,
        },
        { setIgnored },
      ),
      ["AITHER", "REMOTE"],
    );

    expect(screen.getByText("In client · upload blocked")).toBeInTheDocument();
    expect(
      screen.queryByRole("checkbox", { name: "Ignore dupes for AITHER" }),
    ).not.toBeInTheDocument();
    const optional = screen.getByRole("switch", { name: "Ignore dupes for REMOTE" });
    expect(optional).not.toBeChecked();
    fireEvent.click(optional);
    expect(setIgnored).toHaveBeenCalledWith("REMOTE", true);
  });

  it("renders per-candidate relations and uses distinct incomplete-evidence acknowledgement", () => {
    const setIgnored = vi.fn();
    renderPage(
      facetFor(
        {
          status: "ready",
          assessment: {
            results: [
              {
                trackerId: "EXAMPLE",
                uploadReleaseName: "Example.Release.2026.2160p-GRP",
                decision: "pending",
                status: "blocked",
                policyId: "example/duplicate/v1",
                search: {
                  complete: false,
                  pages: 2,
                  candidateCount: 2,
                  warnings: ["Tracker result limit prevented a complete search."],
                },
                matches: [
                  {
                    id: "1",
                    name: "Example.Release.2026.2160p.DV.HDR-GRP",
                    relation: "proposed_trumps",
                    reasons: [{ code: "broader_hdr_compatibility" }],
                    hdr: {
                      formats: ["dolby_vision", "hdr10"],
                      origin: "tracker_api",
                      status: "complete",
                    },
                    evidenceStatus: "complete",
                    pack: false,
                    internal: false,
                    trumpable: false,
                  },
                  {
                    id: "2",
                    name: "Example.Release.2026.2160p.Unknown-GRP",
                    relation: "insufficient_evidence",
                    reasons: [{ code: "candidate_hdr_missing" }],
                    hdr: { origin: "unknown", status: "missing" },
                    evidenceStatus: "missing",
                    pack: false,
                    internal: false,
                    trumpable: false,
                  },
                ],
              },
            ],
          } as unknown as NonNullable<DuplicatesFacet["view"]["assessment"]>,
          projections: {
            projections: [
              {
                trackerId: "EXAMPLE",
                displayName: "Example",
                uploadReleaseName: "Example.Release.2026.2160p-GRP",
                readiness: "ready",
              },
            ],
          } as unknown as NonNullable<DuplicatesFacet["view"]["projections"]>,
          preflight: {
            results: [{ trackerId: "EXAMPLE", state: "ready" }],
          } as unknown as NonNullable<DuplicatesFacet["view"]["preflight"]>,
        },
        { setIgnored },
      ),
    );

    expect(screen.getByText("Search incomplete · 2 page(s)")).toBeInTheDocument();
    expect(screen.getByText("proposed trumps")).toBeInTheDocument();
    expect(screen.getByText("insufficient evidence")).toBeInTheDocument();
    expect(screen.getByText(/HDR: dolby vision \+ hdr10/)).toBeInTheDocument();
    expect(
      screen.getByText("Tracker result limit prevented a complete search."),
    ).toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Ignore dupes for EXAMPLE" })).toBeNull();
    const acknowledge = screen.getByRole("switch", {
      name: "Acknowledge dupe risk for EXAMPLE",
    });
    fireEvent.click(acknowledge);
    expect(setIgnored).toHaveBeenCalledWith("EXAMPLE", true);
  });

  it("keeps proven coexistence out of the default action list and candidate count", () => {
    renderPage(
      facetFor({
        status: "ready",
        assessment: {
          results: [
            {
              trackerId: "EXAMPLE",
              uploadReleaseName: "Example.Release.2026.1080p.WEB-DL-GRP",
              decision: "no_match",
              status: "completed",
              search: { complete: true, pages: 1, candidateCount: 1 },
              matches: [
                {
                  id: "1",
                  name: "Example.Release.2026.2160p.Remux-GRP",
                  relation: "coexists",
                  reasons: [{ code: "different_resolution" }],
                  resolution: "2160p",
                  pack: false,
                  internal: false,
                  trumpable: false,
                },
              ],
            },
          ],
        } as unknown as NonNullable<DuplicatesFacet["view"]["assessment"]>,
        projections: {
          projections: [
            {
              trackerId: "EXAMPLE",
              displayName: "Example",
              uploadReleaseName: "Example.Release.2026.1080p.WEB-DL-GRP",
              readiness: "ready",
            },
          ],
        } as unknown as NonNullable<DuplicatesFacet["view"]["projections"]>,
        preflight: {
          results: [{ trackerId: "EXAMPLE", state: "ready" }],
        } as unknown as NonNullable<DuplicatesFacet["view"]["preflight"]>,
      }),
    );

    expect(screen.getByText("0 candidates · no duplicate found")).toBeInTheDocument();
    expect(screen.queryByText("Example.Release.2026.2160p.Remux-GRP")).not.toBeInTheDocument();
    expect(screen.queryByRole("switch", { name: "Ignore dupes for EXAMPLE" })).toBeNull();
  });

  it("allows explicit risk acknowledgement when an incomplete search returns no candidates", () => {
    const setIgnored = vi.fn();
    renderPage(
      facetFor(
        {
          status: "ready",
          assessment: {
            results: [
              {
                trackerId: "EXAMPLE",
                uploadReleaseName: "Example.Release.2026.2160p-GRP",
                decision: "pending",
                status: "blocked",
                search: { complete: false, pages: 2, candidateCount: 0 },
                matches: [],
              },
            ],
          } as unknown as NonNullable<DuplicatesFacet["view"]["assessment"]>,
          projections: {
            projections: [
              {
                trackerId: "EXAMPLE",
                displayName: "Example",
                uploadReleaseName: "Example.Release.2026.2160p-GRP",
                readiness: "ready",
              },
            ],
          } as unknown as NonNullable<DuplicatesFacet["view"]["projections"]>,
          preflight: {
            results: [{ trackerId: "EXAMPLE", state: "ready" }],
          } as unknown as NonNullable<DuplicatesFacet["view"]["preflight"]>,
        },
        { setIgnored },
      ),
    );

    const acknowledge = screen.getByRole("switch", {
      name: "Acknowledge dupe risk for EXAMPLE",
    });
    fireEvent.click(acknowledge);
    expect(setIgnored).toHaveBeenCalledWith("EXAMPLE", true);
  });
});
