// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { AudioAnalysisFacet } from "../../releaseSession/types";
import AudioAnalysisPage from "./index";

afterEach(cleanup);

const facet = (overrides: Partial<AudioAnalysisFacet["view"]> = {}): AudioAnalysisFacet => ({
  view: {
    available: true,
    enabled: false,
    status: "idle",
    releaseGeneration: 4,
    sourceLabel: "Example Release",
    sourceContext: "Matroska",
    primaryTrackID: "audio-main",
    tracks: [
      {
        ID: "audio-main",
        Kind: "audio",
        ResourceID: "resource-one",
        ManifestFingerprint: "a".repeat(64),
        NativeID: "1",
        Ordinal: 1,
        Title: "Main audio",
        Codec: "FLAC",
        ChannelLayout: "5.1",
        Channels: 6,
        SampleRate: 48000,
        DetectedLanguages: ["English"],
        Languages: ["English"],
        LanguageProvenance: "automatic",
        Default: true,
        Commentary: false,
      },
      {
        ID: "audio-commentary",
        Kind: "audio",
        ResourceID: "resource-one",
        ManifestFingerprint: "a".repeat(64),
        NativeID: "2",
        Ordinal: 2,
        Title: "Director commentary",
        Codec: "AAC",
        ChannelLayout: "stereo",
        Channels: 2,
        SampleRate: 48000,
        DetectedLanguages: ["English"],
        Languages: ["English"],
        LanguageProvenance: "automatic",
        Default: false,
        Commentary: true,
      },
    ],
    result: null,
    completed: 0,
    total: 0,
    operationItems: [],
    mutationBlockedReason: "",
    error: "",
    ...overrides,
  },
  generate: vi.fn(async () => true),
  retry: vi.fn(async () => true),
  cancel: vi.fn(async () => true),
  disable: vi.fn(async () => true),
  artifactURL: (artifactID) => `/audio/${artifactID}`,
});

type AnalysisResult = NonNullable<AudioAnalysisFacet["view"]["result"]>;

const resultWithArtifacts = (
  artifacts: AnalysisResult["tracks"][number]["artifacts"],
): AnalysisResult => ({
  id: "analysis-one",
  workflowId: "workflow-one",
  revision: 8,
  release: { SourcePath: "C:\\media\\Example.mkv", Generation: 4 },
  resourceId: "resource-one",
  manifestFingerprint: "a".repeat(64),
  attemptId: "attempt-one",
  selection: "primary",
  trackIds: ["audio-main"],
  variants: artifacts.map((artifact) => artifact.variant),
  profileVersion: "audio-analysis-v2",
  resourceLimits: { decoderThreads: 2 },
  status: "completed",
  tracks: [
    {
      trackId: "audio-main",
      ordinal: 1,
      title: "Main audio",
      codec: "FLAC",
      channelLayout: "5.1",
      channels: 6,
      sampleRate: 48000,
      sampleFrames: 480000,
      durationSeconds: 10,
      status: "completed",
      artifacts,
    },
  ],
  createdAt: "2026-09-21T00:00:00Z",
  completedAt: "2026-09-21T00:01:00Z",
  expiresAt: "2026-09-22T00:00:00Z",
});

