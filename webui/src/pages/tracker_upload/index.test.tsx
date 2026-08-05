// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { UploadFacet } from "../../releaseSession/types";
import TrackerUploadPage from "./index";

afterEach(cleanup);

const uploadFacet = (
  view: Partial<UploadFacet["view"]> = {},
  methods: Partial<Omit<UploadFacet, "view">> = {},
): UploadFacet => ({
  view: {
    revision: 1,
    selectedTrackers: ["EXAMPLE"],
    projections: null,
    ignoredDupesFor: [],
    questionnaireAnswers: {},
    options: { noSeed: false, runLogLevel: "info" },
    dryRunStatus: "idle",
    uploadStatus: "idle",
    dryRunResult: null,
    result: null,
    error: "",
    ...view,
  },
  chooseTrackers: vi.fn(),
  answerQuestionnaire: vi.fn(),
  changeOptions: vi.fn(),
  runDryRun: vi.fn(async () => true),
  start: vi.fn(async () => true),
  cancel: vi.fn(async () => true),
  retry: vi.fn(async () => true),
  retryClientInjection: vi.fn(async () => true),
  ...methods,
});

const renderPage = (facet: UploadFacet) => render(<TrackerUploadPage facet={facet} />);

describe("TrackerUploadPage", () => {
  it("offers workflow dry run and direct upload without a review step", () => {
    const runDryRun = vi.fn(async () => true);
    const start = vi.fn(async () => true);
    renderPage(uploadFacet({}, { runDryRun, start }));

    expect(screen.queryByRole("heading", { name: "Tracker intent" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Review upload" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Run dry run" }));
    fireEvent.click(screen.getByRole("button", { name: "Start upload" }));
    expect(runDryRun).toHaveBeenCalledOnce();
    expect(start).toHaveBeenCalledOnce();
  });

  it("collects questionnaire answers from current workflow projections", () => {
    const answerQuestionnaire = vi.fn();
    const projections = {
      projections: [
        {
          trackerId: "EXAMPLE",
          displayName: "Example Tracker",
          questionnaire: [
            { key: "edition", label: "Edition", options: ["Standard", "Extended"], required: true },
            { key: "note", label: "Note", required: false },
          ],
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["projections"]>;
    renderPage(uploadFacet({ projections }, { answerQuestionnaire }));

    fireEvent.change(screen.getByLabelText("Edition *"), { target: { value: "Extended" } });
    fireEvent.change(screen.getByLabelText("Note"), { target: { value: "Synthetic note" } });
    expect(answerQuestionnaire).toHaveBeenCalledWith("EXAMPLE", "edition", "Extended");
    expect(answerQuestionnaire).toHaveBeenCalledWith("EXAMPLE", "note", "Synthetic note");
  });

  it("shows projection-backed tracker upload names before any dry run", () => {
    const projections = {
      projections: [
        {
          trackerId: "EXAMPLE",
          displayName: "Example Tracker",
          canonicalReleaseName: "Example.Release.2026.1080p-GRP",
          uploadReleaseName: "Example.Release.2026.1080p-GRP",
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["projections"]>;
    renderPage(uploadFacet({ projections }));

    expect(screen.getByText("Example.Release.2026.1080p-GRP")).toBeInTheDocument();
    expect(screen.queryByText("Canonical:")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Expand Example Tracker" }),
    ).not.toBeInTheDocument();
  });

  it("shows the canonical name as secondary context when it differs from the tracker name", () => {
    const projections = {
      projections: [
        {
          trackerId: "EXAMPLE",
          displayName: "Example Tracker",
          canonicalReleaseName: "Example Release 2026 1080p-GRP",
          uploadReleaseName: "Example.Release.2026.1080p-GRP",
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["projections"]>;
    renderPage(uploadFacet({ projections }));

    expect(screen.getByText("Example.Release.2026.1080p-GRP")).toBeInTheDocument();
    expect(screen.getByText("Example Release 2026 1080p-GRP")).toBeInTheDocument();
  });

  it("keeps tracker names visible for collapsed, partial, and failed dry-run results", () => {
    const projections = {
      projections: [
        {
          trackerId: "EXAMPLE",
          displayName: "Example Tracker",
          canonicalReleaseName: "Example.Release.2026.1080p-GRP",
          uploadReleaseName: "Example.Release.2026.1080p-GRP",
        },
        {
          trackerId: "OTHER",
          displayName: "Other Tracker",
          canonicalReleaseName: "Example.Release.2026.REPACK.1080p-GRP",
          uploadReleaseName: "Example.Release.2026.REPACK.1080p-GRP",
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["projections"]>;
    const dryRunResult = {
      status: "partial",
      reports: [
        {
          trackerId: "EXAMPLE",
          displayName: "Example Tracker",
          uploadReleaseName: "Example.Release.2026.1080p-GRP",
          status: "failed",
          failures: [{ failure: { Code: "upload_failed", Message: "Synthetic failure." } }],
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["dryRunResult"]>;
    renderPage(
      uploadFacet({
        selectedTrackers: ["EXAMPLE", "OTHER"],
        dryRunStatus: "ready",
        projections,
        dryRunResult,
      }),
    );

    expect(screen.getByText("partial")).toBeInTheDocument();
    expect(screen.getByText("failed")).toBeInTheDocument();
    expect(screen.getByText("Example.Release.2026.1080p-GRP")).toBeInTheDocument();
    expect(screen.getByText("Example.Release.2026.REPACK.1080p-GRP")).toBeInTheDocument();
    expect(screen.queryByText("Synthetic failure.")).not.toBeInTheDocument();
  });

  it("merges dry-run report details by tracker without replacing unrelated projections", () => {
    const projections = {
      projections: [
        {
          trackerId: "EXAMPLE",
          displayName: "Example Tracker",
          canonicalReleaseName: "Example.Release.2026.1080p-GRP",
          uploadReleaseName: "Example.Release.2026.1080p-GRP",
        },
        {
          trackerId: "OTHER",
          displayName: "Other Tracker",
          canonicalReleaseName: "Example.Release.2026.REPACK.1080p-GRP",
          uploadReleaseName: "Example.Release.2026.REPACK.1080p-GRP",
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["projections"]>;
    const dryRunResult = {
      status: "completed",
      reports: [
        {
          trackerId: "EXAMPLE",
          displayName: "Example Tracker",
          uploadReleaseName: "Example.Release.2026.PROPER.1080p-GRP",
          status: "ready",
          endpoint: "https://tracker.invalid/upload",
          fields: [{ key: "category", value: "1" }],
          files: [{ field: "torrent", present: true }],
          clientInjection: { status: "completed", message: "Injected." },
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["dryRunResult"]>;
    renderPage(
      uploadFacet({
        selectedTrackers: ["EXAMPLE", "OTHER"],
        dryRunStatus: "ready",
        projections,
        dryRunResult,
      }),
    );

    // The reviewed projection name stays authoritative; the report's differing
    // name must not be rendered as the reviewed name.
    expect(screen.getByText("Example.Release.2026.1080p-GRP")).toBeInTheDocument();
    expect(screen.queryByText("Example.Release.2026.PROPER.1080p-GRP")).not.toBeInTheDocument();
    expect(screen.getByText("Example.Release.2026.REPACK.1080p-GRP")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Expand Other Tracker" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Expand Example Tracker" }));
    expect(screen.getByText("https://tracker.invalid/upload")).toBeInTheDocument();
    expect(screen.getByText("category: 1")).toBeInTheDocument();
  });

  it("renders generated dry-run and upload outcomes", () => {
    const retry = vi.fn(async () => true);
    const dryRunResult = {
      status: "completed",
      reports: [
        {
          trackerId: "EXAMPLE",
          displayName: "Example Tracker",
          uploadReleaseName: "Example.Release.2026.1080p-GRP",
          status: "ready",
          endpoint: "https://tracker.invalid/upload",
          fields: [{ key: "category", value: "1" }],
          files: [{ field: "torrent", present: true }],
          clientInjection: { status: "completed", message: "Injected." },
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["dryRunResult"]>;
    const result = {
      status: "failed",
      results: [
        {
          trackerId: "EXAMPLE",
          status: "failed",
          submissionStatus: "failed",
          clientInjectionStatus: "pending",
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["result"]>;
    renderPage(uploadFacet({ dryRunStatus: "ready", dryRunResult, result }, { retry }));

    fireEvent.click(screen.getByRole("button", { name: "Expand Example Tracker" }));
    expect(screen.getByText("Example.Release.2026.1080p-GRP")).toBeInTheDocument();
    expect(screen.getByText("category: 1")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry failed uploads" }));
    expect(retry).toHaveBeenCalledOnce();
  });

  it("offers client-only retry for completed submissions", () => {
    const retryClientInjection = vi.fn(async () => true);
    const result = {
      status: "partial",
      results: [
        {
          trackerId: "EXAMPLE",
          status: "partial",
          submissionStatus: "completed",
          clientInjectionStatus: "failed",
          clientFailureCode: "client_injection",
          clientInjectionMessage: "Exact-torrent client injection failed.",
        },
      ],
    } as unknown as NonNullable<UploadFacet["view"]["result"]>;
    renderPage(uploadFacet({ result }, { retryClientInjection }));

    expect(screen.queryByRole("button", { name: "Retry failed uploads" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry client injection" }));
    expect(retryClientInjection).toHaveBeenCalledOnce();
    expect(
      screen.getByText(
        "Submission: completed · Client injection: failed · Exact-torrent client injection failed.",
      ),
    ).toBeInTheDocument();
  });
});
