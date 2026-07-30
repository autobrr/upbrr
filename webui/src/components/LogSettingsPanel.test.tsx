// Copyright (c) 2025-2026, Audionut and the autobrr contributors.
// SPDX-License-Identifier: GPL-2.0-or-later

import { screen, waitFor } from "@testing-library/dom";
import { cleanup, render } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import LogSettingsPanel from "./LogSettingsPanel";
import { loggingClient } from "../api/app";
import { subscribeWebEvent } from "../api/client";

vi.mock("../api/app", () => ({
  loggingClient: {
    getPath: vi.fn(),
    getRecent: vi.fn(),
    getExclusions: vi.fn(),
    startStream: vi.fn(),
    stopStream: vi.fn(),
    updateExclusions: vi.fn(),
  },
}));

vi.mock("../api/client", () => ({
  subscribeWebEvent: vi.fn(() => vi.fn()),
}));

const installLogAPI = () => {
  vi.mocked(loggingClient.getPath).mockResolvedValue("C:/logs/upbrr.log");
  vi.mocked(loggingClient.getRecent).mockResolvedValue([]);
  vi.mocked(loggingClient.getExclusions).mockResolvedValue([]);
  vi.mocked(loggingClient.startStream).mockResolvedValue("stream-1");
  vi.mocked(loggingClient.stopStream).mockResolvedValue(undefined);
  vi.mocked(loggingClient.updateExclusions).mockResolvedValue(undefined);
};

describe("LogSettingsPanel", () => {
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
  });

  it("renders log path and updates logging level", async () => {
    installLogAPI();
    const updateConfigValue = vi.fn();

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={updateConfigValue}
      />,
    );

    await waitFor(() => expect(screen.getByText("C:/logs/upbrr.log")).toBeInTheDocument());

    await userEvent.selectOptions(screen.getByLabelText("Verbosity"), "debug");

    expect(updateConfigValue).toHaveBeenCalledWith(["Logging", "Level"], "debug");
  });

  it("keeps distinct rows when a restarted log stream reuses IDs", async () => {
    installLogAPI();
    vi.mocked(loggingClient.getRecent).mockResolvedValueOnce([
      {
        ID: 1,
        Time: "2026-06-15T00:00:00.000Z",
        Level: "info",
        Message: "before restart",
      },
    ]);
    vi.mocked(loggingClient.getRecent).mockResolvedValueOnce([
      {
        ID: 1,
        Time: "2026-06-15T00:01:00.000Z",
        Level: "info",
        Message: "after restart",
      },
    ]);

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={vi.fn()}
      />,
    );

    await waitFor(() => expect(screen.getByText("before restart")).toBeInTheDocument());
    await waitFor(() => expect(screen.getByText("after restart")).toBeInTheDocument());
  });

  it("dedupes exact recent/live overlap rows", async () => {
    installLogAPI();
    const row = {
      ID: 2,
      Time: "2026-06-15T00:00:00.000Z",
      Level: "info",
      Message: "overlap",
    };
    vi.mocked(loggingClient.getRecent).mockResolvedValue([row]);
    vi.mocked(subscribeWebEvent).mockImplementationOnce((_eventName, callback) => {
      callback(row);
      return vi.fn();
    });

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={vi.fn()}
      />,
    );

    await waitFor(() => expect(subscribeWebEvent).toHaveBeenCalled());
    await waitFor(() => expect(screen.getAllByText("overlap")).toHaveLength(1));
  });

  it("does not restart the log stream when auto-scroll changes", async () => {
    installLogAPI();

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={vi.fn()}
      />,
    );

    await waitFor(() => expect(loggingClient.startStream).toHaveBeenCalledTimes(1));

    await userEvent.click(screen.getByLabelText("Auto-scroll logs"));

    expect(loggingClient.stopStream).not.toHaveBeenCalled();
    expect(loggingClient.startStream).toHaveBeenCalledTimes(1);
  });

  it("stops a started stream when event subscription fails", async () => {
    installLogAPI();
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.mocked(subscribeWebEvent).mockImplementationOnce(() => {
      throw new Error("subscribe failed");
    });

    render(
      <LogSettingsPanel
        configData={{ Logging: { Level: "info" } }}
        fieldMeta={{ Level: { label: "Verbosity" } }}
        renderField={(label) => <div key={label}>{label}</div>}
        updateConfigValue={vi.fn()}
      />,
    );

    try {
      await waitFor(() => expect(loggingClient.stopStream).toHaveBeenCalledWith("stream-1"));
      expect(consoleError).toHaveBeenCalledWith("Failed to start log stream", expect.any(Error));
    } finally {
      consoleError.mockRestore();
    }
  });
});