describe("AudioAnalysisPage", () => {
  it("submits the prepared primary track and all outputs by default", () => {
    const value = facet();
    render(<AudioAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);

    fireEvent.click(screen.getByRole("button", { name: "Generate" }));

    expect(screen.queryByRole("combobox", { name: /Maximum input rate/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("combobox", { name: /Spectrogram detail/ })).not.toBeInTheDocument();
    expect(value.generate).toHaveBeenCalledWith({
      resourceID: "resource-one",
      selection: "primary",
      trackIDs: ["audio-main"],
      variants: ["waveform", "spectrogram", "stats"],
      resourceLimits: { decoderThreads: 2 },
    });
    expect(screen.getByText(/Director commentary/)).toBeInTheDocument();
    expect(screen.getAllByText(/commentary/)).toHaveLength(2);
  });

  it("requires a checkbox selection in Selected mode", () => {
    const value = facet();
    render(<AudioAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);

    fireEvent.click(screen.getByRole("radio", { name: "Selected" }));
    expect(screen.getByRole("alert")).toHaveTextContent("Select at least one audio track.");
    expect(screen.getByRole("button", { name: "Generate" })).toBeDisabled();
    fireEvent.click(screen.getByRole("checkbox", { name: /Director commentary/ }));
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));

    expect(value.generate).toHaveBeenCalledWith(
      expect.objectContaining({ selection: "selected", trackIDs: ["audio-commentary"] }),
    );
  });

  it("submits adjusted decoder threads", () => {
    const value = facet();
    render(<AudioAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);

    fireEvent.change(
      within(screen.getByRole("group", { name: "Tracks" })).getByRole("combobox", {
        name: /Threads per decoder/,
      }),
      {
        target: { value: "1" },
      },
    );
    fireEvent.click(screen.getByRole("button", { name: "Generate" }));

    expect(value.generate).toHaveBeenCalledWith(
      expect.objectContaining({
        resourceLimits: { decoderThreads: 1 },
      }),
    );
  });

  it("blocks mutations and shows progress owned by the active workflow operation", () => {
    const value = facet({
      enabled: true,
      status: "running",
      completed: 1,
      total: 2,
      operationItems: [
        {
          id: "audio-main:waveform",
          kind: "audio_output",
          label: "Track 1 waveform",
          status: "completed",
          completed: 1,
          total: 1,
          message: "Analysis image completed.",
        },
        {
          id: "audio-main:spectrogram",
          kind: "audio_output",
          label: "Track 1 spectrogram",
          status: "failed",
          completed: 1,
          total: 1,
          message: "Analysis image failed.",
        },
      ],
      mutationBlockedReason: "",
    });
    render(<AudioAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);

    expect(screen.getByRole("button", { name: "Generate" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
    expect(screen.getByText("Generating audio analysis… 1/2")).toBeInTheDocument();
    expect(screen.getByText(/Track 1 waveform: completed — 100%/)).toBeInTheDocument();
    expect(
      screen.getByText(/Track 1 spectrogram: failed — Analysis image failed/),
    ).toBeInTheDocument();

    cleanup();
    const blocked = facet({
      enabled: true,
      mutationBlockedReason:
        "Another workflow operation (upload execute) is running. Wait for it to finish before changing audio analysis.",
    });
    render(
      <AudioAnalysisPage facet={blocked} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />,
    );

    expect(screen.getByRole("button", { name: "Generate" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Disable" })).toBeDisabled();
    expect(
      screen.getByText(/Another workflow operation \(upload execute\) is running/),
    ).toBeInTheDocument();
  });

  it("renders an authorized preview and native download for a retained artifact", () => {
    const setLightboxImage = vi.fn();
    const value = facet({
      enabled: true,
      status: "ready",
      result: resultWithArtifacts([
        {
          id: "waveform-one",
          variant: "waveform",
          status: "completed",
          width: 1812,
          height: 980,
        },
      ]),
    });
    render(
      <AudioAnalysisPage
        facet={value}
        setLightboxImage={setLightboxImage}
        setLightboxAlt={vi.fn()}
      />,
    );

    expect(screen.getByRole("link", { name: "Download native PNG" })).toHaveAttribute(
      "href",
      "/audio/waveform-one",
    );
    const thumbnail = screen.getByRole("button", {
      name: "Open Track 1: Main audio waveform full size",
    });
    expect(thumbnail).toHaveClass("audio-analysis-thumbnail");
    fireEvent.click(thumbnail);
    expect(setLightboxImage).toHaveBeenCalledWith("/audio/waveform-one");
  });

  it("shows retained amplitude statistics as bounded text with a file download", () => {
    const statistics = "DC offset   0.000000\nRMS lev dB    -29.68\n";
    const setLightboxImage = vi.fn();
    const value = facet({
      enabled: true,
      status: "ready",
      result: resultWithArtifacts([
        { id: "stats-one", variant: "stats", status: "completed", text: statistics },
      ]),
    });
    render(
      <AudioAnalysisPage
        facet={value}
        setLightboxImage={setLightboxImage}
        setLightboxAlt={vi.fn()}
      />,
    );

    const output = screen.getByRole("region", {
      name: "Track 1: Main audio amplitude statistics",
    });
    expect(output.tagName).toBe("PRE");
    expect(output).toHaveAttribute("tabindex", "0");
    expect(output.textContent).toBe(statistics);
    expect(output).toHaveClass("max-h-40", "overflow-auto", "whitespace-pre", "font-mono");
    expect(screen.getByRole("link", { name: "Download text file" })).toHaveAttribute(
      "href",
      "/audio/stats-one",
    );
    expect(screen.getByRole("link", { name: "Download text file" })).toHaveAttribute("download");
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Open .* stats full size/ }),
    ).not.toBeInTheDocument();
    expect(setLightboxImage).not.toHaveBeenCalled();
  });

  it("shows a failed statistics artifact while preserving a completed waveform", () => {
    const value = facet({
      enabled: true,
      status: "ready",
      result: {
        ...resultWithArtifacts([
          { id: "waveform-one", variant: "waveform", status: "completed" },
          {
            id: "",
            variant: "stats",
            status: "failed",
            failure: { code: "output_failed", message: "Could not publish audio statistics." },
          },
        ]),
        status: "partial",
      },
    });
    render(<AudioAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);

    expect(
      screen.getByRole("button", { name: "Open Track 1: Main audio waveform full size" }),
    ).toBeEnabled();
    expect(screen.getByRole("link", { name: "Download native PNG" })).toHaveAttribute(
      "href",
      "/audio/waveform-one",
    );
    expect(screen.getByText("output_failed: Could not publish audio statistics.")).toHaveClass(
      "error",
    );
    expect(screen.queryByRole("link", { name: "Download text file" })).not.toBeInTheDocument();
    expect(screen.queryByText("No retained statistics are available.")).not.toBeInTheDocument();
  });

  it("offers the exact retry action for an interrupted retained result", () => {
    const value = facet({
      enabled: true,
      status: "ready",
      result: {
        id: "analysis-interrupted",
        workflowId: "workflow-one",
        revision: 8,
        release: { SourcePath: "C:\\media\\Example.mkv", Generation: 4 },
        resourceId: "resource-one",
        manifestFingerprint: "a".repeat(64),
        attemptId: "attempt-interrupted",
        selection: "primary",
        trackIds: ["audio-main"],
        variants: ["waveform", "spectrogram"],
        profileVersion: "audio-analysis-v2",
        resourceLimits: { decoderThreads: 2 },
        status: "interrupted",
        tracks: [],
        createdAt: "2026-09-21T00:00:00Z",
        completedAt: "2026-09-21T00:01:00Z",
        expiresAt: "2026-09-22T00:00:00Z",
      },
    });
    render(<AudioAnalysisPage facet={value} setLightboxImage={vi.fn()} setLightboxAlt={vi.fn()} />);

    fireEvent.click(screen.getByRole("button", { name: "Retry failed work" }));

    expect(value.retry).toHaveBeenCalledOnce();
  });
});
